package delivery

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // testing the SNS v1 (SHA1) signing path.
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"
)

const testTopicARN = "arn:aws:sns:us-east-2:123456789012:e2a-delivery"

// testCert generates an RSA key + self-signed cert and returns the key plus a
// CertFetcher that serves the PEM-encoded cert.
func testCert(t *testing.T) (*rsa.PrivateKey, CertFetcher) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	fetch := func(_ context.Context, _ string) ([]byte, error) { return pemBytes, nil }
	return key, fetch
}

// sign signs the canonical string of m with key using the algorithm for
// SignatureVersion and writes the base64 signature into m.Signature.
func sign(t *testing.T, key *rsa.PrivateKey, m *SNSMessage) {
	t.Helper()
	canonical, err := canonicalStringToSign(m)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	var hash crypto.Hash
	var digest []byte
	switch m.SignatureVersion {
	case "1":
		sum := sha1.Sum([]byte(canonical)) //nolint:gosec // v1 path under test.
		hash, digest = crypto.SHA1, sum[:]
	case "2":
		sum := sha256.Sum256([]byte(canonical))
		hash, digest = crypto.SHA256, sum[:]
	default:
		t.Fatalf("test sign: unsupported version %q", m.SignatureVersion)
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, hash, digest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
}

func notification(version string) *SNSMessage {
	return &SNSMessage{
		Type:             "Notification",
		MessageId:        "msg-1",
		TopicArn:         testTopicARN,
		Subject:          "delivery",
		Message:          `{"eventType":"Delivery"}`,
		Timestamp:        "2026-06-15T00:00:00.000Z",
		SignatureVersion: version,
		SigningCertURL:   "https://sns.us-east-2.amazonaws.com/SimpleNotificationService-x.pem",
	}
}

func TestSNSVerifyNotification(t *testing.T) {
	for _, version := range []string{"1", "2"} {
		t.Run("v"+version, func(t *testing.T) {
			key, fetch := testCert(t)
			m := notification(version)
			sign(t, key, m)
			v := NewVerifier([]string{testTopicARN}, fetch)
			if err := v.Verify(context.Background(), m); err != nil {
				t.Fatalf("expected valid signature, got: %v", err)
			}
		})
	}
}

func TestSNSVerifyTamperedMessage(t *testing.T) {
	key, fetch := testCert(t)
	m := notification("1")
	sign(t, key, m)
	m.Message = `{"eventType":"Bounce"}` // changed after signing
	v := NewVerifier([]string{testTopicARN}, fetch)
	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected verification failure for tampered Message")
	}
}

func TestSNSVerifyGarbageSignature(t *testing.T) {
	key, fetch := testCert(t)
	m := notification("2")
	sign(t, key, m)
	m.Signature = base64.StdEncoding.EncodeToString([]byte("not a real signature"))
	v := NewVerifier([]string{testTopicARN}, fetch)
	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected verification failure for garbage signature")
	}

	// Also: non-base64 signature.
	m2 := notification("2")
	sign(t, key, m2)
	m2.Signature = "!!!not base64!!!"
	if err := v.Verify(context.Background(), m2); err == nil {
		t.Fatal("expected verification failure for non-base64 signature")
	}
}

func TestSNSValidSigningCertHost(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://sns.us-east-2.amazonaws.com/x.pem", true},
		{"https://sns.eu-west-1.amazonaws.com/SimpleNotificationService.pem", true},
		{"http://sns.us-east-2.amazonaws.com/x.pem", false},
		{"https://evil.com/x.pem", false},
		{"https://sns.us-east-2.amazonaws.com.evil.com/x.pem", false},
		{"https://user:pass@sns.us-east-2.amazonaws.com/x.pem", false},
		{"https://example.amazonaws.com/x.pem", false},
		{"https://sns.us-east-2.amazonaws.com:8443/x.pem", false},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			u, err := url.Parse(c.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := validSigningCertHost(u); got != c.want {
				t.Fatalf("validSigningCertHost(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestSNSVerifyTopicNotAllowed(t *testing.T) {
	key, fetch := testCert(t)
	m := notification("1")
	m.TopicArn = "arn:aws:sns:us-east-2:000000000000:other"
	sign(t, key, m)
	v := NewVerifier([]string{testTopicARN}, fetch)
	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected rejection: topic not in allow-list")
	}
}

func TestSNSVerifyEmptyAllowListRejects(t *testing.T) {
	key, fetch := testCert(t)
	m := notification("1")
	sign(t, key, m)
	// Empty allow-list ⇒ fail closed even with an otherwise-valid signature.
	v := NewVerifier(nil, fetch)
	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected rejection: empty allow-list must reject all")
	}
}

func TestSNSVerifyUnsupportedSignatureVersion(t *testing.T) {
	key, fetch := testCert(t)
	m := notification("1")
	sign(t, key, m) // signature itself is fine; version field is what's rejected
	m.SignatureVersion = "3"
	v := NewVerifier([]string{testTopicARN}, fetch)
	if err := v.Verify(context.Background(), m); err == nil {
		t.Fatal("expected rejection: unsupported SignatureVersion")
	}
}

func TestSNSSubscriptionConfirmation(t *testing.T) {
	key, fetch := testCert(t)
	m := &SNSMessage{
		Type:             "SubscriptionConfirmation",
		MessageId:        "msg-sub-1",
		TopicArn:         testTopicARN,
		Message:          "You have chosen to subscribe...",
		Token:            "tok-123",
		SubscribeURL:     "https://sns.us-east-2.amazonaws.com/?Action=ConfirmSubscription&Token=tok-123",
		Timestamp:        "2026-06-15T00:00:00.000Z",
		SignatureVersion: "1",
		SigningCertURL:   "https://sns.us-east-2.amazonaws.com/SimpleNotificationService-x.pem",
	}
	sign(t, key, m)
	v := NewVerifier([]string{testTopicARN}, fetch)
	if err := v.Verify(context.Background(), m); err != nil {
		t.Fatalf("expected valid subscription confirmation, got: %v", err)
	}
	if !v.IsSubscriptionConfirmation(m) {
		t.Fatal("IsSubscriptionConfirmation should be true")
	}

	got, ok := ConfirmSubscriptionURL(m)
	if !ok || got != m.SubscribeURL {
		t.Fatalf("ConfirmSubscriptionURL = (%q, %v), want (%q, true)", got, ok, m.SubscribeURL)
	}

	// A SubscribeURL on a non-allow-listed host must be refused.
	m.SubscribeURL = "https://evil.com/?Action=ConfirmSubscription"
	if _, ok := ConfirmSubscriptionURL(m); ok {
		t.Fatal("ConfirmSubscriptionURL must reject a non-allow-listed host")
	}
}
