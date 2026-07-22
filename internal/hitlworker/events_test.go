package hitlworker_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

func assertTTLReviewEventLifecycleMatchesRow(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, messageID, eventType string, event webhookpub.Event, wantReason messagelifecycle.ReasonCode) {
	t.Helper()
	data := dataOf(t, event)
	raw, err := json.Marshal(data["lifecycle_transitions"])
	if err != nil {
		t.Fatal(err)
	}
	var got []messagelifecycle.MessageLifecycleTransition
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var persistedRaw []byte
	if err := pool.QueryRow(context.Background(), `SELECT jsonb_build_array(to_jsonb(t) - 'dedupe_key') FROM message_lifecycle_transitions t WHERE message_id=$1 AND reason_code=$2`, messageID, wantReason).Scan(&persistedRaw); err != nil {
		t.Fatalf("read persisted lifecycle: %v", err)
	}
	var persisted []messagelifecycle.MessageLifecycleTransition
	if err := json.Unmarshal(persistedRaw, &persisted); err != nil {
		t.Fatalf("decode persisted lifecycle: %v", err)
	}
	if len(got) == 1 && len(persisted) == 1 {
		got[0].OccurredAt = got[0].OccurredAt.UTC()
		persisted[0].OccurredAt = persisted[0].OccurredAt.UTC()
	}
	if len(got) != 1 || len(persisted) != 1 || got[0].ReasonCode != wantReason || !reflect.DeepEqual(got[0], persisted[0]) {
		t.Fatalf("%s lifecycle = %+v, persisted = %+v, want exact %s transition", eventType, got, persisted, wantReason)
	}
}

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
	assertTTLReviewEventLifecycleMatchesRow(t, pool, m.ID, webhookpub.EventEmailReviewApproved, e, messagelifecycle.ReasonReviewExpiredApproved)
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
	if d["direction"] != "inbound" || d["reason"] != "ttl_expired" {
		t.Errorf("payload = %v, want inbound/ttl_expired", d)
	}
	assertTTLReviewEventLifecycleMatchesRow(t, pool, m.ID, webhookpub.EventEmailReviewRejected, e, messagelifecycle.ReasonReviewExpiredRejected)
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
	assertTTLReviewEventLifecycleMatchesRow(t, pool, msg.ID, webhookpub.EventEmailReviewRejected, e, messagelifecycle.ReasonReviewExpiredRejected)
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
	assertTTLReviewEventLifecycleMatchesRow(t, pool, msg.ID, webhookpub.EventEmailReviewApproved, e, messagelifecycle.ReasonReviewExpiredApproved)
}

func TestWorkerLoopbackReviewApprovedOmitsProviderID(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	cap := &capPub{}
	w.SetPublisher(cap)
	ctx := context.Background()
	agent := prepareAgent(t, store, "emit-loopback-approve", identity.HITLExpirationApprove)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID,
		[]string{agent.EmailAddress()}, nil, nil, "Held self", "body", "", nil, "send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)
	w.RunOnce(ctx)
	d := dataOf(t, cap.waitFor(t, webhookpub.EventEmailReviewApproved))
	if d["method"] != "loopback" {
		t.Fatalf("method = %v", d["method"])
	}
	if _, exists := d["provider_message_id"]; exists {
		t.Fatalf("providerless loopback review event leaked provider_message_id: %v", d)
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
