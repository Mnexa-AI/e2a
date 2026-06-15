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
