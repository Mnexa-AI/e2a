package agent_test

// DB-backed coverage for the sendramp ThrottleError detection branches — the
// seams that map the sender's throttle into the 429 wire error. These drive a
// REAL outbound.Sender whose ramp gate throttles (not a fabricated
// OutboundError), so a regression that breaks errors.As detection — e.g.
// wrapping the sender's error — fails here, not in production as a 500 (direct
// send) or a terminal auto-reject (TTL worker, covered in internal/hitlworker).

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/sendramp"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
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

// countingUsage counts billable outbound recordings so tests can pin WHEN
// usage is metered relative to the send outcome.
type countingUsage struct{ outbound int }

func (c *countingUsage) RecordAndCheck(_ context.Context, _, _, _, direction string) (bool, error) {
	if direction == "outbound" {
		c.outbound++
	}
	return true, nil
}

// setupThrottleAPI wires an *agent.API over a real test DB and a real
// outbound.Sender (fake SMTP upstream) with the given ramp gate installed.
func setupThrottleAPI(t *testing.T, gate outbound.SendingRampGate) (*agent.API, *identity.Store, *pgxpool.Pool, *countingUsage, func() []testutil.SMTPMessage) {
	t.Helper()
	smtpAddr, smtpDone := testutil.FakeSMTPServer(t)
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{Host: smtpAddr.Host, Port: smtpAddr.Port})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	sender.SetSendingRampGate(gate)
	usg := &countingUsage{}
	api := agent.NewAPI(store, sender, smtpRelay, nil, usg,
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	return api, store, pool, usg, smtpDone
}

func throttled(domain string) *sendramp.ThrottleError {
	return &sendramp.ThrottleError{Domain: domain, DailyCap: 50, SentToday: 50, RetryAfter: 5 * time.Hour}
}

// A ramp-throttled direct send surfaces as 429 sending_ramp_limited with the
// pacing details, leaves no message row, and — critically — meters NO billable
// usage: clients are told to retry against the ramp daily, and those refused
// attempts must not burn plan quota.
func TestDeliverOutboundSendingRampThrottled(t *testing.T) {
	gate := &throttleGate{err: throttled("selframp.example.com")}
	api, store, pool, usg, smtpDone := setupThrottleAPI(t, gate)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "ramp")

	_, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"alice@external.example"}, Subject: "hi", Body: "b",
	}, "send", "", nil)
	if oerr == nil {
		t.Fatal("expected a throttle error, send went through")
	}
	if oerr.Status != 429 || oerr.Code != "sending_ramp_limited" {
		t.Fatalf("want 429 sending_ramp_limited, got %d %s (%s)", oerr.Status, oerr.Code, oerr.Msg)
	}
	if oerr.Details["daily_cap"] != 50 || oerr.Details["sent_today"] != 50 || oerr.Details["retry_after_seconds"] != 5*60*60 {
		t.Fatalf("pacing details lost in the mapping: %v", oerr.Details)
	}
	if gate.calls != 1 {
		t.Fatalf("gate consulted %d times, want 1", gate.calls)
	}
	if usg.outbound != 0 {
		t.Fatalf("throttled send must not meter usage, recorded %d", usg.outbound)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM messages WHERE agent_id = $1`, ag.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("throttled send must not persist a message row: rows=%d err=%v", rows, err)
	}
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("throttled send must not reach SMTP, got %d messages", len(msgs))
	}
}

// The successful path meters exactly one billable outbound — recorded AFTER
// the wire send, mirroring the HITL release paths.
func TestDeliverOutboundRecordsUsageAfterSend(t *testing.T) {
	gate := &throttleGate{} // allows
	api, store, _, usg, smtpDone := setupThrottleAPI(t, gate)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "rampok")

	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"alice@external.example"}, Subject: "hi", Body: "b",
	}, "send", "", nil)
	if oerr != nil {
		t.Fatalf("send: %d %s", oerr.Status, oerr.Msg)
	}
	if res.Held {
		t.Fatal("send unexpectedly held")
	}
	if usg.outbound != 1 {
		t.Fatalf("successful send must meter exactly once, recorded %d", usg.outbound)
	}
	if msgs := smtpDone(); len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
}

// A ramp-throttled HITL approve maps to the same 429, and the transaction
// rollback leaves the row pending_review — the approver can retry after the
// reset; the hold is never consumed by a pacing refusal. No usage is metered.
func TestApprovePendingCoreSendingRampThrottled(t *testing.T) {
	gate := &throttleGate{err: throttled("selframpappr.example.com")}
	api, store, pool, usg, smtpDone := setupThrottleAPI(t, gate)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "rampappr")

	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"alice@external.example"}, nil, nil,
		"Held", "body", "", nil, "send", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	_, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, "", agent.ApproveOverrides{})
	if oerr == nil {
		t.Fatal("expected a throttle error, approve went through")
	}
	if oerr.Status != 429 || oerr.Code != "sending_ramp_limited" {
		t.Fatalf("want 429 sending_ramp_limited, got %d %s (%s)", oerr.Status, oerr.Code, oerr.Msg)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id = $1`, msg.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusPendingReview {
		t.Fatalf("throttled approve must leave the row pending_review, got %q", status)
	}
	if usg.outbound != 0 {
		t.Fatalf("throttled approve must not meter usage, recorded %d", usg.outbound)
	}
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("throttled approve must not reach SMTP, got %d messages", len(msgs))
	}
}
