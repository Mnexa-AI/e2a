package dkim

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	msgauth "github.com/emersion/go-msgauth/dkim"
)

func TestSelectorForTime_FixedMonth(t *testing.T) {
	got := SelectorForTime(time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC))
	if got != "e2a202605" {
		t.Errorf("SelectorForTime = %q, want e2a202605", got)
	}
}

func TestGenerateKeypair_RoundTrip(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	if kp.Selector == "" {
		t.Error("selector is empty")
	}
	if kp.PublicKeyDNS == "" {
		t.Error("public key is empty")
	}
	if len(kp.PrivateKeyDER) == 0 {
		t.Error("private key DER is empty")
	}

	// The private key must round-trip through PKCS#1.
	priv, err := x509.ParsePKCS1PrivateKey(kp.PrivateKeyDER)
	if err != nil {
		t.Fatalf("ParsePKCS1PrivateKey: %v", err)
	}
	if priv.N.BitLen() != 2048 {
		t.Errorf("key bit length = %d, want 2048", priv.N.BitLen())
	}

	// The base64 public key must decode to a SubjectPublicKeyInfo whose
	// modulus matches the private key.
	pubDER, err := base64.StdEncoding.DecodeString(kp.PublicKeyDNS)
	if err != nil {
		t.Fatalf("base64 decode public key: %v", err)
	}
	pubAny, err := x509.ParsePKIXPublicKey(pubDER)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey: %v", err)
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *rsa.PublicKey", pubAny)
	}
	if pub.N.Cmp(priv.N) != 0 {
		t.Error("public and private moduli do not match — keypair is broken")
	}
}

func TestSign_PrependsDKIMSignatureHeader(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	msg := []byte(
		"From: bot@example.com\r\n" +
			"To: alice@elsewhere.test\r\n" +
			"Subject: hello\r\n" +
			"Date: Fri, 22 May 2026 12:00:00 +0000\r\n" +
			"Message-ID: <abc@example.com>\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"\r\n" +
			"hi there\r\n",
	)

	signed, err := Sign(msg, "example.com", kp.Selector, kp.PrivateKeyDER)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// DKIM-Signature must be the first header in the output.
	if !bytes.HasPrefix(signed, []byte("DKIM-Signature:")) {
		head := signed
		if len(head) > 80 {
			head = head[:80]
		}
		t.Fatalf("signed message must begin with DKIM-Signature header; first 80 bytes:\n%s", head)
	}

	// The signed message must verify with the public key portion of the
	// generated keypair. We construct an in-memory verifier rather than
	// going through DNS by writing a small lookup that returns the
	// public key for this selector+domain.
	verifier := func(domain, selector string) (string, error) {
		if domain != "example.com" || selector != kp.Selector {
			return "", nil
		}
		return "v=DKIM1; k=rsa; p=" + kp.PublicKeyDNS, nil
	}
	verifications, err := msgauth.VerifyWithOptions(bytes.NewReader(signed), &msgauth.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			// name is "{selector}._domainkey.{domain}"
			parts := strings.SplitN(name, "._domainkey.", 2)
			if len(parts) != 2 {
				return nil, nil
			}
			v, err := verifier(parts[1], parts[0])
			if err != nil {
				return nil, err
			}
			return []string{v}, nil
		},
	})
	if err != nil {
		t.Fatalf("VerifyWithOptions: %v", err)
	}
	if len(verifications) != 1 {
		t.Fatalf("expected 1 verification result, got %d", len(verifications))
	}
	if verifications[0].Err != nil {
		t.Errorf("signature did not verify: %v", verifications[0].Err)
	}
}

func TestSign_RejectsEmptyKey(t *testing.T) {
	_, err := Sign([]byte("From: a@b\r\n\r\nx"), "b", "e2a", nil)
	if err == nil {
		t.Error("expected error for empty private key, got nil")
	}
}

func TestSign_CoversManagedUnsubscribeHeaders(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("From: bot@example.com\r\nTo: a@example.net\r\nSubject: hi\r\nDate: Fri, 22 May 2026 12:00:00 +0000\r\nList-Unsubscribe: <https://api.example/u/x>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n\r\nbody")
	signed, err := Sign(msg, "example.com", kp.Selector, kp.PrivateKeyDER)
	if err != nil {
		t.Fatal(err)
	}
	head := string(signed[:bytes.Index(signed, []byte("\r\n\r\n"))])
	if !strings.Contains(strings.ToLower(head), "list-unsubscribe:list-unsubscribe-post") {
		t.Fatalf("DKIM h= does not cover both unsubscribe headers:\n%s", head)
	}
}

func TestDNSRecord_Format(t *testing.T) {
	name, value := DNSRecord("e2a202605", "mail.acme.com", "ABCDEF")
	if name != "e2a202605._domainkey.mail.acme.com" {
		t.Errorf("name = %q", name)
	}
	if value != "v=DKIM1; k=rsa; p=ABCDEF" {
		t.Errorf("value = %q", value)
	}
}

func TestExtractPublicKeyFromTXT(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"v=DKIM1; k=rsa; p=ABCDEF", "ABCDEF"},
		{"v=DKIM1;k=rsa;p=ABCDEF", "ABCDEF"},
		// Whitespace in the middle of a long key — TXT splits across
		// 255-char chunks sometimes inject these.
		{"v=DKIM1; k=rsa; p=ABC DEF GHI", "ABCDEFGHI"},
		// p= followed by another tag (defensive — RFC says p= is last).
		{"v=DKIM1; p=XYZ; t=y", "XYZ"},
		// No p= → empty.
		{"v=DKIM1; k=rsa", ""},
	}
	for _, c := range cases {
		got := ExtractPublicKeyFromTXT(c.in)
		if got != c.want {
			t.Errorf("ExtractPublicKeyFromTXT(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
