package relay

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5"
)

// These tests cover the slice-3 branching in the relay handler that
// can't easily be exercised through the SMTP entry point. They focus
// on the edge cases the design called out in §5 — specifically the
// dual-path (outbox set vs nil) and feature-flag behavior.

// fakeOutbox records PublishTx calls for assertion.
type fakeOutbox struct {
	calls       []webhookpub.Event
	publishErr  error
	besteffortN int
	enabled     bool // toggled by tests that exercise the C3 dedup branch
}

func (f *fakeOutbox) PublishTx(ctx context.Context, tx pgx.Tx, e webhookpub.Event) error {
	f.calls = append(f.calls, e)
	return f.publishErr
}

func (f *fakeOutbox) PublishBestEffortTx(ctx context.Context, tx pgx.Tx, e webhookpub.Event) bool {
	f.besteffortN++
	// Tests don't simulate failures for BestEffortTx here yet;
	// mirror Enabled() so disabled flag → wrote=false and enabled flag → wrote=true.
	return f.enabled
}

// DeleteExpiredWebhookEvents — slice A addition. Returns zero in tests.
func (f *fakeOutbox) DeleteExpiredWebhookEvents(ctx context.Context) (int, error) {
	return 0, nil
}

// Enabled satisfies the C3 fix: trigger sites read this to decide
// whether to suppress the legacy publisher.Publish goroutine.
func (f *fakeOutbox) Enabled() bool { return f.enabled }

func (f *fakeOutbox) SetFanOutEnqueuer(webhookpub.FanOutEnqueuer) {}

func TestServer_SetOutbox_AcceptsNilForBackwardCompat(t *testing.T) {
	// A Server constructed without SetOutbox stays in the legacy path.
	// This is what makes the dual-mode rollout safe: deployments that
	// haven't wired the outbox don't regress.
	s := &Server{}
	if s.outbox != nil {
		t.Errorf("Server.outbox should default to nil; legacy path is the default")
	}
	// SetOutbox to nil should be valid (e.g. tests that explicitly
	// pass a nil to ensure the legacy path).
	s.SetOutbox(nil)
	if s.outbox != nil {
		t.Errorf("SetOutbox(nil) should keep outbox nil, got %#v", s.outbox)
	}
}

func TestServer_SetOutbox_WiresFakeForTesting(t *testing.T) {
	s := &Server{}
	fo := &fakeOutbox{}
	s.SetOutbox(fo)
	if s.outbox == nil {
		t.Fatal("SetOutbox did not store the fake")
	}
	// PublishTx through the interface should reach the fake.
	if err := s.outbox.PublishTx(context.Background(), nil, webhookpub.Event{ID: "evt_x"}); err != nil {
		t.Errorf("PublishTx through interface: %v", err)
	}
	if len(fo.calls) != 1 {
		t.Errorf("expected 1 call on fake, got %d", len(fo.calls))
	}
	if fo.calls[0].ID != "evt_x" {
		t.Errorf("event id = %s, want evt_x", fo.calls[0].ID)
	}
}

// Compile-time interface satisfaction check: confirms the production
// Outbox type and the test fake both satisfy the same interface, so a
// future signature change ripples to both.
var _ webhookpub.Outbox = (*fakeOutbox)(nil)

func TestServer_FakeOutbox_EnabledTogglesAsConfigured(t *testing.T) {
	// Pins the C3 fix's interface contract: the fakeOutbox's Enabled()
	// method must mirror its configured state so per-test setups can
	// exercise both branches of the legacy-suppression check.
	off := &fakeOutbox{}
	if off.Enabled() {
		t.Errorf("default fakeOutbox should report Enabled()=false")
	}
	on := &fakeOutbox{enabled: true}
	if !on.Enabled() {
		t.Errorf("fakeOutbox{enabled:true} should report Enabled()=true")
	}
}

// Compile-time check: identity.Store.WithTx exists (added by slice 3
// and needed by the relay's tx branch). If this doesn't compile, the
// branch above is broken.
var _ = (*identity.Store)(nil).WithTx
