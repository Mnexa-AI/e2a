package agentauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
)

func genPKCS1PEM(t *testing.T) (string, *rsa.PrivateKey) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(k)
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return string(p), k
}

func genPKCS8PEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return string(p)
}

// TestSign_RoundTrip: a token signed by the Signer verifies against the public
// JWKS it publishes, carries the kid in its header, and round-trips claims.
func TestSign_RoundTrip(t *testing.T) {
	pemKey, _ := genPKCS1PEM(t)
	s, err := NewSigner(pemKey, "v1")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if !s.Enabled() || s.KeyID() != "v1" {
		t.Fatalf("expected enabled signer with kid v1, got enabled=%v kid=%q", s.Enabled(), s.KeyID())
	}

	claims := jwt.Claims{Subject: "support@acme.com", Audience: jwt.Audience{"https://api.e2a.dev"}, Expiry: jwt.NewNumericDate(time.Unix(2000000000, 0))}
	tok, err := s.Sign(claims, map[string]interface{}{"scope": "agent"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	parsed, err := jwt.ParseSigned(tok)
	if err != nil {
		t.Fatalf("ParseSigned: %v", err)
	}
	if len(parsed.Headers) == 0 || parsed.Headers[0].KeyID != "v1" {
		t.Errorf("expected kid v1 in header, got %+v", parsed.Headers)
	}

	// Verify against the published JWKS.
	jwks := s.PublicJWKS()
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 public key, got %d", len(jwks.Keys))
	}
	var got jwt.Claims
	var priv map[string]interface{}
	if err := parsed.Claims(jwks.Keys[0].Key, &got, &priv); err != nil {
		t.Fatalf("verify with public JWK: %v", err)
	}
	if got.Subject != "support@acme.com" {
		t.Errorf("sub = %q, want support@acme.com", got.Subject)
	}
	if priv["scope"] != "agent" {
		t.Errorf("scope = %v, want agent", priv["scope"])
	}
	// Public JWK must be public-only (no private material) and tagged for sig.
	if jwks.Keys[0].IsPublic() == false {
		t.Error("published JWK must be public-only")
	}
	if jwks.Keys[0].Use != "sig" || jwks.Keys[0].KeyID != "v1" {
		t.Errorf("published JWK use/kid = %q/%q, want sig/v1", jwks.Keys[0].Use, jwks.Keys[0].KeyID)
	}
}

func TestSign_PKCS8Accepted(t *testing.T) {
	s, err := NewSigner(genPKCS8PEM(t), "v1")
	if err != nil {
		t.Fatalf("NewSigner(PKCS8): %v", err)
	}
	if _, err := s.Sign(jwt.Claims{Subject: "x"}, nil); err != nil {
		t.Fatalf("Sign with PKCS8 key: %v", err)
	}
}

// TestDisabledSigner: an empty PEM yields a disabled signer that errs on sign
// and serves an empty (non-nil) JWKS — the off-by-default posture.
func TestDisabledSigner(t *testing.T) {
	s, err := NewSigner("", "")
	if err != nil {
		t.Fatalf("NewSigner(empty): unexpected error %v", err)
	}
	if s.Enabled() {
		t.Error("empty-key signer must be disabled")
	}
	if _, err := s.Sign(jwt.Claims{Subject: "x"}, nil); err != ErrSigningDisabled {
		t.Errorf("Sign on disabled signer = %v, want ErrSigningDisabled", err)
	}
	jwks := s.PublicJWKS()
	if jwks.Keys == nil || len(jwks.Keys) != 0 {
		t.Errorf("disabled JWKS must be empty non-nil, got %+v", jwks.Keys)
	}
}

// TestMalformedKeyHardError: a non-empty but invalid key is a startup error
// (fail fast), not a silently-disabled signer.
func TestMalformedKeyHardError(t *testing.T) {
	if _, err := NewSigner("not a pem", "v1"); err == nil {
		t.Error("expected error for malformed PEM")
	}
	// A well-formed PEM block whose bytes are not a valid key must be rejected.
	// Built at runtime (rather than an embedded key-armor literal) so secret
	// scanners don't false-positive on the test source.
	garbagePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("not-a-real-key")}))
	if _, err := NewSigner(garbagePEM, "v1"); err == nil {
		t.Error("expected error for malformed key bytes inside a valid PEM block")
	}
}

// TestRejectsWeakKey: a syntactically valid but too-small RSA key (1024-bit)
// must fail closed — RS256 with a weak modulus is forgeable (review finding).
func TestRejectsWeakKey(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	if _, err := NewSigner(pemKey, "v1"); err == nil {
		t.Error("expected error for 1024-bit RSA key (< 2048 min)")
	}
}

// TestRejectsECKey: a valid PKCS#8 EC key exercises the non-RSA type-assertion
// branch — RS256 requires RSA, so it must be rejected (review finding).
func TestRejectsECKey(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen ec: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatalf("marshal ec pkcs8: %v", err)
	}
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if _, err := NewSigner(pemKey, "v1"); err == nil {
		t.Error("expected error for EC key (RS256 requires RSA)")
	}
}

// guard against accidental alg drift.
func TestSigningAlgIsRS256(t *testing.T) {
	if SigningAlg != jose.RS256 {
		t.Errorf("SigningAlg = %v, want RS256", SigningAlg)
	}
}
