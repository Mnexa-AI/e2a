package agent_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/outboundsend"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// fakeOutboundEnqueuer stands in for the shared River client: it records the
// in-tx enqueue and returns a stable job id the accept-tx stamps on the row.
type fakeOutboundEnqueuer struct{ jobID int64 }

func (f *fakeOutboundEnqueuer) EnqueueSendTx(_ context.Context, _ pgx.Tx, _ string) (int64, error) {
	return f.jobID, nil
}

// fakeAsyncDeliverer is the SMTP submit the SendWorker calls — no network.
type fakeAsyncDeliverer struct{ out outboundsend.DeliverOutcome }

func (f fakeAsyncDeliverer) Deliver(_ context.Context, _ *outboundsend.SendJob) outboundsend.DeliverOutcome {
	return f.out
}

func workerJob(id string, attempt int) *river.Job[outboundsend.OutboundSendArgs] {
	return &river.Job[outboundsend.OutboundSendArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: outboundsend.MaxSendAttempts},
		Args:   outboundsend.OutboundSendArgs{MessageID: id},
	}
}

func setupAsyncAPI(t *testing.T) (*agent.API, *identity.Store, webhookpub.Outbox, *fakeOutboundEnqueuer) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	outbox := webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true))
	api.SetOutbox(outbox)
	enq := &fakeOutboundEnqueuer{jobID: 999}
	api.SetOutboundEnqueuer(enq)
	return api, store, outbox, enq
}

// TestDeliverOutbound_AsyncAccept is the end-to-end slice-C check: with the async
// enqueuer wired, a real (non-self) send is durably accepted (not submitted
// inline) — it returns status=accepted, persists a delivery_status='accepted' row
// with the send_job_id stamped, and does NOT yet emit email.sent. Running the real
// SendWorker over the store adapter + a fake deliverer then flips the row to sent,
// records the provider id, and emits email.sent.
func TestDeliverOutbound_AsyncAccept(t *testing.T) {
	api, store, outbox, enq := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asyncacc")

	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"alice@gmail.com"}, Subject: "async hi", Body: "queued not sent",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if res.Status != "accepted" {
		t.Fatalf("Status = %q, want accepted", res.Status)
	}
	if res.MessageID == "" || res.Method != "smtp" {
		t.Fatalf("res = %+v, want a msg id + method=smtp", res)
	}

	// Row is accepted, job stamped, not yet sent, no provider id.
	var (
		deliveryStatus, providerID string
		sendJobID                  *int64
		raw                        []byte
	)
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT delivery_status, COALESCE(provider_message_id,''), send_job_id, raw_message FROM messages WHERE id=$1`,
			res.MessageID,
		).Scan(&deliveryStatus, &providerID, &sendJobID, &raw)
	}); err != nil {
		t.Fatalf("read accepted row: %v", err)
	}
	if deliveryStatus != "accepted" {
		t.Errorf("delivery_status = %q, want accepted", deliveryStatus)
	}
	if providerID != "" {
		t.Errorf("provider_message_id = %q, want empty at accept", providerID)
	}
	if sendJobID == nil || *sendJobID != enq.jobID {
		t.Errorf("send_job_id = %v, want %d", sendJobID, enq.jobID)
	}
	if len(raw) == 0 {
		t.Errorf("raw_message empty; the composed bytes must be persisted for the worker")
	}
	// No email.sent yet (the message isn't sent).
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailSent); n != 0 {
		t.Errorf("email.sent events at accept = %d, want 0", n)
	}

	// Run the real SendWorker over the production store adapter + a fake submit.
	adapter := agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker())
	worker := outboundsend.NewSendWorker(adapter, fakeAsyncDeliverer{
		out: outboundsend.DeliverOutcome{ProviderMessageID: "<ses-async-1@amazonses.com>", SentAs: "relay"},
	})
	if err := worker.Work(ctx, workerJob(res.MessageID, 1)); err != nil {
		t.Fatalf("worker.Work: %v", err)
	}

	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT delivery_status, COALESCE(provider_message_id,'') FROM messages WHERE id=$1`,
			res.MessageID,
		).Scan(&deliveryStatus, &providerID)
	}); err != nil {
		t.Fatalf("read sent row: %v", err)
	}
	if deliveryStatus != "sent" {
		t.Errorf("post-worker delivery_status = %q, want sent", deliveryStatus)
	}
	if providerID != "<ses-async-1@amazonses.com>" {
		t.Errorf("provider_message_id = %q, want the SES id from the worker", providerID)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailSent); n != 1 {
		t.Errorf("email.sent events after worker = %d, want 1", n)
	}

	// Re-running the worker is a no-op (delivery_status past accepted/sending).
	if err := worker.Work(ctx, workerJob(res.MessageID, 2)); err != nil {
		t.Fatalf("worker.Work re-drive: %v", err)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailSent); n != 1 {
		t.Errorf("email.sent events after re-drive = %d, want 1 (idempotent)", n)
	}
}

// TestOutboundSendStore_MarkFailed pins the failure path: the adapter flips the
// row to failed and emits email.failed.
func TestOutboundSendStore_MarkFailed(t *testing.T) {
	api, store, outbox, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asyncfail")

	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"bob@gmail.com"}, Subject: "will fail", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}

	adapter := agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker())
	if err := adapter.MarkFailed(ctx, res.MessageID, 6, "550 mailbox unavailable"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	var deliveryStatus, detail string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT delivery_status, COALESCE(delivery_detail,'') FROM messages WHERE id=$1`,
			res.MessageID,
		).Scan(&deliveryStatus, &detail)
	}); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if deliveryStatus != "failed" {
		t.Errorf("delivery_status = %q, want failed", deliveryStatus)
	}
	if detail != "550 mailbox unavailable" {
		t.Errorf("delivery_detail = %q, want the failure detail", detail)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailFailed); n != 1 {
		t.Errorf("email.failed events = %d, want 1", n)
	}
}

func countEvents(t *testing.T, store *identity.Store, userID, eventType string) int {
	t.Helper()
	var n int
	if err := store.WithTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM webhook_events WHERE user_id=$1 AND type=$2`, userID, eventType,
		).Scan(&n)
	}); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}
