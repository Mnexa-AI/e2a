package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

func assertReviewEventLifecycleMatchesRow(t *testing.T, pool *pgxpool.Pool, messageID, eventType string, wantReason messagelifecycle.ReasonCode) {
	t.Helper()
	var eventRaw, rowRaw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT envelope->'data'->'lifecycle_transitions' FROM webhook_events WHERE message_id=$1 AND type=$2`,
		messageID, eventType).Scan(&eventRaw); err != nil {
		t.Fatalf("read event lifecycle: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT jsonb_build_array(to_jsonb(t) - 'dedupe_key') FROM message_lifecycle_transitions t WHERE message_id=$1 AND reason_code=$2`,
		messageID, wantReason).Scan(&rowRaw); err != nil {
		t.Fatalf("read persisted lifecycle: %v", err)
	}
	var eventTransitions, rowTransitions []messagelifecycle.MessageLifecycleTransition
	if err := json.Unmarshal(eventRaw, &eventTransitions); err != nil {
		t.Fatalf("decode event lifecycle: %v", err)
	}
	if err := json.Unmarshal(rowRaw, &rowTransitions); err != nil {
		t.Fatalf("decode persisted lifecycle: %v", err)
	}
	if len(eventTransitions) != 1 || len(rowTransitions) != 1 || eventTransitions[0].ID != rowTransitions[0].ID || eventTransitions[0].ReasonCode != wantReason {
		t.Fatalf("event lifecycle = %+v, persisted = %+v, want exact %s transition", eventTransitions, rowTransitions, wantReason)
	}
}

// waitForEvent asserts an event of the given type for the user landed in the
// durable outbox (webhook_events). The approve/reject core writes the event in
// the same tx via the outbox, so it is durable by the time the call returns; the
// short poll is belt-and-suspenders.
func waitForEvent(t *testing.T, pool *pgxpool.Pool, userID, eventType string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM webhook_events WHERE user_id = $1 AND type = $2`,
			userID, eventType).Scan(&n); err == nil && n >= 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s event in webhook_events", eventType)
}

func newReviewAPI(t *testing.T) (*agent.API, *identity.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	// Events flow through the durable outbox (webhook_events) — River is the sole
	// delivery engine and there is no legacy publisher. Wire the real outbox so the
	// approve/reject core's publish path fires and the assertions can read the row.
	api.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	return api, store, pool
}

func seedHeldInbound(t *testing.T, store *identity.Store, ctx context.Context, domain string) (userID, agentID, messageID string) {
	t.Helper()
	user, err := store.CreateOrGetUser(ctx, "o@"+domain, "O", "g-"+domain)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	m, err := store.CreateInboundMessage(ctx, "", ag.ID, "evil@x.com", ag.ID, "", "held", "", "unread",
		[]byte("Subject: held\r\n\r\nx"), nil, nil, false, "", []string{ag.ID}, nil, nil,
		identity.InboundScreening{
			Status: identity.MessageStatusPendingReview, ScanAction: "review",
			ReviewReason: identity.ReviewReasonInboundScan,
		})
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	return user.ID, ag.ID, m.ID
}

// TestApproveInboundReviewCore_ReleasesAndPublishes is the end-to-end core check:
// the held message transitions to review_approved AND email.review_approved is
// actually emitted (wired through publishApproved → the durable outbox).
func TestApproveInboundReviewCore_ReleasesAndPublishes(t *testing.T) {
	api, store, pool := newReviewAPI(t)
	ctx := context.Background()
	userID, agentID, msgID := seedHeldInbound(t, store, ctx, "approvecore.example.com")
	meta := &identity.ReviewMessageMeta{ID: msgID, AgentID: agentID, Direction: "inbound", Sender: "evil@x.com", Subject: "held", Type: "received"}

	if derr := api.ApproveInboundReviewCore(ctx, userID, meta); derr != nil {
		t.Fatalf("ApproveInboundReviewCore: %+v", derr)
	}
	if got := statusOf(t, pool, ctx, msgID); got != identity.MessageStatusReviewApproved {
		t.Errorf("status = %q, want review_approved", got)
	}
	waitForEvent(t, pool, userID, webhookpub.EventEmailReviewApproved)
	assertReviewEventLifecycleMatchesRow(t, pool, msgID, webhookpub.EventEmailReviewApproved, messagelifecycle.ReasonReviewApproved)

	// A second approve (already resolved) is a clean 409, not a double release.
	if derr := api.ApproveInboundReviewCore(ctx, userID, meta); derr == nil || derr.Status != http.StatusConflict || derr.Code != "message_not_pending" {
		t.Errorf("second approve = %+v, want 409 message_not_pending", derr)
	}
}

// TestRejectInboundReviewCore_DropsAndPublishes mirrors the approve core for reject.
func TestRejectInboundReviewCore_DropsAndPublishes(t *testing.T) {
	api, store, pool := newReviewAPI(t)
	ctx := context.Background()
	userID, agentID, msgID := seedHeldInbound(t, store, ctx, "rejectcore.example.com")
	meta := &identity.ReviewMessageMeta{ID: msgID, AgentID: agentID, Direction: "inbound", Type: "received"}

	if derr := api.RejectInboundReviewCore(ctx, userID, "prompt injection", meta); derr != nil {
		t.Fatalf("RejectInboundReviewCore: %+v", derr)
	}
	var st, reason string
	if err := pool.QueryRow(ctx, `SELECT status, COALESCE(rejection_reason,'') FROM messages WHERE id=$1`, msgID).Scan(&st, &reason); err != nil {
		t.Fatalf("read: %v", err)
	}
	if st != identity.MessageStatusReviewRejected || reason != "prompt injection" {
		t.Errorf("row = status %q reason %q, want review_rejected/prompt injection", st, reason)
	}
	waitForEvent(t, pool, userID, webhookpub.EventEmailReviewRejected)
	assertReviewEventLifecycleMatchesRow(t, pool, msgID, webhookpub.EventEmailReviewRejected, messagelifecycle.ReasonReviewRejected)
}

func statusOf(t *testing.T, pool *pgxpool.Pool, ctx context.Context, msgID string) string {
	t.Helper()
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, msgID).Scan(&st); err != nil {
		t.Fatalf("status lookup: %v", err)
	}
	return st
}
