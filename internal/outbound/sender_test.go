package outbound

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/dkim"
)

func TestNormalizeAddrs(t *testing.T) {
	got, err := normalizeAddrs([]string{"Alice@Gmail.COM", " bob@test.com ", ""})
	if err != nil {
		t.Fatalf("normalizeAddrs: %v", err)
	}
	want := []string{"alice@gmail.com", "bob@test.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeAddrsDisplayName(t *testing.T) {
	got, err := normalizeAddrs([]string{"Alice <alice@GMAIL.com>"})
	if err != nil {
		t.Fatalf("normalizeAddrs: %v", err)
	}
	want := []string{"alice@gmail.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeAddrsInvalid(t *testing.T) {
	_, err := normalizeAddrs([]string{"not-an-email"})
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

func TestDedupe(t *testing.T) {
	got := dedupe([]string{"a@b.com", "c@d.com", "A@B.com", "c@d.com"})
	want := []string{"a@b.com", "c@d.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRemoveAddrs(t *testing.T) {
	got := removeAddrs([]string{"a@b.com", "c@d.com", "e@f.com"}, []string{"c@d.com"})
	want := []string{"a@b.com", "e@f.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCrossFieldDedupe(t *testing.T) {
	// Simulate To > CC > BCC priority
	to := []string{"alice@test.com", "bob@test.com"}
	cc := []string{"bob@test.com", "carol@test.com"}
	bcc := []string{"carol@test.com", "dave@test.com"}

	cc = removeAddrs(cc, to)
	bcc = removeAddrs(bcc, to)
	bcc = removeAddrs(bcc, cc)

	wantCC := []string{"carol@test.com"}
	wantBCC := []string{"dave@test.com"}

	if !reflect.DeepEqual(cc, wantCC) {
		t.Errorf("cc = %v, want %v", cc, wantCC)
	}
	if !reflect.DeepEqual(bcc, wantBCC) {
		t.Errorf("bcc = %v, want %v", bcc, wantBCC)
	}
}

// --- DKIM signing path (Sender.signMessage) ---
//
// The dkim package's unit tests cover keygen, sign/verify roundtrip,
// and TXT extraction. These tests exercise the wiring in
// Sender.signMessage — the four branches that decide whether a
// message gets signed, returns unsigned, or fails open with a log.
//
// The contract is "fail open": ANY problem fetching or applying the
// key must return (nil, false) so the caller proceeds with the
// unsigned message. A bug here that returns an error or panics would
// break outbound mail for every customer with a custom domain.

// fakeDKIMLookup is a test double for DKIMKeyLookup. The function
// fields let each test pick its own response shape without needing
// per-test struct definitions.
type fakeDKIMLookup struct {
	get func(ctx context.Context, domain string) (string, []byte, error)
}

func (f *fakeDKIMLookup) GetDKIMKey(ctx context.Context, domain string) (string, []byte, error) {
	return f.get(ctx, domain)
}

// validTestMessage is an RFC 5322 message stub that the DKIM signer
// will accept. From / Date / Message-ID headers are present (the
// minimum set our signer covers). Body is short so signature
// canonicalization is cheap to inspect when debugging a failure.
const validTestMessage = "From: bot@example.com\r\n" +
	"To: alice@elsewhere.test\r\n" +
	"Subject: hi\r\n" +
	"Date: Fri, 22 May 2026 12:00:00 +0000\r\n" +
	"Message-ID: <abc@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"hello\r\n"

func TestSignMessage_NoLookupConfigured(t *testing.T) {
	// Plain NewSender → dkimLookup is nil. Must return (nil, false)
	// without touching the message — older deployments and unit
	// tests construct Senders this way and shouldn't break.
	s := NewSender(nil, "example.com")
	signed, ok := s.signMessage([]byte(validTestMessage), "example.com")
	if ok {
		t.Errorf("ok = true, want false (no lookup configured)")
	}
	if signed != nil {
		t.Errorf("signed = %d bytes, want nil", len(signed))
	}
}

func TestSignMessage_EmptyDomainSkipsLookup(t *testing.T) {
	// A pathological caller passing "" as the signing domain must
	// short-circuit rather than calling GetDKIMKey(ctx, ""), which
	// would scan all rows. Defensive guard at the entry point.
	calls := 0
	lookup := &fakeDKIMLookup{
		get: func(_ context.Context, _ string) (string, []byte, error) {
			calls++
			return "sel", []byte("not-a-real-key"), nil
		},
	}
	s := NewSenderWithDKIM(nil, "example.com", lookup)
	_, ok := s.signMessage([]byte(validTestMessage), "")
	if ok {
		t.Errorf("ok = true, want false (empty domain)")
	}
	if calls != 0 {
		t.Errorf("GetDKIMKey called %d times for empty domain, want 0", calls)
	}
}

func TestSignMessage_LookupErrorFailsOpen(t *testing.T) {
	// DB transient error → log + proceed unsigned. The caller treats
	// (nil, false) as "use the original message," so outbound mail
	// keeps flowing while alerting kicks in on the log line.
	lookup := &fakeDKIMLookup{
		get: func(_ context.Context, _ string) (string, []byte, error) {
			return "", nil, errors.New("connection refused")
		},
	}
	s := NewSenderWithDKIM(nil, "example.com", lookup)
	signed, ok := s.signMessage([]byte(validTestMessage), "example.com")
	if ok {
		t.Errorf("ok = true, want false (lookup errored)")
	}
	if signed != nil {
		t.Errorf("signed != nil on lookup error; signMessage must not return half-applied data")
	}
}

func TestSignMessage_NoKeyStoredReturnsUnsigned(t *testing.T) {
	// Pre-migration domain: row exists but DKIM columns are NULL.
	// Store returns ("", nil, nil) — distinct from an error, distinct
	// from a successful lookup. signMessage must treat it the same
	// way it treats the no-lookup case: silently fall through.
	lookup := &fakeDKIMLookup{
		get: func(_ context.Context, _ string) (string, []byte, error) {
			return "", nil, nil
		},
	}
	s := NewSenderWithDKIM(nil, "example.com", lookup)
	signed, ok := s.signMessage([]byte(validTestMessage), "example.com")
	if ok {
		t.Errorf("ok = true, want false (no keypair stored)")
	}
	if signed != nil {
		t.Errorf("signed != nil on missing keypair")
	}
}

func TestSignMessage_SignErrorFailsOpen(t *testing.T) {
	// Store returned a selector + bytes, but the bytes aren't a
	// valid PKCS#1 RSA private key (corruption, partial-write, etc).
	// dkim.Sign returns an error; we log and proceed unsigned. A
	// panic here would crash the outbound goroutine for every send.
	lookup := &fakeDKIMLookup{
		get: func(_ context.Context, _ string) (string, []byte, error) {
			return "e2a202605", []byte("definitely-not-a-key"), nil
		},
	}
	s := NewSenderWithDKIM(nil, "example.com", lookup)
	signed, ok := s.signMessage([]byte(validTestMessage), "example.com")
	if ok {
		t.Errorf("ok = true, want false (sign should fail on garbage key)")
	}
	if signed != nil {
		t.Errorf("signed != nil on sign error")
	}
}

func TestSignMessage_HappyPathPrependsSignatureHeader(t *testing.T) {
	// Generate a real keypair, hand it to the fake lookup, sign the
	// validTestMessage. signMessage must return (signed, true) and
	// the signed bytes must start with "DKIM-Signature:" — the
	// header dkim.Sign prepends per RFC 6376 §3.5.
	kp, err := dkim.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	lookup := &fakeDKIMLookup{
		get: func(_ context.Context, _ string) (string, []byte, error) {
			return kp.Selector, kp.PrivateKeyDER, nil
		},
	}
	s := NewSenderWithDKIM(nil, "example.com", lookup)
	signed, ok := s.signMessage([]byte(validTestMessage), "example.com")
	if !ok {
		t.Fatalf("ok = false, want true on happy path")
	}
	if !bytes.HasPrefix(signed, []byte("DKIM-Signature:")) {
		head := signed
		if len(head) > 80 {
			head = head[:80]
		}
		t.Errorf("signed bytes must begin with DKIM-Signature header; first 80 bytes:\n%s", head)
	}
	// Defense-in-depth: signed message must still contain the
	// original headers + body — the signer prepends, doesn't
	// rewrite.
	if !bytes.Contains(signed, []byte("From: bot@example.com")) {
		t.Errorf("signed bytes lost the original From header")
	}
	if !bytes.Contains(signed, []byte("hello")) {
		t.Errorf("signed bytes lost the original body")
	}
}
