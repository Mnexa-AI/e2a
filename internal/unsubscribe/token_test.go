package unsubscribe

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestDeriveIsDeterministicForNormalizedScope(t *testing.T) {
	token, err := Derive("secret", " user_123 ", " Otto@Example.COM ", " Person@Example.COM ")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	normalized, err := Derive("secret", "user_123", "otto@example.com", "person@example.com")
	if err != nil {
		t.Fatalf("Derive normalized: %v", err)
	}
	if token != normalized {
		t.Fatalf("normalized scope produced different tokens: %q != %q", token, normalized)
	}
	if again, err := Derive("secret", "user_123", "otto@example.com", "person@example.com"); err != nil || again != token {
		t.Fatalf("same scope was not deterministic: token=%q err=%v", again, err)
	}
}

func TestDeriveKnownAnswer(t *testing.T) {
	// This vector pins HMAC-SHA256 with the first length-prefixed field
	// "e2a:unsubscribe:u1", followed by normalized user, agent, and recipient
	// fields. Each field length is an unsigned 64-bit big-endian byte count.
	const want = "u1_PUUbtIxrGtvnQCcJ-0P-l35CMmAcqURYPS6hEF_ktvQ"

	got, err := Derive(
		"known-secret",
		" user_123 ",
		" Otto@Example.COM ",
		" Person@Example.COM ",
	)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if got != want {
		t.Fatalf("Derive known-answer token = %q, want %q", got, want)
	}
}

func TestDeriveSeparatesEveryInput(t *testing.T) {
	base, err := Derive("secret", "user_123", "otto@example.com", "person@example.com")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	tests := []struct {
		name      string
		secret    string
		userID    string
		agentID   string
		recipient string
	}{
		{name: "secret", secret: "other-secret", userID: "user_123", agentID: "otto@example.com", recipient: "person@example.com"},
		{name: "user ID", secret: "secret", userID: "user_456", agentID: "otto@example.com", recipient: "person@example.com"},
		{name: "agent ID", secret: "secret", userID: "user_123", agentID: "other@example.com", recipient: "person@example.com"},
		{name: "recipient", secret: "secret", userID: "user_123", agentID: "otto@example.com", recipient: "other@example.com"},
		// These two tuples collide under naive concatenation. Length framing
		// must keep them distinct.
		{name: "field boundary", secret: "secret", userID: "user_123otto", agentID: "@example.com", recipient: "person@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Derive(tt.secret, tt.userID, tt.agentID, tt.recipient)
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if got == base {
				t.Fatalf("changing %s did not change token", tt.name)
			}
		})
	}
}

func TestDeriveReturnsVersioned256BitBase64URLToken(t *testing.T) {
	token, err := Derive("secret", "user_123", "otto@example.com", "person@example.com")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !strings.HasPrefix(token, "u1_") {
		t.Fatalf("token %q does not have u1_ prefix", token)
	}
	encoded := strings.TrimPrefix(token, "u1_")
	if strings.Contains(encoded, "=") {
		t.Fatalf("token digest must use unpadded base64url: %q", encoded)
	}
	digest, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode token digest: %v", err)
	}
	if len(digest) != sha256.Size {
		t.Fatalf("decoded token digest length = %d, want %d", len(digest), sha256.Size)
	}
}

func TestHashReturnsSHA256LookupKey(t *testing.T) {
	token := "u1_example-token"
	want := sha256.Sum256([]byte(token))
	got := Hash(token)
	if !bytes.Equal(got, want[:]) {
		t.Fatalf("Hash(%q) = %x, want %x", token, got, want)
	}
}

func TestDeriveRejectsEmptyRequiredInputs(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		userID    string
		agentID   string
		recipient string
	}{
		{name: "secret", userID: "user_123", agentID: "otto@example.com", recipient: "person@example.com"},
		{name: "user ID", secret: "secret", userID: " \t\n", agentID: "otto@example.com", recipient: "person@example.com"},
		{name: "agent ID", secret: "secret", userID: "user_123", agentID: " \t\n", recipient: "person@example.com"},
		{name: "recipient", secret: "secret", userID: "user_123", agentID: "otto@example.com", recipient: " \t\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Derive(tt.secret, tt.userID, tt.agentID, tt.recipient); err == nil {
				t.Fatalf("Derive accepted empty %s", tt.name)
			}
		})
	}
}
