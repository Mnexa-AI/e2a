package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// Fix #1: the single-message detail read (GetMessageWithContent) must be a
// superset of the list/summary view — it has to carry webhook_status,
// webhook_error and size_bytes, which previously were dropped because the
// read-marking UPDATE never joined webhook_deliveries and never sized the blob.
func TestGetMessageWithContentCarriesWebhookStatusAndSize(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "detail-wh")

	raw := []byte("From: alice@gmail.com\r\nSubject: Hi\r\n\r\nhello world body")
	in, err := store.CreateInboundMessage(
		ctx, "msg_detail_wh", agentID, "alice@gmail.com", "bot@acme.com",
		"<detail-wh@x>", "Hi", "conv-detail-wh", "unread", raw,
		map[string]string{}, nil, false, "", []string{"bot@acme.com"}, nil, nil,
		identity.InboundScreening{})
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	// Seed a delivery row so the detail read can surface its status/error.
	if _, err := pool.Exec(ctx,
		`INSERT INTO webhook_deliveries (message_id, status, attempts, last_error, created_at)
		 VALUES ($1, 'failed', 3, 'connection refused', now())`, in.ID); err != nil {
		t.Fatalf("seed webhook_deliveries: %v", err)
	}

	m, err := store.GetMessageWithContent(ctx, in.ID, agentID)
	if err != nil {
		t.Fatalf("GetMessageWithContent: %v", err)
	}
	if m.WebhookStatus != "failed" {
		t.Errorf("WebhookStatus = %q, want %q", m.WebhookStatus, "failed")
	}
	if m.WebhookError != "connection refused" {
		t.Errorf("WebhookError = %q, want %q", m.WebhookError, "connection refused")
	}
	if m.SizeBytes != len(raw) {
		t.Errorf("SizeBytes = %d, want %d (len of raw_message)", m.SizeBytes, len(raw))
	}
}
