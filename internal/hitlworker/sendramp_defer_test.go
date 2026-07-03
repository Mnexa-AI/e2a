package hitlworker_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/hitlworker"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/sendramp"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// throttleGate satisfies outbound.SendingRampGate with a fixed verdict.
type throttleGate struct {
	calls int
	err   error
}

func (g *throttleGate) Reserve(context.Context, string) error {
	g.calls++
	return g.err
}

// A ramp-throttled auto-approve is a pacing condition, not a send failure: the
// worker must NOT auto-reject (that would terminally drop an operator-approved
// message on a busy ramp day), and it must push the row's approval_expires_at
// to the counter reset so the throttled row rotates to the BACK of the
// ListExpiredPending sweep. Without the bump the earliest-expired throttled
// rows head-of-line block every sweep — re-running the full approve
// transaction per row per poll and, past batchSize rows, starving every other
// tenant's expirations until UTC midnight. This is also the regression guard
// on the worker's errors.As throttle detection: if a refactor ever wraps the
// sender's error, this test fails instead of prod silently auto-rejecting.
func TestWorkerAutoApproveSendingRampThrottleDefers(t *testing.T) {
	smtpAddr, smtpDone := testutil.FakeSMTPServer(t)
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{Host: smtpAddr.Host, Port: smtpAddr.Port})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	retryAfter := 7 * time.Hour
	gate := &throttleGate{err: &sendramp.ThrottleError{
		Domain: "ramp-defer.example.com", DailyCap: 50, SentToday: 50, RetryAfter: retryAfter,
	}}
	sender.SetSendingRampGate(gate)
	w := hitlworker.New(store, sender, usage.NewNoopUsageTracker(), "test.e2a.dev")
	ctx := context.Background()

	agent := prepareAgent(t, store, "ramp-defer", identity.HITLExpirationApprove)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Throttled auto-send", "body", "", nil,
		"send", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)

	before := time.Now()
	w.RunOnce(ctx)

	if gate.calls != 1 {
		t.Fatalf("gate consulted %d times, want 1", gate.calls)
	}
	var status string
	var expiresAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status, approval_expires_at FROM messages WHERE id = $1`, msg.ID,
	).Scan(&status, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusPendingReview {
		t.Fatalf("throttled auto-approve must leave the row pending_review, got %q", status)
	}
	// Deferred to the counter reset: expiry moved from 5 minutes in the past to
	// ~retryAfter in the future (window tolerant of test runtime).
	if expiresAt.Before(before.Add(retryAfter - time.Minute)) {
		t.Fatalf("approval_expires_at not deferred: %v (want ≈ now+%v)", expiresAt, retryAfter)
	}
	if expiresAt.After(before.Add(retryAfter + time.Minute)) {
		t.Fatalf("approval_expires_at deferred too far: %v (want ≈ now+%v)", expiresAt, retryAfter)
	}

	// The deferred row is out of the sweep's working set: another sweep must
	// not re-run the approve transaction against a spent cap.
	w.RunOnce(ctx)
	if gate.calls != 1 {
		t.Fatalf("deferred row re-swept: gate consulted %d times, want still 1", gate.calls)
	}

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("throttled auto-approve must not reach SMTP, got %d messages", len(msgs))
	}
}
