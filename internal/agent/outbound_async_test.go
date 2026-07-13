package agent_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

type blockingAsyncDeliverer struct {
	entered chan struct{}
	release chan struct{}
	out     outboundsend.DeliverOutcome
}

func (d *blockingAsyncDeliverer) Deliver(_ context.Context, _ *outboundsend.SendJob) outboundsend.DeliverOutcome {
	close(d.entered)
	<-d.release
	return d.out
}

func workerJob(id string, attempt int) *river.Job[outboundsend.OutboundSendArgs] {
	return workerJobWithID(id, 999, attempt)
}

func workerJobWithID(id string, jobID int64, attempt int) *river.Job[outboundsend.OutboundSendArgs] {
	return &river.Job[outboundsend.OutboundSendArgs]{
		JobRow: &rivertype.JobRow{ID: jobID, Attempt: attempt, MaxAttempts: outboundsend.MaxSendAttempts},
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

func TestSendWorker_TrashLinearizesWithDurableClaim(t *testing.T) {
	api, store, outbox, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asynctrashrace")
	trashPool, err := pgxpool.New(ctx, testutil.TestDBURL())
	if err != nil {
		t.Fatalf("open independent trash pool: %v", err)
	}
	t.Cleanup(trashPool.Close)
	if err := trashPool.Ping(ctx); err != nil {
		t.Fatalf("warm independent trash pool: %v", err)
	}
	trashStore := identity.NewStore(trashPool)

	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"race@gmail.com"}, Subject: "trash race", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}

	deliverer := &blockingAsyncDeliverer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		out: outboundsend.DeliverOutcome{
			ProviderMessageID: "<ses-trash-race@amazonses.com>",
			SentAs:            "relay",
		},
	}
	worker := outboundsend.NewSendWorker(
		agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()),
		deliverer,
	)
	workDone := make(chan error, 1)
	go func() { workDone <- worker.Work(ctx, workerJob(res.MessageID, 1)) }()
	<-deliverer.entered

	duplicateDeliverer := &blockingAsyncDeliverer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		out:     outboundsend.DeliverOutcome{ProviderMessageID: "duplicate"},
	}
	duplicateWorker := outboundsend.NewSendWorker(
		agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()),
		duplicateDeliverer,
	)
	duplicateDone := make(chan error, 1)
	go func() { duplicateDone <- duplicateWorker.Work(ctx, workerJobWithID(res.MessageID, 1000, 1)) }()
	select {
	case err := <-duplicateDone:
		if err != nil {
			t.Fatalf("duplicate worker: %v", err)
		}
	case <-duplicateDeliverer.entered:
		close(duplicateDeliverer.release)
		<-duplicateDone
		close(deliverer.release)
		<-workDone
		t.Fatal("a different River job ID submitted an already-claimed message")
	case <-time.After(200 * time.Millisecond):
		close(deliverer.release)
		<-workDone
		t.Fatal("duplicate worker did not resolve the foreign claim promptly")
	}

	trashDone := make(chan error, 1)
	go func() { trashDone <- trashStore.SoftDeleteMessage(ctx, res.MessageID, ag.ID) }()
	select {
	case err := <-trashDone:
		if err != nil {
			close(deliverer.release)
			<-workDone
			t.Fatalf("SoftDeleteMessage: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(deliverer.release)
		<-workDone
		t.Fatal("SoftDeleteMessage blocked after the send was durably claimed")
	}
	if err := trashStore.PurgeMessage(ctx, res.MessageID, ag.ID); !errors.Is(err, identity.ErrSendInProgress) {
		close(deliverer.release)
		<-workDone
		t.Fatalf("PurgeMessage during provider call = %v, want ErrSendInProgress", err)
	}

	close(deliverer.release)
	if err := <-workDone; err != nil {
		t.Fatalf("worker.Work: %v", err)
	}

	var deliveryStatus, providerID string
	var deleted bool
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT delivery_status, COALESCE(provider_message_id,''), deleted_at IS NOT NULL
			   FROM messages WHERE id=$1`, res.MessageID,
		).Scan(&deliveryStatus, &providerID, &deleted)
	}); err != nil {
		t.Fatalf("read trashed row: %v", err)
	}
	if deliveryStatus != "sent" || providerID != "<ses-trash-race@amazonses.com>" || !deleted {
		t.Fatalf("post-race row = status %q, provider %q, deleted %v; want sent/provider/deleted", deliveryStatus, providerID, deleted)
	}

	res, oerr = api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"agent-race@gmail.com"}, Subject: "agent trash race", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound(agent race): %+v", oerr)
	}
	deliverer = &blockingAsyncDeliverer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		out: outboundsend.DeliverOutcome{
			Err:       errors.New("550 rejected"),
			Permanent: true,
		},
	}
	worker = outboundsend.NewSendWorker(
		agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()),
		deliverer,
	)
	workDone = make(chan error, 1)
	go func() { workDone <- worker.Work(ctx, workerJob(res.MessageID, 1)) }()
	<-deliverer.entered

	trashDone = make(chan error, 1)
	go func() { trashDone <- trashStore.SoftDeleteAgent(ctx, ag.ID, user.ID) }()
	select {
	case err := <-trashDone:
		if err != nil {
			close(deliverer.release)
			<-workDone
			t.Fatalf("SoftDeleteAgent: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(deliverer.release)
		<-workDone
		t.Fatal("SoftDeleteAgent blocked after the send was durably claimed")
	}
	if _, err := trashStore.DeleteAgent(ctx, ag.ID, user.ID); !errors.Is(err, identity.ErrSendInProgress) {
		close(deliverer.release)
		<-workDone
		t.Fatalf("DeleteAgent during provider call = %v, want ErrSendInProgress", err)
	}

	close(deliverer.release)
	if err := <-workDone; err == nil {
		t.Fatal("worker.Work(agent race) succeeded after a permanent provider failure")
	}
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT m.delivery_status, COALESCE(m.provider_message_id,''), a.deleted_at IS NOT NULL
			   FROM messages m JOIN agent_identities a ON a.id = m.agent_id
			  WHERE m.id=$1`, res.MessageID,
		).Scan(&deliveryStatus, &providerID, &deleted)
	}); err != nil {
		t.Fatalf("read agent-trashed row: %v", err)
	}
	if deliveryStatus != "failed" || providerID != "" || !deleted {
		t.Fatalf("post-agent-race row = status %q, provider %q, agent deleted %v; want failed/empty/deleted", deliveryStatus, providerID, deleted)
	}
}

func TestSendWorker_TrashBeforeClaimCancelsWithoutSubmit(t *testing.T) {
	api, store, outbox, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asynctrashfirst")
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"never@gmail.com"}, Subject: "trash first", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}
	if err := store.SoftDeleteMessage(ctx, res.MessageID, ag.ID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}

	deliverer := &blockingAsyncDeliverer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		out:     outboundsend.DeliverOutcome{ProviderMessageID: "must-not-send"},
	}
	worker := outboundsend.NewSendWorker(
		agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()),
		deliverer,
	)
	if err := worker.Work(ctx, workerJob(res.MessageID, 1)); err != nil {
		t.Fatalf("worker.Work: %v", err)
	}
	select {
	case <-deliverer.entered:
		close(deliverer.release)
		t.Fatal("provider called after trash won before the send claim")
	default:
	}

	var deliveryStatus, detail string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT delivery_status, COALESCE(delivery_detail,'') FROM messages WHERE id=$1`,
			res.MessageID,
		).Scan(&deliveryStatus, &detail)
	}); err != nil {
		t.Fatalf("read canceled row: %v", err)
	}
	if deliveryStatus != "failed" || detail == "" {
		t.Fatalf("canceled row = status %q detail %q, want failed with detail", deliveryStatus, detail)
	}
}

func TestSendWorker_RetryBackoffReleasesClaimForPurge(t *testing.T) {
	api, store, outbox, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asyncreleasepurge")
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"retry@gmail.com"}, Subject: "retry then trash", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}
	worker := outboundsend.NewSendWorker(
		agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()),
		fakeAsyncDeliverer{out: outboundsend.DeliverOutcome{Err: errors.New("421 retry later")}},
	)
	if err := worker.Work(ctx, workerJob(res.MessageID, 1)); err == nil {
		t.Fatal("worker.Work succeeded after retryable provider failure")
	}
	if err := store.SoftDeleteMessage(ctx, res.MessageID, ag.ID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	if err := store.PurgeMessage(ctx, res.MessageID, ag.ID); err != nil {
		t.Fatalf("PurgeMessage after released retry claim: %v", err)
	}
}

func TestPurgeMessage_AllowsStaleOrphanedSendClaim(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asyncstalepurge")
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"stale@gmail.com"}, Subject: "stale claim", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}
	if payload, err := store.ClaimOutboundForSend(ctx, res.MessageID, 999); err != nil || payload == nil {
		t.Fatalf("ClaimOutboundForSend = (%v, %v), want payload", payload, err)
	}
	if err := store.SoftDeleteMessage(ctx, res.MessageID, ag.ID); err != nil {
		t.Fatalf("SoftDeleteMessage: %v", err)
	}
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE messages SET send_claimed_at = now() - make_interval(secs => $2) WHERE id=$1`,
			res.MessageID, int64(identity.OutboundSendClaimStaleWindow/time.Second)+1)
		return err
	}); err != nil {
		t.Fatalf("backdate claim: %v", err)
	}
	if err := store.PurgeMessage(ctx, res.MessageID, ag.ID); err != nil {
		t.Fatalf("PurgeMessage(stale claim): %v", err)
	}
}

func TestDeleteAgent_AllowsStaleOrphanedSendClaim(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "asyncstaleagent")
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"stale-agent@gmail.com"}, Subject: "stale agent claim", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}
	if payload, err := store.ClaimOutboundForSend(ctx, res.MessageID, 999); err != nil || payload == nil {
		t.Fatalf("ClaimOutboundForSend = (%v, %v), want payload", payload, err)
	}
	if err := store.SoftDeleteAgent(ctx, ag.ID, user.ID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE messages SET send_claimed_at = now() - make_interval(secs => $2) WHERE id=$1`,
			res.MessageID, int64(identity.OutboundSendClaimStaleWindow/time.Second)+1)
		return err
	}); err != nil {
		t.Fatalf("backdate claim: %v", err)
	}
	if _, err := store.DeleteAgent(ctx, ag.ID, user.ID); err != nil {
		t.Fatalf("DeleteAgent(stale claim): %v", err)
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
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE messages SET delivery_status='sending' WHERE id=$1`, res.MessageID)
		return err
	}); err != nil {
		t.Fatalf("claim row: %v", err)
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
