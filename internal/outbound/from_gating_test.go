package outbound

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// fakeSendingStatus is a SendingStatusLookup returning a fixed status/error.
type fakeSendingStatus struct {
	status string
	err    error
}

func (f fakeSendingStatus) GetSendingStatus(ctx context.Context, domain string) (string, error) {
	return f.status, f.err
}

func verifiedAgent() *identity.AgentIdentity {
	return &identity.AgentIdentity{ID: "bot@acme.com", Domain: "acme.com", DomainVerified: true}
}

// TestEnvelopeSender is the "remove via e2a" invariant: a verified domain's
// Return-Path is the aligned custom MAIL FROM (bounces@bounce.<domain>) so SPF
// aligns and no "via e2a" shows; every non-verified path stays on the e2a relay
// envelope (fail-closed), which is what keeps the "via" rewrite for those.
func TestEnvelopeSender(t *testing.T) {
	if got := envelopeSender(true, "acme.com", "send.e2a.dev"); got != "bounces@bounce.acme.com" {
		t.Errorf("verified envelope = %q, want bounces@bounce.acme.com", got)
	}
	if got := envelopeSender(false, "acme.com", "send.e2a.dev"); got != "agent@send.e2a.dev" {
		t.Errorf("unverified envelope = %q, want agent@send.e2a.dev (relay, fail-closed)", got)
	}
}

// TestUseOwnAddressFrom_FailClosed is the decision-4 invariant: the own-address
// From is used ONLY when the lookup is wired AND reports "verified" for a
// verified custom domain. Every other path falls back to the relay From.
func TestUseOwnAddressFrom_FailClosed(t *testing.T) {
	tests := []struct {
		name   string
		lookup SendingStatusLookup // nil → no setter
		agent  *identity.AgentIdentity
		want   bool
	}{
		{"no lookup wired", nil, verifiedAgent(), false},
		{"status verified", fakeSendingStatus{status: "verified"}, verifiedAgent(), true},
		{"status none", fakeSendingStatus{status: "none"}, verifiedAgent(), false},
		{"status pending", fakeSendingStatus{status: "pending"}, verifiedAgent(), false},
		{"status failed", fakeSendingStatus{status: "failed"}, verifiedAgent(), false},
		{"lookup error", fakeSendingStatus{err: errors.New("db down")}, verifiedAgent(), false},
		{"empty status", fakeSendingStatus{status: ""}, verifiedAgent(), false},
		{
			"domain not inbound-verified",
			fakeSendingStatus{status: "verified"},
			&identity.AgentIdentity{ID: "bot@acme.com", Domain: "acme.com", DomainVerified: false},
			false,
		},
		{
			"empty domain",
			fakeSendingStatus{status: "verified"},
			&identity.AgentIdentity{ID: "bot@acme.com", Domain: "", DomainVerified: true},
			false,
		},
		{"nil agent", fakeSendingStatus{status: "verified"}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSender(nil, "send.e2a.dev")
			if tc.lookup != nil {
				s.SetSendingStatusLookup(tc.lookup)
			}
			if got := s.useOwnAddressFrom(tc.agent); got != tc.want {
				t.Errorf("useOwnAddressFrom = %v, want %v", got, tc.want)
			}
		})
	}
}
