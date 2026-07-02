package outbound

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// fakeWarmupGate records reservations and returns a fixed error.
type fakeWarmupGate struct {
	calls   int
	domains []string
	err     error
}

func (g *fakeWarmupGate) Reserve(_ context.Context, domain string) error {
	g.calls++
	g.domains = append(g.domains, domain)
	return g.err
}

// A throttling warmup gate must stop Send BEFORE the relay is touched, and the
// gate error must propagate unwrapped so callers can branch on
// *warmup.ThrottleError. The gate is keyed on the agent's own domain.
func TestSendWarmupGateThrottles(t *testing.T) {
	boom := errors.New("throttled sentinel")
	gate := &fakeWarmupGate{err: boom}
	// nil relay: if Send got past the gate it would nil-panic — reaching the
	// error return proves the gate fires before the wire.
	s := NewSender(nil, "relay.e2a.dev")
	s.SetWarmupGate(gate)

	agent := &identity.AgentIdentity{ID: "a1", Email: "support@acme.com", Domain: "acme.com"}
	_, err := s.Send(agent, SendRequest{To: []string{"x@example.com"}, Subject: "hi", Body: "b"})
	if !errors.Is(err, boom) {
		t.Fatalf("gate error must propagate unwrapped, got %v", err)
	}
	if gate.calls != 1 || gate.domains[0] != "acme.com" {
		t.Fatalf("gate must be consulted once with the agent's domain, got %+v", gate)
	}
}

// Validation failures must NOT consume a warmup slot — the reservation happens
// only once the message is actually about to hit the wire.
func TestSendWarmupGateNotConsultedOnValidationError(t *testing.T) {
	gate := &fakeWarmupGate{}
	s := NewSender(nil, "relay.e2a.dev")
	s.SetWarmupGate(gate)

	agent := &identity.AgentIdentity{ID: "a1", Email: "support@acme.com", Domain: "acme.com"}
	_, err := s.Send(agent, SendRequest{To: []string{"not-an-email"}, Subject: "hi", Body: "b"})
	if !IsValidationError(err) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if gate.calls != 0 {
		t.Fatalf("validation failure must not reserve a warmup slot, gate saw %d calls", gate.calls)
	}
}
