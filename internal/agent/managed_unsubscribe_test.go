package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/outbound"
)

type fakeUnsubscribeIssuer struct {
	calls     int
	recipient string
	err       error
}

func (f *fakeUnsubscribeIssuer) Ready() error { return nil }

func (f *fakeUnsubscribeIssuer) Issue(_ context.Context, _, _, recipient string) (string, error) {
	f.calls++
	f.recipient = recipient
	if f.err != nil {
		return "", f.err
	}
	return "https://api.example/u/u1_token", nil
}

func TestPrepareManagedUnsubscribeDefersMintAndBindsCanonicalRecipient(t *testing.T) {
	ag := &identity.AgentIdentity{ID: "bot@example.com"}
	issuer := &fakeUnsubscribeIssuer{}
	req := outbound.SendRequest{To: []string{"Person <USER@Example.net>", "user@example.net"}, Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"}}
	if oerr := prepareManagedUnsubscribe(context.Background(), issuer, "send.e2a.dev", "u1", ag, &req, false); oerr != nil {
		t.Fatal(oerr)
	}
	if issuer.calls != 0 || req.Unsubscribe.URL != "" {
		t.Fatalf("held validation minted token: calls=%d url=%q", issuer.calls, req.Unsubscribe.URL)
	}
	if oerr := prepareManagedUnsubscribe(context.Background(), issuer, "send.e2a.dev", "u1", ag, &req, true); oerr != nil {
		t.Fatal(oerr)
	}
	if issuer.calls != 1 || issuer.recipient != "user@example.net" || req.Unsubscribe.URL == "" {
		t.Fatalf("issuer=%+v req=%+v", issuer, req.Unsubscribe)
	}
}

func TestPrepareManagedUnsubscribeFailsClosed(t *testing.T) {
	ag := &identity.AgentIdentity{ID: "bot@example.com"}
	for name, req := range map[string]outbound.SendRequest{
		"zero": {To: []string{"bot@example.com"}, Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"}},
		"many": {To: []string{"a@example.net"}, BCC: []string{"b@example.net"}, Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"}},
	} {
		if got := prepareManagedUnsubscribe(context.Background(), &fakeUnsubscribeIssuer{}, "send.e2a.dev", "u", ag, &req, false); got == nil || got.Status != 400 {
			t.Errorf("%s got=%v", name, got)
		}
	}
	req := outbound.SendRequest{To: []string{"a@example.net"}, Unsubscribe: &outbound.UnsubscribeOptions{Mode: "managed"}}
	got := prepareManagedUnsubscribe(context.Background(), &fakeUnsubscribeIssuer{err: errors.New("db down")}, "send.e2a.dev", "u", ag, &req, true)
	if got == nil || got.Status != 500 {
		t.Fatalf("issuer failure=%v", got)
	}
}
