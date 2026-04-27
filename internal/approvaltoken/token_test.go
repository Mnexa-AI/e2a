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

// --- Multi-secret Verify (rotation support) ---

func TestVerify_MultipleSecrets_AcceptsAny(t *testing.T) {
	const oldSecret = "old-secret-during-rotation"
	const newSecret = "new-secret-current"
	exp := time.Now().Add(1 * time.Hour)

	tok, err := approvaltoken.Sign(oldSecret, "msg_rot", approvaltoken.ActionApprove, exp)
	if err != nil {
		t.Fatal(err)
	}

	// Verifier holds both.
	claims, err := approvaltoken.Verify([]string{newSecret, oldSecret}, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.MessageID != "msg_rot" {
		t.Errorf("claims.MessageID = %q", claims.MessageID)
	}

	// Without the matching secret, fails with ErrInvalidToken.
	_, err = approvaltoken.Verify([]string{newSecret, "wrong"}, tok)
	if err != approvaltoken.ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken, got %v", err)
	}
}

func TestVerify_EmptySecrets_Fails(t *testing.T) {
	tok, _ := approvaltoken.Sign("k", "msg_e", approvaltoken.ActionApprove, time.Now().Add(time.Hour))
	if _, err := approvaltoken.Verify(nil, tok); err != approvaltoken.ErrInvalidToken {
		t.Errorf("nil secrets should fail with ErrInvalidToken, got %v", err)
	}
}

func TestPeekMessageID_DoesNotVerify(t *testing.T) {
	// Build a token with a real message_id but a *bogus* signature
	// suffix (just random bytes after the dot). PeekMessageID should
	// still pull the message_id without checking HMAC.
	const secret = "sek"
	tok, _ := approvaltoken.Sign(secret, "msg_peek", approvaltoken.ActionApprove, time.Now().Add(time.Hour))
	// Truncate the signature to invalidate it but keep the payload.
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		t.Fatal("token has no '.'")
	}
	tampered := parts[0] + "." + "garbage"

	id, err := approvaltoken.PeekMessageID(tampered)
	if err != nil {
		t.Fatalf("peek should succeed even with invalid sig: %v", err)
	}
	if id != "msg_peek" {
		t.Errorf("PeekMessageID = %q, want msg_peek", id)
	}

	// But Verify must reject the tampered signature.
	if _, err := approvaltoken.Verify([]string{secret}, tampered); err != approvaltoken.ErrInvalidToken {
		t.Errorf("Verify must reject tampered sig: %v", err)
	}
}

func TestPeekMessageID_MalformedToken(t *testing.T) {
	if _, err := approvaltoken.PeekMessageID(""); err != approvaltoken.ErrInvalidToken {
		t.Errorf("empty token: %v", err)
	}
	if _, err := approvaltoken.PeekMessageID("not-base64.also-not"); err != approvaltoken.ErrInvalidToken {
		t.Errorf("bad b64: %v", err)
	}
}
