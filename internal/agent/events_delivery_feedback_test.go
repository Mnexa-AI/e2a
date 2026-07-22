//go:build integration

package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/delivery"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

// realDeliveryFirer wires the delivery consumer to the SAME production
// publisher path as cmd/e2a/main.go's deliveryEventFirer: it persists each
// delivery-feedback event to webhook_events with its envelope routing keys
// (agent_id/conversation_id/message_id), which is exactly what the Events API
// filters on. Kept in lockstep with main.go by construction.
func realDeliveryFirer(outbox webhookpub.Outbox) delivery.Firer {
	return func(ctx context.Context, tx pgx.Tx, e delivery.FiredEvent) error {
		return outbox.PublishTx(ctx, tx, webhookpub.Event{
			ID:             webhookpub.DeterministicEventID(e.DedupKey),
			Type:           e.Type,
			CreatedAt:      e.OccurredAt,
			UserID:         e.UserID,
			AgentID:        e.AgentID,
			ConversationID: e.ConversationID,
			MessageID:      e.MessageID,
			Data:           e.Data,
		})
	}
}

// TestDeliveryFeedback_FindableByMessageID is the regression test for the
// review finding: delivery-feedback events (email.failed via SES Reject, and
// email.delivered) fired through the production publisher must be findable via
// GET /v1/events?message_id= and ?conversation_id=. Before the fix the firer
// dropped MessageID/ConversationID, so the persisted webhook_events row had
// message_id=NULL and the reconciliation query returned nothing.
func TestDeliveryFeedback_FindableByMessageID(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	user, err := store.CreateOrGetUser(ctx, "owner-dfb@example.com", "Owner", "g-dfb")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	const domain = "dfb.example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	agentEmail := "bot@" + domain
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// One rejected message (conversation-threaded) and one delivered message.
	const convID = "conv_dfb_1"
	rejMsg, err := store.CreateOutboundMessage(ctx, agentEmail, []string{"a@x.com"}, nil, nil, "Rejected subj", "send", "smtp", "ses-dfb-reject", convID, nil)
	if err != nil {
		t.Fatalf("CreateOutboundMessage(reject): %v", err)
	}
	if err := store.MarkMessageSent(ctx, rejMsg.ID, "relay", []string{"a@x.com"}, nil, nil); err != nil {
		t.Fatalf("MarkMessageSent(reject): %v", err)
	}
	dlvMsg, err := store.CreateOutboundMessage(ctx, agentEmail, []string{"b@x.com"}, nil, nil, "Delivered subj", "send", "smtp", "ses-dfb-delivered", "", nil)
	if err != nil {
		t.Fatalf("CreateOutboundMessage(delivered): %v", err)
	}
	if err := store.MarkMessageSent(ctx, dlvMsg.ID, "relay", []string{"b@x.com"}, nil, nil); err != nil {
		t.Fatalf("MarkMessageSent(delivered): %v", err)
	}

	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	consumer := delivery.NewConsumer(store, realDeliveryFirer(outbox))

	if err := consumer.Process(ctx, &delivery.Event{
		Kind: delivery.KindReject, SESMessageID: "ses-dfb-reject",
		Recipients: []delivery.RecipientOutcome{{Address: "a@x.com", Status: delivery.StatusFailed, Detail: "Bad content"}},
	}); err != nil {
		t.Fatalf("Process(reject): %v", err)
	}
	if err := consumer.Process(ctx, &delivery.Event{
		Kind: delivery.KindDelivery, SESMessageID: "ses-dfb-delivered",
		Recipients: []delivery.RecipientOutcome{{Address: "b@x.com", Status: delivery.StatusDelivered}},
	}); err != nil {
		t.Fatalf("Process(delivered): %v", err)
	}

	// The reconciliation query: GET /v1/events?message_id=<rejected msg> must
	// return the email.failed emitted via delivery feedback.
	byRejMsg, err := agent.ListEventsForUser(ctx, pool, user.ID, "", "", "", rejMsg.ID, nil, nil, time.Time{}, "", 50)
	if err != nil {
		t.Fatalf("ListEventsForUser(message_id=reject): %v", err)
	}
	if !hasEventType(byRejMsg, "email.failed") {
		t.Fatalf("GET /v1/events?message_id=%s returned no email.failed (found %v) — Reject event is invisible to the reconciliation query", rejMsg.ID, eventTypes(byRejMsg))
	}

	// And findable by its conversation thread.
	byConv, err := agent.ListEventsForUser(ctx, pool, user.ID, "", "", convID, "", nil, nil, time.Time{}, "", 50)
	if err != nil {
		t.Fatalf("ListEventsForUser(conversation_id): %v", err)
	}
	if !hasEventType(byConv, "email.failed") {
		t.Fatalf("GET /v1/events?conversation_id=%s returned no email.failed (found %v)", convID, eventTypes(byConv))
	}

	// The delivered event is likewise findable by its message_id.
	byDlvMsg, err := agent.ListEventsForUser(ctx, pool, user.ID, "", "", "", dlvMsg.ID, nil, nil, time.Time{}, "", 50)
	if err != nil {
		t.Fatalf("ListEventsForUser(message_id=delivered): %v", err)
	}
	if !hasEventType(byDlvMsg, "email.delivered") {
		t.Fatalf("GET /v1/events?message_id=%s returned no email.delivered (found %v)", dlvMsg.ID, eventTypes(byDlvMsg))
	}

	// Cross-check: the rejected message's query must NOT surface the OTHER
	// message's event (proves the message_id filter is actually applied, not a
	// blanket return).
	if hasEventType(byRejMsg, "email.delivered") {
		t.Fatalf("message_id filter leaked another message's event: %v", eventTypes(byRejMsg))
	}
}

func hasEventType(evs []agent.EventView, typ string) bool {
	for i := range evs {
		if evs[i].Type == typ {
			return true
		}
	}
	return false
}

func eventTypes(evs []agent.EventView) []string {
	out := make([]string, 0, len(evs))
	for i := range evs {
		out = append(out, evs[i].Type)
	}
	return out
}
