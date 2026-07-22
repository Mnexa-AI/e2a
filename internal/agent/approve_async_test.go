package agent_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/identity"
)

// TestApprovePendingCore_AsyncExternal: with the outbound enqueuer wired (async
// mode), a dashboard approve of a non-self-send hold transitions it to
// review_approved + delivery_status='accepted', stamps the enqueued send_job_id, and
// returns method/sent_as — WITHOUT submitting inline (no provider id yet; the
// SendWorker submits + emits email.sent later).
func TestApprovePendingCore_AsyncExternalLifecycle(t *testing.T) {
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
		var approvedCount, queued int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE reason_code='review.approved'), count(*) FILTER (WHERE reason_code='queue.outbound_submission') FROM message_lifecycle_transitions WHERE message_id=$1`, approved.ID).Scan(&approvedCount, &queued); err != nil {
			return err
		}
		if approvedCount != 1 || queued != 1 {
			t.Fatalf("approval lifecycle approved=%d queued=%d", approvedCount, queued)
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
func TestApprovePendingCore_AsyncSelfSendLifecycleStaysSync(t *testing.T) {
	api, store, _, _, pool := setupAsyncAPIWithPool(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprasyncself")

	msg, err := store.CreatePendingOutboundMessage(ctx, ag.ID,
		[]string{ag.EmailAddress()}, nil, nil, "To self", "body", "", nil, "send", "", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}

	idemCompleted := false
	complete := func(ctx context.Context, tx pgx.Tx, approved *identity.Message) error {
		var deliveryStatus string
		var raw []byte
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(delivery_status,''), raw_message FROM messages WHERE id=$1`, approved.ID,
		).Scan(&deliveryStatus, &raw); err != nil {
			return err
		}
		var inboundRows, outcomeEvents int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='To self'`, ag.ID,
		).Scan(&inboundRows); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM webhook_events
			  WHERE message_id IN (
			    SELECT id FROM messages WHERE agent_id=$1 AND subject='To self'
			  ) AND type IN ('email.sent','email.received')`, ag.ID,
		).Scan(&outcomeEvents); err != nil {
			return err
		}
		if deliveryStatus != "sent" || len(raw) == 0 || inboundRows != 1 || outcomeEvents != 2 {
			t.Fatalf("self-send approval not atomic in completion tx: delivery_status=%q raw=%d inbound=%d events=%d",
				deliveryStatus, len(raw), inboundRows, outcomeEvents)
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
	if sent.DeliveryStatus != "sent" {
		t.Errorf("self-send delivery_status=%q want sent", sent.DeliveryStatus)
	}
	if len(sent.RawMessage) == 0 {
		t.Error("self-send approved Sent copy must retain composed MIME")
	}
	if !idemCompleted {
		t.Error("self-send approval did not complete idempotency in the local-delivery transaction")
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

	var inboundRows int
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='To self'`, ag.ID,
		).Scan(&inboundRows)
	}); err != nil {
		t.Fatal(err)
	}
	if inboundRows != 1 {
		t.Errorf("self-send approval inbound rows=%d want 1", inboundRows)
	}
	var inboundID string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='To self'`, ag.ID).Scan(&inboundID)
	}); err != nil {
		t.Fatal(err)
	}
	assertApprovedLoopbackLifecycleParity(t, pool, msg.ID, inboundID)
}
