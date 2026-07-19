// Package dkim wraps github.com/emersion/go-msgauth/dkim for the
// per-domain signing path.
//
// Responsibilities split:
//
//   - GenerateKeypair generates a fresh RSA-2048 keypair and returns the
//     three things the rest of the system needs: a DNS-friendly selector,
//     the base64 public key for the user's DNS TXT value, and the
//     PKCS#1-DER private key for BYTEA storage.
//
//   - Sign takes a fully composed RFC 5322 message body, looks up the
//     matching keypair by selector + domain, and returns the message
//     with a DKIM-Signature header prepended. Callers that don't have a
//     key (legacy domains, the seeded shared domain) skip this and the
//     downstream SMTP relay falls back to the deployment-level signing
//     it has always done.
//
//   - DNSRecord renders the TXT record the user must publish to make
//     their key resolvable. The shape is fixed by RFC 6376 §3.6.1; the
//     selector convention "e2a{YYYYMM}" matches what we tell users in
//     the Get-started UI.
//
// The 2048-bit RSA choice mirrors what every major mailbox provider
// recommends for new selectors as of 2026. Ed25519 keys are smaller but
// not all receivers verify them yet — switch when adoption catches up.
package dkim

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	msgauth "github.com/emersion/go-msgauth/dkim"
)

// SelectorForNow returns the selector used for new keypairs at the
// current wall-clock month. The "e2a" prefix scopes the selector to
// this product so users hosting both e2a and another mail provider
// under the same domain can keep selectors disjoint. The YYYYMM tail
// lets us rotate selectors monthly without colliding with existing
// records — the rotated row reuses the same column, but selector
// changes mean DNS lookups land on a new key.
func SelectorForNow() string {
	return SelectorForTime(time.Now().UTC())
}

// SelectorForTime is the testable variant of SelectorForNow.
func SelectorForTime(t time.Time) string {
	return fmt.Sprintf("e2a%04d%02d", t.Year(), int(t.Month()))
}

// Keypair is the result of GenerateKeypair. PrivateKeyDER is suitable
// for direct BYTEA storage; PublicKeyDNS is the literal "p=" value for
// the TXT record.
type Keypair struct {
	Selector      string
	PublicKeyDNS  string
	PrivateKeyDER []byte
}

// GenerateKeypair mints a fresh RSA-2048 keypair scoped to the current
// month's selector. PrivateKeyDER is PKCS#1 DER (parseable with
// x509.ParsePKCS1PrivateKey); PublicKeyDNS is the base64 SPKI value
// with the PEM header/footer/newlines stripped so it can be pasted
// straight into a TXT record's "p=" field.
func GenerateKeypair() (*Keypair, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("rsa keygen: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	return &Keypair{
		Selector:      SelectorForNow(),
		PublicKeyDNS:  base64.StdEncoding.EncodeToString(pubDER),
		PrivateKeyDER: x509.MarshalPKCS1PrivateKey(key),
	}, nil
}

// Sign prepends a DKIM-Signature header to the given RFC 5322 message
// body, signed with the supplied private key for "{selector}.{domain}".
//
// We only sign the From, To, Subject, Date and Message-ID headers (plus
// any References / In-Reply-To that may be present). Signing every
// header is brittle — receivers reject messages whose intermediary
// MTAs rewrite or fold a covered header. The whitelist keeps DMARC
// alignment intact while tolerating typical Send-via-SES rewrites.
func Sign(message []byte, domain, selector string, privateKeyDER []byte) ([]byte, error) {
	if domain == "" || selector == "" {
		return nil, fmt.Errorf("dkim: domain and selector required")
	}
	if len(privateKeyDER) == 0 {
		return nil, fmt.Errorf("dkim: empty private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(privateKeyDER)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	opts := &msgauth.SignOptions{
		Domain:                 domain,
		Selector:               selector,
		Signer:                 key,
		HeaderCanonicalization: msgauth.CanonicalizationRelaxed,
		BodyCanonicalization:   msgauth.CanonicalizationRelaxed,
		HeaderKeys: []string{
			"From", "To", "Cc", "Subject", "Date",
			"Message-ID", "In-Reply-To", "References",
			"MIME-Version", "Content-Type", "Reply-To",
			"List-Unsubscribe", "List-Unsubscribe-Post",
		},
	}

	var signed bytes.Buffer
	if err := msgauth.Sign(&signed, bytes.NewReader(message), opts); err != nil {
		return nil, fmt.Errorf("dkim sign: %w", err)
	}
	return signed.Bytes(), nil
}

// DNSRecord renders the TXT record the user must publish. Returns the
// hostname (left of the apex) and the record value.
//
// Example for selector "e2a202605" + domain "mail.acme.com":
//
//	name  = "e2a202605._domainkey.mail.acme.com"
//	value = "v=DKIM1; k=rsa; p=MIIBIjANBgkq..."
func DNSRecord(selector, domain, publicKeyDNS string) (string, string) {
	name := fmt.Sprintf("%s._domainkey.%s", selector, domain)
	value := fmt.Sprintf("v=DKIM1; k=rsa; p=%s", publicKeyDNS)
	return name, value
}

// ExtractPublicKeyFromTXT pulls the "p=" payload out of a TXT record's
// raw value, trimming any whitespace mail systems sometimes inject when
// splitting the record across multiple strings. Returns "" if the
// payload is missing — callers treat that as "key not yet published".
func ExtractPublicKeyFromTXT(txt string) string {
	const marker = "p="
	i := strings.Index(txt, marker)
	if i < 0 {
		return ""
	}
	tail := txt[i+len(marker):]
	// "p=" must be the last tag in the record per RFC 6376 §3.6.1, but
	// we still defensively cut at the next ";" in case operators
	// reorder tags. Whitespace is stripped because TXT records longer
	// than 255 chars get split with quoted segments — joiners may
	// introduce stray spaces.
	if j := strings.Index(tail, ";"); j >= 0 {
		tail = tail[:j]
	}
	return strings.Join(strings.Fields(tail), "")
}
