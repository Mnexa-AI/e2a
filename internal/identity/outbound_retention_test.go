package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func seedRetentionAgent(t *testing.T, store *identity.Store, ctx context.Context, domain string) (userID, agentID string) {
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
	return user.ID, ag.ID
}

// TestOutboundRetention_DirectSend: a normal send retains the composed MIME as
// raw_message (a readable Sent folder); a self-send passes nil (body lives on the
// inbound twin) and stores NULL.
func TestOutboundRetention_DirectSend(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	_, agentID := seedRetentionAgent(t, store, ctx, "ret.example.com")

	raw := []byte("From: bot@ret.example.com\r\nSubject: hi\r\n\r\nhello world")
	m, err := store.CreateOutboundMessage(ctx, agentID, []string{"a@b.com"}, nil, nil, "hi", "send", "smtp", "<p@x>", "", raw)
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	got, err := store.GetMessageWithContent(ctx, m.ID, agentID)
	if err != nil {
		t.Fatalf("GetMessageWithContent: %v", err)
	}
	if string(got.RawMessage) != string(raw) {
		t.Errorf("sent body not retained: got %d bytes, want %d", len(got.RawMessage), len(raw))
	}

	// Self-send path passes nil → NULL (the body is on the inbound twin).
	m2, err := store.CreateOutboundMessage(ctx, agentID, []string{"a@b.com"}, nil, nil, "hi2", "send", "loopback", "<p2@x>", "", nil)
	if err != nil {
		t.Fatalf("CreateOutboundMessage(nil): %v", err)
	}
	got2, err := store.GetMessageWithContent(ctx, m2.ID, agentID)
	if err != nil {
		t.Fatalf("GetMessageWithContent(nil): %v", err)
	}
	if len(got2.RawMessage) != 0 {
		t.Errorf("nil raw should store NULL, got %d bytes", len(got2.RawMessage))
	}
}

// TestOutboundRetention_HITLApprove: a HITL-approved send retains the sent MIME
// (raw_message) from the send callback, replacing the scrubbed draft columns.
func TestOutboundRetention_HITLApprove(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := seedRetentionAgent(t, store, ctx, "rethitl.example.com")

	pending, err := store.CreatePendingOutboundMessage(ctx, agentID, []string{"a@b.com"}, nil, nil,
		"subj", "body text", "", nil, "send", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	raw := []byte("From: bot@rethitl.example.com\r\nSubject: subj\r\n\r\nbody text")
	sent, err := store.ApproveAndSend(ctx, pending.ID, userID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			return identity.SendResult{
				ProviderMessageID: "<ses@x>",
				Method:            "smtp",
				To:                m.ToRecipients,
				Raw:               raw,
			}, nil
		})
	if err != nil {
		t.Fatalf("ApproveAndSend: %v", err)
	}

	got, err := store.GetMessageWithContent(ctx, sent.ID, agentID)
	if err != nil {
		t.Fatalf("GetMessageWithContent: %v", err)
	}
	if string(got.RawMessage) != string(raw) {
		t.Errorf("HITL-approved sent body not retained: got %d bytes, want %d", len(got.RawMessage), len(raw))
	}
	// The draft body columns are scrubbed (raw_message is the canonical copy).
	if got.BodyText != "" {
		t.Errorf("draft body_text should be scrubbed after send, got %q", got.BodyText)
	}
}
