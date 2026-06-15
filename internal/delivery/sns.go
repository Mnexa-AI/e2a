package delivery

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SNS SignatureVersion 1 mandates SHA1; AWS controls the signing.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// SNSMessage is the standard Amazon SNS HTTP/S POST body. The SES delivery
// notifications arrive wrapped in this envelope; Message carries the SES event
// JSON (see ParseSESNotification). Field tags match the SNS wire format.
type SNSMessage struct {
	Type             string `json:"Type"` // Notification | SubscriptionConfirmation | UnsubscribeConfirmation
	MessageId        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Subject          string `json:"Subject,omitempty"` // Notifications only, optional
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	// SubscribeURL and Token are present only on SubscriptionConfirmation and
	// UnsubscribeConfirmation envelopes.
	SubscribeURL string `json:"SubscribeURL,omitempty"`
	Token        string `json:"Token,omitempty"`
}

// CertFetcher fetches the bytes of an SNS signing certificate (PEM). Injected as
// a seam so the signature path is testable without network access.
type CertFetcher func(ctx context.Context, url string) ([]byte, error)

// snsHostRE is the SSRF guard for SigningCertURL / SubscribeURL hosts: only the
// canonical regional SNS endpoints (e.g. sns.us-east-2.amazonaws.com).
var snsHostRE = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com$`)

// HTTPCertFetcher is the production CertFetcher: an HTTPS GET with a short
// timeout that returns the PEM cert bytes. It rejects non-2xx responses. The
// URL must already have passed the host allow-list (see validSigningCertHost).
func HTTPCertFetcher(ctx context.Context, certURL string) ([]byte, error) {
	// No redirect-following: the cert URL host is already allow-listed
	// (sns.*.amazonaws.com); a redirect must not carry the fetch to an internal
	// host — SSRF defense in depth.
	c := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build cert request: %w", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch signing cert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch signing cert: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read signing cert: %w", err)
	}
	return body, nil
}

// Verifier validates the authenticity of an SNS message. Because the SES event
// ingress is a public, unauthenticated HTTP endpoint, signature verification is
// a fail-closed security boundary: any failure rejects the message.
type Verifier struct {
	allowed map[string]struct{} // allow-listed TopicArns; empty ⇒ reject all
	fetch   CertFetcher
}

// NewVerifier builds a Verifier. allowedTopicARNs is the set of SNS topics e2a
// trusts; an empty set rejects every message (fail closed). fetch retrieves the
// signing certificate (use HTTPCertFetcher in production).
func NewVerifier(allowedTopicARNs []string, fetch CertFetcher) *Verifier {
	allowed := make(map[string]struct{}, len(allowedTopicARNs))
	for _, a := range allowedTopicARNs {
		allowed[a] = struct{}{}
	}
	return &Verifier{allowed: allowed, fetch: fetch}
}

// Verify checks an SNS message fail-closed and returns nil only if it is
// authentic and from a trusted topic. The checks run in order so the cheap,
// no-network guards (topic allow-list, URL host) reject before any fetch.
func (v *Verifier) Verify(ctx context.Context, m *SNSMessage) error {
	if m == nil {
		return fmt.Errorf("sns: nil message")
	}

	// (a) TopicArn allow-list. An empty allow-list rejects everything.
	if _, ok := v.allowed[m.TopicArn]; !ok {
		return fmt.Errorf("sns: topic not allowed: %q", m.TopicArn)
	}

	// (b) SigningCertURL must be an absolute HTTPS URL on a canonical SNS host.
	certURL, err := url.Parse(m.SigningCertURL)
	if err != nil {
		return fmt.Errorf("sns: parse SigningCertURL: %w", err)
	}
	if !validSigningCertHost(certURL) {
		return fmt.Errorf("sns: SigningCertURL host not allowed: %q", m.SigningCertURL)
	}

	// (c) Fetch + parse the signing certificate, extract its RSA public key.
	pemBytes, err := v.fetch(ctx, m.SigningCertURL)
	if err != nil {
		return fmt.Errorf("sns: fetch signing cert: %w", err)
	}
	pub, err := rsaPublicKeyFromPEM(pemBytes)
	if err != nil {
		return fmt.Errorf("sns: %w", err)
	}

	// (d) Canonical string-to-sign (field set + order depend on Type).
	canonical, err := canonicalStringToSign(m)
	if err != nil {
		return fmt.Errorf("sns: %w", err)
	}

	// (e) Hash per SignatureVersion, base64-decode Signature, verify PKCS1v15.
	hashAlg, digest, err := hashForVersion(m.SignatureVersion, []byte(canonical))
	if err != nil {
		return fmt.Errorf("sns: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("sns: decode signature: %w", err)
	}
	if err := rsa.VerifyPKCS1v15(pub, hashAlg, digest, sig); err != nil {
		return fmt.Errorf("sns: signature verification failed: %w", err)
	}
	return nil
}

// IsSubscriptionConfirmation reports whether m is a topic subscription handshake
// that the HTTP handler should confirm.
func (v *Verifier) IsSubscriptionConfirmation(m *SNSMessage) bool {
	return m != nil && m.Type == "SubscriptionConfirmation"
}

// ConfirmSubscriptionURL returns the SubscribeURL to GET (out of band) to
// confirm a subscription, but only for a SubscriptionConfirmation envelope whose
// SubscribeURL is an allow-listed HTTPS sns.*.amazonaws.com host (SSRF guard).
// It does not perform the GET — the caller does, after Verify has passed.
func ConfirmSubscriptionURL(m *SNSMessage) (string, bool) {
	if m == nil || m.Type != "SubscriptionConfirmation" || m.SubscribeURL == "" {
		return "", false
	}
	u, err := url.Parse(m.SubscribeURL)
	if err != nil || !validSigningCertHost(u) {
		return "", false
	}
	return m.SubscribeURL, true
}

// validSigningCertHost is the SSRF guard for SNS-supplied URLs: an absolute
// HTTPS URL, no userinfo, the default port, and a canonical SNS host.
func validSigningCertHost(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.Host == "" {
		return false
	}
	if u.User != nil {
		return false
	}
	if u.Port() != "" { // any explicit port (even :443) is non-canonical for SNS URLs
		return false
	}
	return snsHostRE.MatchString(u.Hostname())
}

// canonicalStringToSign builds the AWS SNS string-to-sign: the signed fields in
// a Type-specific order, each emitted as "key\nvalue\n".
func canonicalStringToSign(m *SNSMessage) (string, error) {
	var fields []string
	switch m.Type {
	case "Notification":
		// Subject is included only when present.
		fields = []string{"Message", m.Message, "MessageId", m.MessageId}
		if m.Subject != "" {
			fields = append(fields, "Subject", m.Subject)
		}
		fields = append(fields, "Timestamp", m.Timestamp, "TopicArn", m.TopicArn, "Type", m.Type)
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		fields = []string{
			"Message", m.Message,
			"MessageId", m.MessageId,
			"SubscribeURL", m.SubscribeURL,
			"Timestamp", m.Timestamp,
			"Token", m.Token,
			"TopicArn", m.TopicArn,
			"Type", m.Type,
		}
	default:
		return "", fmt.Errorf("unsupported message type: %q", m.Type)
	}
	out := make([]byte, 0, 256)
	for i := 0; i < len(fields); i += 2 {
		out = append(out, fields[i]...)
		out = append(out, '\n')
		out = append(out, fields[i+1]...)
		out = append(out, '\n')
	}
	return string(out), nil
}

// hashForVersion maps the SNS SignatureVersion to its hash algorithm and returns
// the digest of data. "1" ⇒ SHA1, "2" ⇒ SHA256; anything else is rejected.
func hashForVersion(version string, data []byte) (crypto.Hash, []byte, error) {
	switch version {
	case "1":
		sum := sha1.Sum(data) //nolint:gosec // AWS SNS v1 signatures are SHA1.
		return crypto.SHA1, sum[:], nil
	case "2":
		sum := sha256.Sum256(data)
		return crypto.SHA256, sum[:], nil
	default:
		return 0, nil, fmt.Errorf("unsupported SignatureVersion: %q", version)
	}
}

// rsaPublicKeyFromPEM decodes a PEM certificate and extracts its RSA public key.
func rsaPublicKeyFromPEM(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("decode signing cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse signing cert: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("signing cert public key is not RSA")
	}
	return pub, nil
}
