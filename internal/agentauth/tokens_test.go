package agentauth

import (
	"strings"
	"testing"
	"time"
)

const testIssuer = "https://api.e2a.dev"

func testSigner(t *testing.T) *Signer {
	t.Helper()
	pem, _ := genPKCS1PEM(t)
	s, err := NewSigner(pem, "v1")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestSignVerify_IdentityAssertion(t *testing.T) {
	s := testSigner(t)
	tok, exp, err := s.SignIdentityAssertion("support@acme.com", "agent", 3, testIssuer)
	if err != nil {
		t.Fatalf("SignIdentityAssertion: %v", err)
	}
	if time.Until(exp) < 29*24*time.Hour {
		t.Errorf("identity_assertion TTL too short: %v", time.Until(exp))
	}
	got, err := s.VerifyToken(tok, TypIdentityAssertion, testIssuer)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got.Subject != "support@acme.com" || got.Scope != "agent" || got.AssertionVersion != 3 || got.Type != TypIdentityAssertion {
		t.Errorf("claims = %+v", got)
	}
}

func TestSignVerify_AccessToken(t *testing.T) {
	s := testSigner(t)
	tok, exp, err := s.SignAccessToken("bot@acme.com", "agent", 1, testIssuer)
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	if d := time.Until(exp); d > 16*time.Minute || d < 14*time.Minute {
		t.Errorf("access_token TTL = %v, want ~15m", d)
	}
	got, err := s.VerifyToken(tok, TypAccessToken, testIssuer)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got.Subject != "bot@acme.com" || got.Type != TypAccessToken {
		t.Errorf("claims = %+v", got)
	}
}

// TestVerify_TypeConfusion: an access_token must NOT verify as an
// identity_assertion (and vice versa) — the typ claim is load-bearing, so a
// short-lived access token can't be replayed as the long-lived credential.
func TestVerify_TypeConfusion(t *testing.T) {
	s := testSigner(t)
	at, _, _ := s.SignAccessToken("x@acme.com", "agent", 1, testIssuer)
	if _, err := s.VerifyToken(at, TypIdentityAssertion, testIssuer); err == nil {
		t.Error("access_token must not verify as identity_assertion")
	}
	ia, _, _ := s.SignIdentityAssertion("x@acme.com", "agent", 1, testIssuer)
	if _, err := s.VerifyToken(ia, TypAccessToken, testIssuer); err == nil {
		t.Error("identity_assertion must not verify as access_token")
	}
}

// TestVerify_WrongIssuerOrAudience: a token minted for a different AS host must
// be rejected (prevents cross-deployment token replay).
func TestVerify_WrongIssuerOrAudience(t *testing.T) {
	s := testSigner(t)
	tok, _, _ := s.SignAccessToken("x@acme.com", "agent", 1, testIssuer)
	if _, err := s.VerifyToken(tok, TypAccessToken, "https://evil.example.com"); err == nil {
		t.Error("token must not verify against a different issuer/audience")
	}
}

// TestVerify_WrongKey: a token signed by a different key must not verify.
func TestVerify_WrongKey(t *testing.T) {
	s1 := testSigner(t)
	s2 := testSigner(t)
	tok, _, _ := s1.SignAccessToken("x@acme.com", "agent", 1, testIssuer)
	if _, err := s2.VerifyToken(tok, TypAccessToken, testIssuer); err == nil {
		t.Error("token signed by another key must not verify")
	}
}

func TestVerify_DisabledSigner(t *testing.T) {
	disabled, _ := NewSigner("", "")
	if _, _, err := disabled.SignAccessToken("x", "agent", 1, testIssuer); err != ErrSigningDisabled {
		t.Errorf("sign on disabled = %v, want ErrSigningDisabled", err)
	}
	if _, err := disabled.VerifyToken("whatever", TypAccessToken, testIssuer); err != ErrSigningDisabled {
		t.Errorf("verify on disabled = %v, want ErrSigningDisabled", err)
	}
}

func TestVerify_GarbageToken(t *testing.T) {
	s := testSigner(t)
	for _, bad := range []string{"", "not-a-jwt", "a.b.c", strings.Repeat("x", 50)} {
		if _, err := s.VerifyToken(bad, TypAccessToken, testIssuer); err == nil {
			t.Errorf("garbage %q verified", bad)
		}
	}
}
