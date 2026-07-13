package agent_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
)

// TestApprovePendingCore_AsyncExternal: with the outbound enqueuer wired (async
// mode), a dashboard approve of a non-self-send hold transitions it to
// review_approved + delivery_status='accepted', stamps the enqueued send_job_id, and
// returns method/sent_as — WITHOUT submitting inline (no provider id yet; the
// SendWorker submits + emits email.sent later).
func TestApprovePendingCore_AsyncExternal(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprasyncext")

	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{"alice@external.test"}, nil, nil, "Held", "body", "", nil, "send", "", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}

	idemCompleted := false
	complete := func(ctx context.Context, tx pgx.Tx, approved *identity.Message) error {
		var deliveryStatus string
		var sendJobID *int64
		if err := tx.QueryRow(ctx,
			`SELECT delivery_status, send_job_id FROM messages WHERE id=$1`, approved.ID,
		).Scan(&deliveryStatus, &sendJobID); err != nil {
			return err
		}
		if deliveryStatus != "accepted" || sendJobID == nil || *sendJobID != 999 {
			t.Fatalf("idempotency completion ran before async accept was durable in tx: delivery_status=%q send_job_id=%v", deliveryStatus, sendJobID)
		}
		idemCompleted = true
		return nil
	}
	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, complete)
	if oerr != nil {
		t.Fatalf("ApprovePendingCore: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if sent.Status != identity.MessageStatusSent {
		t.Errorf("status = %q, want %q", sent.Status, identity.MessageStatusSent)
	}
	if sent.DeliveryStatus != "accepted" {
		t.Errorf("DeliveryStatus = %q, want accepted (async)", sent.DeliveryStatus)
	}
	if sent.Method != "smtp" {
		t.Errorf("Method = %q, want smtp (should be populated on the accepted view)", sent.Method)
	}
	if !idemCompleted {
		t.Error("async approval did not complete idempotency inside the accept transaction")
	}

	var deliveryStatus, providerID string
	var sendJobID *int64
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT delivery_status, COALESCE(provider_message_id,''), send_job_id FROM messages WHERE id=$1`,
			msg.ID,
		).Scan(&deliveryStatus, &providerID, &sendJobID)
	}); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deliveryStatus != "accepted" {
		t.Errorf("db delivery_status = %q, want accepted", deliveryStatus)
	}
	if providerID != "" {
		t.Errorf("provider_message_id = %q, want empty at accept (SendWorker fills it)", providerID)
	}
	if sendJobID == nil || *sendJobID != 999 { // fakeOutboundEnqueuer.jobID
		t.Errorf("send_job_id = %v, want 999 (enqueued)", sendJobID)
	}
}

// TestApprovePendingCore_AsyncSelfSendStaysSync: even in async mode, a self-send
// (single To == the agent's own address) is NOT enqueued onto QueueOutbound — it
// falls through to the sync loopback path, so no send_job_id is stamped and the row
// is not delivery_status='accepted'.
func TestApprovePendingCore_AsyncSelfSendStaysSync(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprasyncself")

	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{ag.EmailAddress()}, nil, nil, "To self", "body", "", nil, "send", "", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}

	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, nil)
	if oerr != nil {
		t.Fatalf("ApprovePendingCore: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if sent.Status != identity.MessageStatusSent {
		t.Errorf("status = %q, want %q", sent.Status, identity.MessageStatusSent)
	}
	if sent.DeliveryStatus == "accepted" {
		t.Errorf("self-send must NOT be async-accepted (sync loopback), got DeliveryStatus=%q", sent.DeliveryStatus)
	}

	var sendJobID *int64
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT send_job_id FROM messages WHERE id=$1`, msg.ID).Scan(&sendJobID)
	}); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if sendJobID != nil {
		t.Errorf("self-send must NOT be enqueued onto QueueOutbound, send_job_id = %v", *sendJobID)
	}
}
