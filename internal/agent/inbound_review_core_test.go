package agent_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/jackc/pgx/v5/pgxpool"
)

// capturePublisher records published events for assertions.
type capturePublisher struct {
	mu     sync.Mutex
	events []webhookpub.Event
}

func (c *capturePublisher) Publish(_ context.Context, e webhookpub.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *capturePublisher) waitType(t *testing.T, typ string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, e := range c.events {
			if e.Type == typ {
				c.mu.Unlock()
				return
			}
		}
		c.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s event", typ)
}

func newReviewAPI(t *testing.T) (*agent.API, *identity.Store, *pgxpool.Pool, *capturePublisher) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	cap := &capturePublisher{}
	api.SetPublisher(cap)
	return api, store, pool, cap
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
// actually emitted (wired through publishApproved → the legacy publisher).
func TestApproveInboundReviewCore_ReleasesAndPublishes(t *testing.T) {
	api, store, pool, cap := newReviewAPI(t)
	ctx := context.Background()
	userID, agentID, msgID := seedHeldInbound(t, store, ctx, "approvecore.example.com")
	meta := &identity.ReviewMessageMeta{ID: msgID, AgentID: agentID, Direction: "inbound", Sender: "evil@x.com", Subject: "held", Type: "received"}

	if derr := api.ApproveInboundReviewCore(ctx, userID, meta); derr != nil {
		t.Fatalf("ApproveInboundReviewCore: %+v", derr)
	}
	if got := statusOf(t, pool, ctx, msgID); got != identity.MessageStatusReviewApproved {
		t.Errorf("status = %q, want review_approved", got)
	}
	cap.waitType(t, webhookpub.EventEmailReviewApproved)

	// A second approve (already resolved) is a clean 409, not a double release.
	if derr := api.ApproveInboundReviewCore(ctx, userID, meta); derr == nil || derr.Status != http.StatusConflict || derr.Code != "message_not_pending" {
		t.Errorf("second approve = %+v, want 409 message_not_pending", derr)
	}
}

// TestRejectInboundReviewCore_DropsAndPublishes mirrors the approve core for reject.
func TestRejectInboundReviewCore_DropsAndPublishes(t *testing.T) {
	api, store, pool, cap := newReviewAPI(t)
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
	cap.waitType(t, webhookpub.EventEmailReviewRejected)
}

func statusOf(t *testing.T, pool *pgxpool.Pool, ctx context.Context, msgID string) string {
	t.Helper()
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, msgID).Scan(&st); err != nil {
		t.Fatalf("status lookup: %v", err)
	}
	return st
}
