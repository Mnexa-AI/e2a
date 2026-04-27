package headers

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
	})

	if h[HeaderVerified] != "true" {
		t.Errorf("expected Verified=true, got %s", h[HeaderVerified])
	}
	if h[HeaderSender] != "alice@example.com" {
		t.Errorf("expected Sender=alice@example.com, got %s", h[HeaderSender])
	}
	if h[HeaderSignature] == "" {
		t.Error("expected non-empty signature")
	}
	if !s.Verify(h) {
		t.Error("expected Verify to return true")
	}
}

func TestSignVerifyWithDelegation(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
		AgentID:    "agent-123",
		HumanID:    "human-456",
	})

	if h[HeaderDelegation] != "agent=agent-123;human=human-456" {
		t.Errorf("unexpected delegation: %s", h[HeaderDelegation])
	}
	if !s.Verify(h) {
		t.Error("expected Verify to return true with delegation")
	}
}

func TestVerifyRejectsExpiredTimestamp(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
	})

	// Overwrite timestamp to 10 minutes ago
	h[HeaderTimestamp] = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)

	if s.Verify(h) {
		t.Error("expected Verify to reject expired timestamp")
	}
}

func TestVerifyRejectsFutureTimestamp(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
	})

	// Overwrite timestamp to 2 minutes in the future (beyond 30s grace)
	h[HeaderTimestamp] = time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339)

	if s.Verify(h) {
		t.Error("expected Verify to reject future timestamp")
	}
}

func TestVerifyRejectsTamperedHeader(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
	})

	h[HeaderSender] = "eve@evil.com"

	if s.Verify(h) {
		t.Error("expected Verify to reject tampered sender")
	}
}

func TestVerifyRejectsMissingSignature(t *testing.T) {
	s := NewSigner("test-secret")
	h := AuthHeaders{
		HeaderVerified:   "true",
		HeaderSender:     "alice@example.com",
		HeaderEntityType: "human",
		HeaderTimestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if s.Verify(h) {
		t.Error("expected Verify to reject missing signature")
	}
}

func TestVerifyWithMaxAge(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
	})

	// Should pass with generous max age
	if !s.VerifyWithMaxAge(h, 1*time.Hour) {
		t.Error("expected VerifyWithMaxAge to pass with 1h window")
	}

	// Should fail with very tight max age after a small delay
	time.Sleep(5 * time.Millisecond)
	if s.VerifyWithMaxAge(h, 1*time.Millisecond) {
		t.Error("expected VerifyWithMaxAge to reject with 1ms window")
	}
}

func TestSignVerifyWithDomainCheck(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:    true,
		Sender:      "alice@example.com",
		EntityType:  "human",
		DomainCheck: "spf=pass; dkim=none",
	})

	if h[HeaderDomainCheck] != "spf=pass; dkim=none" {
		t.Errorf("DomainCheck = %q, want %q", h[HeaderDomainCheck], "spf=pass; dkim=none")
	}
	if !s.Verify(h) {
		t.Error("expected Verify to return true with DomainCheck")
	}

	// Tampering with domain check should break signature
	h[HeaderDomainCheck] = "spf=fail; dkim=fail"
	if s.Verify(h) {
		t.Error("expected Verify to reject tampered DomainCheck")
	}
}

// TestMessageIDIsBound ensures auth headers cannot be lifted from one
// message and reused on another within the replay window. Without
// MessageID in the canonical, an attacker who captured a valid (sender,
// MAC) pair could attach it to any payload — defeating the integrity
// guarantee the headers are supposed to provide.
func TestMessageIDIsBound(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
		MessageID:  "msg_abc123",
		BodyHash:   HashBody([]byte("hello")),
	})

	if h[HeaderMessageID] != "msg_abc123" {
		t.Errorf("HeaderMessageID = %q, want msg_abc123", h[HeaderMessageID])
	}
	if !s.Verify(h) {
		t.Error("expected Verify to pass with bound MessageID")
	}

	// Substituting the message ID without recomputing the MAC must fail.
	h[HeaderMessageID] = "msg_attacker"
	if s.Verify(h) {
		t.Error("Verify accepted a tampered MessageID — auth headers can be lifted")
	}
}

// TestBodyHashIsBound ensures auth headers cannot be replayed under a
// modified message body. Without BodyHash in the canonical, an attacker
// who captured a valid (headers, MAC) pair could attach the same auth
// claim to a body they've rewritten — keeping verified=true while
// changing the message content.
func TestBodyHashIsBound(t *testing.T) {
	s := NewSigner("test-secret")
	body := []byte("Subject: hi\r\n\r\nhello bob")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
		MessageID:  "msg_abc123",
		BodyHash:   HashBody(body),
	})

	if h[HeaderBodyHash] != HashBody(body) {
		t.Errorf("HeaderBodyHash mismatch")
	}
	if !s.Verify(h) {
		t.Error("expected Verify to pass for matching body")
	}

	// Substituting the body hash without recomputing the MAC must fail.
	h[HeaderBodyHash] = HashBody([]byte("forged body"))
	if s.Verify(h) {
		t.Error("Verify accepted a tampered BodyHash — body integrity is not bound")
	}
}

func TestVerifyRejectsMissingBodyHash(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
		MessageID:  "msg_abc123",
		BodyHash:   HashBody([]byte("body")),
	})
	delete(h, HeaderBodyHash)
	if s.Verify(h) {
		t.Error("Verify accepted headers with missing BodyHash")
	}
}

func TestVerifyRejectsMissingMessageID(t *testing.T) {
	s := NewSigner("test-secret")
	h := s.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
		MessageID:  "msg_abc123",
	})
	delete(h, HeaderMessageID)
	if s.Verify(h) {
		t.Error("Verify accepted headers with missing MessageID")
	}
}

func TestDifferentSecretRejects(t *testing.T) {
	s1 := NewSigner("secret-A")
	s2 := NewSigner("secret-B")

	h := s1.Sign(AuthPayload{
		Verified:   true,
		Sender:     "alice@example.com",
		EntityType: "human",
	})

	if s2.Verify(h) {
		t.Error("expected Verify with different secret to return false")
	}
}

// --- Multi-secret Verify (rotation support) ---

func TestVerify_AcceptsAnyOfMultipleSecrets(t *testing.T) {
	const oldSecret = "old-secret-during-rotation"
	const newSecret = "new-secret-current"

	// Sign with the old secret (simulates a webhook in flight from
	// before a rotation).
	signed := Sign(oldSecret, AuthPayload{
		Verified:    true,
		Sender:      "alice@example.com",
		EntityType:  "human",
		DomainCheck: "spf=pass",
		MessageID:   "msg_test_rotation",
		BodyHash:    "deadbeef",
	})

	// Recipient holds both old + new during the rotation window.
	if !Verify([]string{newSecret, oldSecret}, signed) {
		t.Error("verify should succeed when ONE of the secrets matches")
	}

	// Order shouldn't matter.
	if !Verify([]string{oldSecret, newSecret}, signed) {
		t.Error("verify should succeed regardless of secret order")
	}

	// Without the matching secret, verify fails.
	if Verify([]string{newSecret, "another-wrong-secret"}, signed) {
		t.Error("verify should fail when no secret matches")
	}
}

func TestVerify_EmptySecretsList_Fails(t *testing.T) {
	signed := Sign("k", AuthPayload{
		Sender: "a@b.c", MessageID: "msg_x", BodyHash: "z",
	})
	if Verify(nil, signed) {
		t.Error("verify with no secrets must fail")
	}
	if Verify([]string{}, signed) {
		t.Error("verify with empty secrets slice must fail")
	}
}

func TestSign_ParametricMatchesSignerWrapper(t *testing.T) {
	// The legacy Signer wrapper should produce identical output to the
	// new free Sign function for any given secret. (Timestamp is the
	// only non-deterministic field; we check everything else.)
	const secret = "consistency-test"
	payload := AuthPayload{
		Verified:    true,
		Sender:      "consistency@test.example",
		EntityType:  "human",
		DomainCheck: "spf=pass; dkim=pass",
		MessageID:   "msg_consistency",
		BodyHash:    "abc123",
	}
	free := Sign(secret, payload)
	wrapped := NewSigner(secret).Sign(payload)

	for _, k := range []string{HeaderVerified, HeaderSender, HeaderEntityType, HeaderDomainCheck, HeaderMessageID, HeaderBodyHash} {
		if free[k] != wrapped[k] {
			t.Errorf("%s differs: free=%q wrapped=%q", k, free[k], wrapped[k])
		}
	}
}
