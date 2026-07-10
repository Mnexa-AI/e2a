package hitlworker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// capPub captures published events for the TTL-resolution emission tests.
type capPub struct {
	mu     sync.Mutex
	events []webhookpub.Event
}

func (c *capPub) Publish(_ context.Context, e webhookpub.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *capPub) waitFor(t *testing.T, typ string) webhookpub.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, e := range c.events {
			if e.Type == typ {
				c.mu.Unlock()
				return e
			}
		}
		c.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s event", typ)
	return webhookpub.Event{}
}

// dataOf returns the event payload as a map.
func dataOf(t *testing.T, e webhookpub.Event) map[string]interface{} {
	t.Helper()
	m, ok := e.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event data is %T, want map", e.Data)
	}
	return m
}

// TestWorkerEmitsInboundReviewApprovedOnExpiry: a TTL auto-APPROVED inbound hold
// fires email.review_approved — its only push signal — routed on the agent owner.
func TestWorkerEmitsInboundReviewApprovedOnExpiry(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	cap := &capPub{}
	w.SetPublisher(cap)
	ctx := context.Background()

	agent := prepareAgent(t, store, "emit-inapprove", identity.HITLExpirationApprove)
	exp := time.Now().Add(time.Hour)
	m, err := store.CreateInboundMessage(ctx, "", agent.ID, "evil@x.com", agent.ID, "", "held", "", "unread",
		[]byte("Subject: held\r\n\r\nx"), nil, nil, false, "", []string{agent.ID}, nil, nil,
		identity.InboundScreening{Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp})
	if err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET approval_expires_at = now() - interval '1 hour' WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	w.RunOnce(ctx)

	e := cap.waitFor(t, webhookpub.EventEmailReviewApproved)
	if e.UserID != agent.UserID {
		t.Errorf("routing UserID = %q, want the owner %q", e.UserID, agent.UserID)
	}
	if e.MessageID != m.ID {
		t.Errorf("MessageID = %q, want %q", e.MessageID, m.ID)
	}
	d := dataOf(t, e)
	if d["direction"] != "inbound" {
		t.Errorf("direction = %v, want inbound", d["direction"])
	}
	if d["auto_resolved"] != true {
		t.Errorf("auto_resolved = %v, want true (distinguishes TTL from human)", d["auto_resolved"])
	}
}

// TestWorkerEmitsInboundReviewRejectedOnExpiry: a TTL auto-REJECTED inbound hold
// fires email.review_rejected with reason ttl_expired.
func TestWorkerEmitsInboundReviewRejectedOnExpiry(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	cap := &capPub{}
	w.SetPublisher(cap)
	ctx := context.Background()

	agent := prepareAgent(t, store, "emit-inreject", identity.HITLExpirationReject)
	exp := time.Now().Add(time.Hour)
	m, err := store.CreateInboundMessage(ctx, "", agent.ID, "evil@x.com", agent.ID, "", "held", "", "unread",
		[]byte("Subject: held\r\n\r\nx"), nil, nil, false, "", []string{agent.ID}, nil, nil,
		identity.InboundScreening{Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp})
	if err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET approval_expires_at = now() - interval '1 hour' WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	w.RunOnce(ctx)

	e := cap.waitFor(t, webhookpub.EventEmailReviewRejected)
	if e.UserID != agent.UserID {
		t.Errorf("routing UserID = %q, want owner %q", e.UserID, agent.UserID)
	}
	d := dataOf(t, e)
	if d["direction"] != "inbound" || d["rejection_reason"] != "ttl_expired" {
		t.Errorf("payload = %v, want inbound/ttl_expired", d)
	}
}

// TestWorkerEmitsOutboundReviewRejectedOnExpiry: a TTL auto-rejected OUTBOUND hold
// fires email.review_rejected (direction outbound), matching the human reject.
func TestWorkerEmitsOutboundReviewRejectedOnExpiry(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	cap := &capPub{}
	w.SetPublisher(cap)
	ctx := context.Background()

	agent := prepareAgent(t, store, "emit-outreject", identity.HITLExpirationReject)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil, "Held", "body", "<p>html</p>", nil, "send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	e := cap.waitFor(t, webhookpub.EventEmailReviewRejected)
	if e.UserID != agent.UserID {
		t.Errorf("routing UserID = %q, want owner %q", e.UserID, agent.UserID)
	}
	d := dataOf(t, e)
	if d["direction"] != "outbound" {
		t.Errorf("direction = %v, want outbound", d["direction"])
	}
}

// TestWorkerEmitsOutboundReviewApprovedOnExpiry: a TTL auto-approved OUTBOUND hold
// (auto-sent via SMTP) fires email.review_approved (direction outbound).
func TestWorkerEmitsOutboundReviewApprovedOnExpiry(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	cap := &capPub{}
	w.SetPublisher(cap)
	ctx := context.Background()

	agent := prepareAgent(t, store, "emit-outapprove", identity.HITLExpirationApprove)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{"alice@example.com"}, nil, nil, "Held", "body", "<p>html</p>", nil, "send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)

	w.RunOnce(ctx)

	e := cap.waitFor(t, webhookpub.EventEmailReviewApproved)
	if e.UserID != agent.UserID {
		t.Errorf("routing UserID = %q, want owner %q", e.UserID, agent.UserID)
	}
	d := dataOf(t, e)
	if d["direction"] != "outbound" {
		t.Errorf("direction = %v, want outbound", d["direction"])
	}
	if d["auto_resolved"] != true {
		t.Errorf("auto_resolved = %v, want true", d["auto_resolved"])
	}
}

// TestWorkerNilPublisherStillResolves: with no publisher wired the sweep still
// transitions rows (emission is best-effort, never load-bearing for resolution).
func TestWorkerNilPublisherStillResolves(t *testing.T) {
	w, store, pool, _ := setupWorker(t) // no SetPublisher
	ctx := context.Background()

	agent := prepareAgent(t, store, "emit-nilpub", identity.HITLExpirationApprove)
	exp := time.Now().Add(time.Hour)
	m, err := store.CreateInboundMessage(ctx, "", agent.ID, "e@x.com", agent.ID, "", "held", "", "unread",
		[]byte("Subject: held\r\n\r\nx"), nil, nil, false, "", []string{agent.ID}, nil, nil,
		identity.InboundScreening{Status: identity.MessageStatusPendingReview, ApprovalExpiresAt: &exp})
	if err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messages SET approval_expires_at = now() - interval '1 hour' WHERE id=$1`, m.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	w.RunOnce(ctx) // must not panic on nil publisher

	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM messages WHERE id=$1`, m.ID).Scan(&st); err != nil {
		t.Fatalf("read: %v", err)
	}
	if st != identity.MessageStatusReviewExpiredApproved {
		t.Errorf("status = %q, want review_expired_approved (resolution must not depend on the publisher)", st)
	}
}
