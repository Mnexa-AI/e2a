package approvaltoken_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
)

const testSecret = "test-hmac-secret-for-approvaltoken"

func TestSignVerifyRoundTrip(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	future := time.Now().Add(1 * time.Hour)

	tok, err := s.Sign("msg_abc123", approvaltoken.ActionApprove, future)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if !strings.Contains(tok, ".") {
		t.Errorf("token missing separator: %q", tok)
	}

	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.MessageID != "msg_abc123" {
		t.Errorf("MessageID = %q", claims.MessageID)
	}
	if claims.Action != approvaltoken.ActionApprove {
		t.Errorf("Action = %q", claims.Action)
	}
	// ExpiresAt is stored as unix seconds, so allow 1s rounding.
	if diff := claims.ExpiresAt.Sub(future); diff > time.Second || diff < -time.Second {
		t.Errorf("ExpiresAt mismatch: got %v, want %v", claims.ExpiresAt, future)
	}
}

func TestSignRejectsInvalidAction(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	future := time.Now().Add(1 * time.Hour)
	for _, bad := range []string{"", "maybe", "Approve", "delete"} {
		if _, err := s.Sign("msg_x", bad, future); err == nil {
			t.Errorf("Sign(%q) should error", bad)
		}
	}
}

func TestSignRejectsReservedCharactersInMessageID(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	future := time.Now().Add(1 * time.Hour)
	for _, bad := range []string{"msg|with|pipes", "msg\nwith\nnewlines"} {
		if _, err := s.Sign(bad, approvaltoken.ActionApprove, future); err == nil {
			t.Errorf("Sign should reject reserved characters in %q", bad)
		}
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	future := time.Now().Add(1 * time.Hour)
	tok, _ := s.Sign("msg_x", approvaltoken.ActionApprove, future)

	// Flip the first character of the signature half. Using the first
	// character avoids base64-padding edge cases where the final character
	// encodes only a subset of bits and mutation can be absorbed.
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		t.Fatal("malformed test token")
	}
	first := parts[1][0]
	swap := byte('A')
	if first == 'A' {
		swap = 'B'
	}
	flipped := string(swap) + parts[1][1:]
	tampered := parts[0] + "." + flipped

	if _, err := s.Verify(tampered); !errors.Is(err, approvaltoken.ErrInvalidToken) {
		t.Errorf("tampered signature: err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	future := time.Now().Add(1 * time.Hour)

	// Sign a reject token, then swap the payload for an approve token with
	// the original signature — must fail HMAC check.
	rejTok, _ := s.Sign("msg_x", approvaltoken.ActionReject, future)
	appTok, _ := s.Sign("msg_x", approvaltoken.ActionApprove, future)
	rejSig := strings.SplitN(rejTok, ".", 2)[1]
	appPayload := strings.SplitN(appTok, ".", 2)[0]

	spliced := appPayload + "." + rejSig
	if _, err := s.Verify(spliced); !errors.Is(err, approvaltoken.ErrInvalidToken) {
		t.Errorf("payload/sig splice should fail: err = %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	past := time.Now().Add(-1 * time.Second)
	tok, _ := s.Sign("msg_x", approvaltoken.ActionApprove, past)

	_, err := s.Verify(tok)
	if !errors.Is(err, approvaltoken.ErrTokenExpired) {
		t.Errorf("expired token: err = %v, want ErrTokenExpired", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	issuer := approvaltoken.NewSigner("secret-a")
	verifier := approvaltoken.NewSigner("secret-b")
	future := time.Now().Add(1 * time.Hour)
	tok, _ := issuer.Sign("msg_x", approvaltoken.ActionApprove, future)

	if _, err := verifier.Verify(tok); !errors.Is(err, approvaltoken.ErrInvalidToken) {
		t.Errorf("wrong-secret verify: err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRejectsMalformedTokens(t *testing.T) {
	s := approvaltoken.NewSigner(testSecret)
	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no separator", "just-random-base64-here"},
		{"only separator", "."},
		{"too many fields in payload", encodePayload("a|b|c|d") + ".XXXXX"},
		{"non-numeric exp", encodePayload("msg|approve|notanumber") + ".XXXXX"},
		{"invalid base64 payload", "!!!bad!!!" + "." + "XXXXX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Verify(tc.token); !errors.Is(err, approvaltoken.ErrInvalidToken) {
				t.Errorf("err = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func encodePayload(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
