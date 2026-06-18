package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// B1 (review correctness bug): outbound messages must carry their sender (the
// agent's own address). The outbound INSERTs never write the `sender` column,
// so it defaults to '' and every outbound message reports from="" — a REQUIRED
// wire field returning empty. This test reads an outbound message back and
// expects the agent address.
func TestOutboundMessageHasSender(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "out-sender")
	agentEmail := "bot@out-sender.example.com"

	out, err := store.CreateOutboundMessage(
		ctx, agentID, []string{"alice@gmail.com"}, nil, nil,
		"Hello", "send", "smtp", "<out-sender-1@x>", "conv-sender",
	)
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}

	// Read back via the detail path the API uses.
	got, err := store.GetMessageWithContent(ctx, out.ID, agentID)
	if err != nil {
		t.Fatalf("GetMessageWithContent: %v", err)
	}
	if got.Sender != agentEmail {
		t.Errorf("outbound sender = %q, want %q (the agent's own address)", got.Sender, agentEmail)
	}

	// And via the list path (MessageSummaryView.from is sourced from Sender).
	msgs, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{
		AgentID: agentID, Direction: "outbound", Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetMessagesByAgent: %v", err)
	}
	var found bool
	for _, m := range msgs {
		if m.ID == out.ID {
			found = true
			if m.Sender != agentEmail {
				t.Errorf("list outbound sender = %q, want %q", m.Sender, agentEmail)
			}
		}
	}
	if !found {
		t.Fatalf("outbound message %s not returned by GetMessagesByAgent", out.ID)
	}
}
