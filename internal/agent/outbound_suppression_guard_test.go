package agent_test

// End-to-end (real store adapter + real DB) coverage of the SendWorker's
// pre-provider suppression guard: a suppression added AFTER a send was
// durably accepted + queued — approval or direct — still prevents provider
// I/O; the row records a durable terminal failure and email.failed fires.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/outboundsend"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

// countingDeliverer records provider submits so the guard can assert zero I/O.
type countingDeliverer struct {
	calls int
	out   outboundsend.DeliverOutcome
}

func (d *countingDeliverer) Deliver(_ context.Context, _ *outboundsend.SendJob) outboundsend.DeliverOutcome {
	d.calls++
	return d.out
}

func TestDeliverOutbound_AgentSuppressionBlocksSendReplyAndForward(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "agentacceptscope")
	if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "blocked@external.test", "opted out", "unsubscribe", nil); err != nil {
		t.Fatal(err)
	}
	for _, messageType := range []string{"send", "reply", "forward"} {
		t.Run(messageType, func(t *testing.T) {
			res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
				To: []string{"blocked@external.test"}, Subject: messageType, Body: "x",
			}, messageType, "", nil, nil)
			if res != nil || oerr == nil || oerr.Code != "recipient_suppressed" {
				t.Fatalf("result/error = %+v/%+v, want recipient_suppressed", res, oerr)
			}
		})
	}
}

func TestSendWorker_SuppressionAddedAfterAcceptPreventsProviderIO(t *testing.T) {
	api, store, outbox, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "suppafterqueue")

	// Accept + queue while the recipient is clean.
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"victim@external.test"}, Subject: "queued then suppressed", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}
	if res.Status != "accepted" {
		t.Fatalf("Status = %q, want accepted", res.Status)
	}

	// Suppression lands between accept and the worker run (e.g. a bounce or a
	// manual add) — case-varied to exercise normalization.
	if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "Victim@External.TEST", "opted out", "unsubscribe", nil); err != nil {
		t.Fatalf("AddAgentSuppression: %v", err)
	}

	deliverer := &countingDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "must-not-happen"}}
	worker := outboundsend.NewSendWorker(
		agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()), deliverer)
	if err := worker.Work(ctx, workerJob(res.MessageID, 1)); err == nil {
		t.Fatal("suppressed send must cancel the job (non-nil error)")
	}

	if deliverer.calls != 0 {
		t.Fatalf("provider Deliver called %d times, want zero", deliverer.calls)
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
		t.Errorf("delivery_status = %q, want failed (durable terminal failure)", deliveryStatus)
	}
	if !strings.Contains(detail, "recipient_suppressed") || !strings.Contains(detail, "victim@external.test") {
		t.Errorf("delivery_detail = %q, want recipient_suppressed naming the address", detail)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailFailed); n != 1 {
		t.Errorf("email.failed events = %d, want 1", n)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailSent); n != 0 {
		t.Errorf("email.sent events = %d, want 0", n)
	}

	// A sibling agent under the same account remains allowed for the same
	// recipient; the exact message.agent_id must reach the worker guard.
	domain := ag.RegisteredDomain
	sibling, err := store.CreateAgent(ctx, "sibling@"+domain, domain, "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent(sibling): %v", err)
	}
	siblingResult, siblingErr := api.DeliverOutbound(ctx, user, sibling, outbound.SendRequest{
		To: []string{"victim@external.test"}, Subject: "sibling allowed", Body: "x",
	}, "send", "", nil, nil)
	if siblingErr != nil {
		t.Fatalf("sibling DeliverOutbound: %+v", siblingErr)
	}
	allowedDeliverer := &countingDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "ses-sibling", SentAs: "own_address"}}
	allowedWorker := outboundsend.NewSendWorker(agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()), allowedDeliverer)
	if err := allowedWorker.Work(ctx, workerJob(siblingResult.MessageID, 1)); err != nil {
		t.Fatalf("sibling worker: %v", err)
	}
	if allowedDeliverer.calls != 1 {
		t.Fatalf("sibling provider calls = %d, want 1", allowedDeliverer.calls)
	}
}
