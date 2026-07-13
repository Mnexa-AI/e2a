package identity_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestCreateOutboundMessageTx_AcceptedRow pins the async accept-tx store shape
// (async-message-pipeline.md, slice C): the row lands with delivery_status='accepted',
// status='sent' (the two-column model: lifecycle vs send-progression), an EMPTY
// provider_message_id, and the envelope_from / sent_as decided at compose time;
// StampSendJobIDTx records the River job id in the same spirit.
func TestCreateOutboundMessageTx_AcceptedRow(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "async-accept")

	var msg *identity.Message
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		m, err := store.CreateOutboundMessageTx(ctx, tx, agentID,
			[]string{"alice@gmail.com"}, []string{"cc@gmail.com"}, []string{"bcc@gmail.com"},
			"Hi", "send", "smtp", "", "conv-async",
			[]byte("From: bot\r\n\r\nbody"), "accepted", "agent@test.e2a.dev", "relay")
		if err != nil {
			return err
		}
		msg = m
		return store.StampSendJobIDTx(ctx, tx, m.ID, 4242)
	}); err != nil {
		t.Fatalf("accept tx: %v", err)
	}

	var (
		deliveryStatus, status, providerID, sentAs, envFrom string
		sendJobID                                           *int64
	)
	if err := pool.QueryRow(ctx,
		`SELECT delivery_status, status, COALESCE(provider_message_id,''), COALESCE(sent_as,''), COALESCE(envelope_from,''), send_job_id
		   FROM messages WHERE id=$1`, msg.ID,
	).Scan(&deliveryStatus, &status, &providerID, &sentAs, &envFrom, &sendJobID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deliveryStatus != "accepted" {
		t.Errorf("delivery_status = %q, want accepted", deliveryStatus)
	}
	if status != identity.MessageStatusSent {
		t.Errorf("status = %q, want %q (lifecycle column)", status, identity.MessageStatusSent)
	}
	if providerID != "" {
		t.Errorf("provider_message_id = %q, want empty at accept", providerID)
	}
	if sentAs != "relay" {
		t.Errorf("sent_as = %q, want relay", sentAs)
	}
	if envFrom != "agent@test.e2a.dev" {
		t.Errorf("envelope_from = %q, want agent@test.e2a.dev", envFrom)
	}
	if sendJobID == nil || *sendJobID != 4242 {
		t.Errorf("send_job_id = %v, want 4242", sendJobID)
	}

	// LoadOutboundForSend returns the worker payload: envelope = to+cc+bcc, raw, etc.
	p, err := store.LoadOutboundForSend(ctx, msg.ID)
	if err != nil {
		t.Fatalf("LoadOutboundForSend: %v", err)
	}
	if p == nil {
		t.Fatal("LoadOutboundForSend returned nil for an accepted row")
	}
	if p.DeliveryStatus != "accepted" || p.EnvelopeFrom != "agent@test.e2a.dev" || p.SentAs != "relay" {
		t.Errorf("payload = %+v, want accepted/agent@.../relay", p)
	}
	if len(p.Recipients) != 3 {
		t.Errorf("recipients = %v, want 3 (to+cc+bcc)", p.Recipients)
	}
	if string(p.Raw) != "From: bot\r\n\r\nbody" {
		t.Errorf("raw = %q, want the persisted MIME", p.Raw)
	}

	// A missing row loads as (nil, nil) — the worker's no-op signal.
	gone, err := store.LoadOutboundForSend(ctx, "msg_does_not_exist")
	if err != nil || gone != nil {
		t.Errorf("LoadOutboundForSend(missing) = (%v, %v), want (nil, nil)", gone, err)
	}
}

// TestMarkOutboundSentTx flips an accepted row to sent with the provider id +
// per-recipient rows, and returns the owning user/domain for the caller.
func TestMarkOutboundSentTx(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "async-sent")

	var msgID string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		m, err := store.CreateOutboundMessageTx(ctx, tx, agentID,
			[]string{"a@gmail.com"}, nil, nil, "S", "send", "smtp", "", "conv-s",
			[]byte("raw"), "accepted", "agent@test.e2a.dev", "relay")
		msgID = m.ID
		return err
	}); err != nil {
		t.Fatalf("accept tx: %v", err)
	}

	var info *identity.OutboundSentInfo
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		i, err := store.MarkOutboundSentTx(ctx, tx, msgID, "<ses-123@amazonses.com>")
		info = i
		return err
	}); err != nil {
		t.Fatalf("MarkOutboundSentTx: %v", err)
	}
	if info == nil || info.UserID == "" || info.Domain == "" {
		t.Fatalf("info = %+v, want user+domain populated", info)
	}

	var deliveryStatus, providerID string
	if err := pool.QueryRow(ctx,
		`SELECT delivery_status, provider_message_id FROM messages WHERE id=$1`, msgID,
	).Scan(&deliveryStatus, &providerID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deliveryStatus != "sent" {
		t.Errorf("delivery_status = %q, want sent", deliveryStatus)
	}
	if providerID != "<ses-123@amazonses.com>" {
		t.Errorf("provider_message_id = %q, want the SES id", providerID)
	}
	var rcpt int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM message_recipients WHERE message_id=$1 AND status='sent'`, msgID,
	).Scan(&rcpt); err != nil {
		t.Fatalf("count recipients: %v", err)
	}
	if rcpt != 1 {
		t.Errorf("sent recipient rows = %d, want 1", rcpt)
	}

	// A gone row marks as (nil, nil) — no-op.
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		i, err := store.MarkOutboundSentTx(ctx, tx, "msg_gone", "x")
		if i != nil {
			t.Errorf("MarkOutboundSentTx(missing) info = %+v, want nil", i)
		}
		return err
	}); err != nil {
		t.Fatalf("MarkOutboundSentTx(missing): %v", err)
	}
}

// TestMarkOutboundFailedTx flips an accepted row to failed with the detail.
func TestMarkOutboundFailedTx(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	agentID := convoTestSetup(t, store, "async-failed")

	var msgID string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		m, err := store.CreateOutboundMessageTx(ctx, tx, agentID,
			[]string{"a@gmail.com"}, nil, nil, "F", "send", "smtp", "", "conv-f",
			[]byte("raw"), "accepted", "agent@test.e2a.dev", "relay")
		msgID = m.ID
		return err
	}); err != nil {
		t.Fatalf("accept tx: %v", err)
	}

	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := store.MarkOutboundFailedTx(ctx, tx, msgID, "550 mailbox unavailable")
		return err
	}); err != nil {
		t.Fatalf("MarkOutboundFailedTx: %v", err)
	}

	var deliveryStatus, detail string
	if err := pool.QueryRow(ctx,
		`SELECT delivery_status, COALESCE(delivery_detail,'') FROM messages WHERE id=$1`, msgID,
	).Scan(&deliveryStatus, &detail); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deliveryStatus != "failed" {
		t.Errorf("delivery_status = %q, want failed", deliveryStatus)
	}
	if detail != "550 mailbox unavailable" {
		t.Errorf("delivery_detail = %q, want the failure detail", detail)
	}

	var sentMsgID string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		m, err := store.CreateOutboundMessageTx(ctx, tx, agentID,
			[]string{"b@gmail.com"}, nil, nil, "Already sent", "send", "smtp", "", "conv-sent",
			[]byte("raw"), "accepted", "agent@test.e2a.dev", "relay")
		if err != nil {
			return err
		}
		sentMsgID = m.ID
		_, err = store.MarkOutboundSentTx(ctx, tx, sentMsgID, "<provider-sent>")
		return err
	}); err != nil {
		t.Fatalf("create sent message: %v", err)
	}

	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		info, err := store.MarkOutboundFailedTx(ctx, tx, sentMsgID, "late terminal job")
		if info != nil {
			t.Errorf("MarkOutboundFailedTx(sent) info = %+v, want nil", info)
		}
		return err
	}); err != nil {
		t.Fatalf("MarkOutboundFailedTx(sent): %v", err)
	}

	var providerID string
	if err := pool.QueryRow(ctx,
		`SELECT delivery_status, provider_message_id, COALESCE(delivery_detail,'') FROM messages WHERE id=$1`, sentMsgID,
	).Scan(&deliveryStatus, &providerID, &detail); err != nil {
		t.Fatalf("read sent row: %v", err)
	}
	if deliveryStatus != "sent" {
		t.Errorf("sent row delivery_status = %q, want sent", deliveryStatus)
	}
	if providerID != "<provider-sent>" {
		t.Errorf("sent row provider_message_id = %q, want unchanged", providerID)
	}
	if detail != "" {
		t.Errorf("sent row delivery_detail = %q, want unchanged empty detail", detail)
	}
}
