package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// B4b (review correctness bug): the same outbound message carries
// webhook_status/webhook_error when listed via GetMessagesByAgent (which LEFT
// JOINs webhook_deliveries) but NOT when read inside its conversation thread
// via GetConversationByID (which lacks the join). Same MessageSummaryView type,
// different payload depending on access path.
func TestConversationMessagesCarryWebhookStatus(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "convo-wh")

	out, err := store.CreateOutboundMessage(
		ctx, agentID, []string{"alice@gmail.com"}, nil, nil,
		"Hi", "send", "smtp", "<wh-1@x>", "conv-wh",
	)
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	// Seed a delivery row for the message (the legacy per-message table the
	// list query joins on).
	if _, err := pool.Exec(ctx,
		`INSERT INTO webhook_deliveries (message_id, status, attempts, last_error, created_at)
		 VALUES ($1, 'delivered', 1, '', now())`, out.ID); err != nil {
		t.Fatalf("seed webhook_deliveries: %v", err)
	}

	// Control: the standalone list path surfaces the delivery status.
	msgs, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{
		AgentID: agentID, Direction: "outbound", Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetMessagesByAgent: %v", err)
	}
	var listStatus string
	for _, m := range msgs {
		if m.ID == out.ID {
			listStatus = m.WebhookStatus
		}
	}
	if listStatus != "delivered" {
		t.Fatalf("setup check: list webhook_status = %q, want %q", listStatus, "delivered")
	}

	// The conversation thread must show the SAME status for the SAME message.
	detail, err := store.GetConversationByID(ctx, agentID, "conv-wh")
	if err != nil {
		t.Fatalf("GetConversationByID: %v", err)
	}
	var threadStatus string
	var found bool
	for _, m := range detail.Messages {
		if m.ID == out.ID {
			found = true
			threadStatus = m.WebhookStatus
		}
	}
	if !found {
		t.Fatalf("message %s not in conversation detail", out.ID)
	}
	if threadStatus != "delivered" {
		t.Errorf("conversation thread webhook_status = %q, want %q (must match the list view)", threadStatus, "delivered")
	}
}
