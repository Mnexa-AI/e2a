package agent_test

// End-to-end (real store adapter + real DB) coverage of the SendWorker's
// pre-provider suppression guard: a suppression added AFTER a send was
// durably accepted + queued — approval or direct — still prevents provider
// I/O; the row records a durable terminal failure and email.failed fires.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/delivery"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/outboundsend"
	"github.com/tokencanopy/e2a/internal/sendramp"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

type blockingRampGate struct {
	entered  chan struct{}
	resume   chan struct{}
	mu       sync.Mutex
	released []string
}

func (g *blockingRampGate) Reserve(context.Context, outboundsend.RampRequest) (outboundsend.RampDecision, error) {
	close(g.entered)
	<-g.resume
	return outboundsend.RampDecision{Allowed: true}, nil
}
func (*blockingRampGate) Confirm(context.Context, string) error { return nil }
func (g *blockingRampGate) Release(_ context.Context, messageID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.released = append(g.released, messageID)
	return nil
}
func (*blockingRampGate) Resolve(context.Context, string) error { return nil }

func (g *blockingRampGate) releasedIDs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.released...)
}

// countingDeliverer records provider submits so the guard can assert zero I/O.
type countingDeliverer struct {
	calls int
	out   outboundsend.DeliverOutcome
}

type failOnceSuppressionStore struct {
	outboundsend.Store
	failed bool
}

func (s *failOnceSuppressionStore) SuppressedRecipients(ctx context.Context, userID, agentID string, recipients []string) ([]string, error) {
	if !s.failed {
		s.failed = true
		return nil, errors.New("transient suppression lookup failure")
	}
	return s.Store.SuppressedRecipients(ctx, userID, agentID, recipients)
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
				To: []string{"Blocked Recipient <blocked@external.test>"}, Subject: messageType, Body: "x",
			}, messageType, "", nil, nil)
			if res != nil || oerr == nil || oerr.Code != "recipient_suppressed" {
				t.Fatalf("result/error = %+v/%+v, want recipient_suppressed", res, oerr)
			}
		})
	}
}

func TestSendWorker_SuppressionAddedAfterAcceptPreventsProviderIO(t *testing.T) {
	api, store, outbox, _, pool := setupAsyncAPIWithPool(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "suppafterqueue")

	// Accept + queue while the recipient is clean.
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"victim@external.test", "Victim@External.TEST", "victim@external.test"}, Subject: "queued then suppressed", Body: "x",
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
	if !strings.Contains(detail, "/v1/account/suppressions/{address}") ||
		!strings.Contains(detail, "/v1/agents/"+ag.ID+"/suppressions/{address}?confirm=DELETE") {
		t.Errorf("worker remediation = %q, want account and exact-agent endpoints", detail)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailFailed); n != 1 {
		t.Errorf("email.failed events = %d, want 1", n)
	}
	if n := countEvents(t, store, ag.UserID, webhookpub.EventEmailSent); n != 0 {
		t.Errorf("email.sent events = %d, want 0", n)
	}
	rows := lifecycleRows(t, pool, res.MessageID)
	var blocked, cancelled int
	for _, tr := range rows {
		if tr.ReasonCode == messagelifecycle.ReasonSuppressionRecipientBlocked {
			blocked++
			if tr.Recipient != "victim@external.test" {
				t.Fatalf("blocked recipient=%q", tr.Recipient)
			}
			if len(tr.Evidence) != 0 {
				t.Fatalf("blocked evidence=%v, want none: scope and source were not observed", tr.Evidence)
			}
		}
		if tr.ReasonCode == messagelifecycle.ReasonSubmissionCancelled {
			cancelled++
		}
	}
	if blocked != 1 || cancelled != 1 {
		t.Fatalf("send-time suppression lifecycle blocked=%d cancelled=%d", blocked, cancelled)
	}
	event := eventLifecycle(t, pool, res.MessageID, webhookpub.EventEmailFailed)
	if len(event) != 1 || event[0].ReasonCode != messagelifecycle.ReasonSubmissionCancelled {
		t.Fatalf("email.failed lifecycle=%+v", event)
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

func TestSendWorker_SuppressionFallbackReplaysExactObservation(t *testing.T) {
	api, store, outbox, _, pool := setupAsyncAPIWithPool(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "suppfallback")
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"Victim@External.TEST", "victim@external.test"}, Subject: "suppression fallback", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}
	if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "Victim@External.TEST", "opted out", "unsubscribe", nil); err != nil {
		t.Fatal(err)
	}
	install := `CREATE FUNCTION test_fail_suppression_fallback() RETURNS trigger AS $f$ BEGIN IF NEW.reason_code='submission.cancelled' THEN RAISE EXCEPTION 'forced lifecycle failure'; END IF; RETURN NEW; END; $f$ LANGUAGE plpgsql; CREATE TRIGGER test_fail_suppression_fallback BEFORE INSERT ON message_lifecycle_transitions FOR EACH ROW EXECUTE FUNCTION test_fail_suppression_fallback();`
	uninstall := `DROP TRIGGER IF EXISTS test_fail_suppression_fallback ON message_lifecycle_transitions; DROP FUNCTION IF EXISTS test_fail_suppression_fallback();`
	if _, err := pool.Exec(ctx, install); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), uninstall) })

	worker := outboundsend.NewSendWorker(agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()), &countingDeliverer{})
	if err := worker.Work(ctx, workerJob(res.MessageID, 7)); err == nil {
		t.Fatal("suppressed send must cancel")
	}
	var occurredAt *time.Time
	var attempt *int
	var recipients []string
	if err := pool.QueryRow(ctx, `SELECT delivery_failure_occurred_at,delivery_failure_attempt,delivery_failure_blocked_recipients FROM messages WHERE id=$1`, res.MessageID).Scan(&occurredAt, &attempt, &recipients); err != nil {
		t.Fatal(err)
	}
	if occurredAt == nil || attempt == nil || *attempt != 7 {
		t.Fatalf("fallback occurred_at=%v attempt=%v, want exact attempt 7 observation", occurredAt, attempt)
	}
	if len(recipients) != 1 || recipients[0] != "victim@external.test" {
		t.Fatalf("fallback blocked recipients=%v, want normalized unique recipient", recipients)
	}
	if rows := lifecycleRows(t, pool, res.MessageID); len(rows) != 2 { // acceptance + queue only
		t.Fatalf("failed primary tx leaked lifecycle rows: %+v", rows)
	}

	if _, err := pool.Exec(ctx, uninstall); err != nil {
		t.Fatal(err)
	}
	adapter := agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker())
	if err := outboundsend.NewTerminalReconcileWorker(pool, adapter).Work(ctx, &river.Job[outboundsend.TerminalReconcileArgs]{}); err != nil {
		t.Fatal(err)
	}
	var cancelled, blocked *messagelifecycle.MessageLifecycleTransition
	rows := lifecycleRows(t, pool, res.MessageID)
	for i := range rows {
		tr := rows[i]
		switch tr.ReasonCode {
		case messagelifecycle.ReasonSubmissionCancelled:
			cancelled = &tr
		case messagelifecycle.ReasonSuppressionRecipientBlocked:
			blocked = &tr
		}
	}
	if cancelled == nil || blocked == nil {
		t.Fatalf("reconciled cancelled=%+v blocked=%+v", cancelled, blocked)
	}
	if !cancelled.OccurredAt.Equal(occurredAt.UTC()) || !blocked.OccurredAt.Equal(occurredAt.UTC()) {
		t.Fatalf("replayed timestamps cancelled=%s blocked=%s want %s", cancelled.OccurredAt, blocked.OccurredAt, occurredAt.UTC())
	}
	var dedupeKey string
	if err := pool.QueryRow(ctx, `SELECT dedupe_key FROM message_lifecycle_transitions WHERE id=$1`, cancelled.ID).Scan(&dedupeKey); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dedupeKey, ":attempt:7:") || len(blocked.Evidence) != 0 {
		t.Fatalf("replayed cancellation dedupe_key=%q suppression evidence=%v", dedupeKey, blocked.Evidence)
	}
}

func TestSendWorker_SuppressionAddedDuringRampReservePreventsProviderIO(t *testing.T) {
	api, store, outbox, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "suppduringramp")
	if err := store.SetSendingStatus(ctx, ag.RegisteredDomain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus: %v", err)
	}
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"late@external.test"}, Subject: "ramp race", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}

	gate := &blockingRampGate{entered: make(chan struct{}), resume: make(chan struct{})}
	deliverer := &countingDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "must-not-happen"}}
	worker := outboundsend.NewSendWorker(agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker()), deliverer, gate)
	done := make(chan error, 1)
	go func() { done <- worker.Work(ctx, workerJob(res.MessageID, 1)) }()
	<-gate.entered
	if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "late@external.test", "opted out", "unsubscribe", nil); err != nil {
		t.Fatal(err)
	}
	close(gate.resume)
	if err := <-done; err == nil {
		t.Fatal("suppression created during ramp reservation must cancel the send")
	}
	if deliverer.calls != 0 {
		t.Fatalf("provider calls = %d, want zero", deliverer.calls)
	}
	if got := gate.releasedIDs(); len(got) != 1 || got[0] != res.MessageID {
		t.Fatalf("released reservations = %v, want [%s]", got, res.MessageID)
	}
	var status, detail string
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT delivery_status, COALESCE(delivery_detail,'') FROM messages WHERE id=$1`, res.MessageID).Scan(&status, &detail)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(detail, "recipient_suppressed") {
		t.Fatalf("status/detail = %q/%q, want failed recipient_suppressed", status, detail)
	}
}

func TestSendWorker_TransientSuppressionFailureReusesRealRampReservation(t *testing.T) {
	api, store, outbox, _, pool := setupAsyncAPIWithPool(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "rampretryreal")
	if err := store.SetSendingStatus(ctx, ag.RegisteredDomain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus: %v", err)
	}
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{"recipient@external.test"}, Subject: "retry after suppression lookup", Body: "x",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: %+v", oerr)
	}

	baseStore := agent.NewOutboundSendStore(store, outbox, usage.NewNoopUsageTracker())
	failingStore := &failOnceSuppressionStore{Store: baseStore}
	day := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ramp := agent.NewOutboundRampGate(sendramp.NewStore(pool), sendramp.NewSchedule(50, 100, 2), true, func() time.Time { return day })
	deliverer := &countingDeliverer{out: outboundsend.DeliverOutcome{ProviderMessageID: "ses-after-retry", SentAs: "own_address"}}
	worker := outboundsend.NewSendWorker(failingStore, deliverer, ramp)

	if err := worker.Work(ctx, workerJobWithID(res.MessageID, 999, 1)); err == nil {
		t.Fatal("first worker attempt must return the injected transient error")
	}
	var firstState string
	if err := pool.QueryRow(ctx, `SELECT state FROM sending_ramp_reservations WHERE message_id=$1`, res.MessageID).Scan(&firstState); err != nil {
		t.Fatalf("read first reservation: %v", err)
	}
	if firstState != "reserved" {
		t.Fatalf("reservation after transient error = %q, want reserved", firstState)
	}
	if deliverer.calls != 0 {
		t.Fatalf("provider calls after transient error = %d, want zero", deliverer.calls)
	}

	if err := worker.Work(ctx, workerJobWithID(res.MessageID, 999, 2)); err != nil {
		t.Fatalf("retry worker attempt: %v", err)
	}
	var finalState, status string
	if err := pool.QueryRow(ctx, `SELECT state FROM sending_ramp_reservations WHERE message_id=$1`, res.MessageID).Scan(&finalState); err != nil {
		t.Fatalf("read final reservation: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT delivery_status FROM messages WHERE id=$1`, res.MessageID).Scan(&status); err != nil {
		t.Fatalf("read final message: %v", err)
	}
	if finalState != "confirmed" || status != "sent" || deliverer.calls != 1 {
		t.Fatalf("final reservation/status/provider calls = %q/%q/%d, want confirmed/sent/1", finalState, status, deliverer.calls)
	}
}

func TestAccountSuppressionFromBounceBlocksEveryAgentSend(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, first := selfAgent(t, store, "accountbounce")
	second, err := store.CreateAgent(ctx, "second@"+first.RegisteredDomain, first.RegisteredDomain, "", "", "local", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent(second): %v", err)
	}

	seed, err := store.CreateOutboundMessage(ctx, first.ID, []string{"bounced@external.test"}, nil, nil,
		"seed provider bounce", "send", "smtp", "ses-account-bounce", "", []byte("raw"))
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	if err := store.MarkMessageSent(ctx, seed.ID, "own_address", []string{"bounced@external.test"}, nil, nil); err != nil {
		t.Fatalf("MarkMessageSent: %v", err)
	}
	bounce, err := delivery.ParseSESNotification([]byte(`{
		"eventType":"Bounce",
		"mail":{"messageId":"ses-account-bounce"},
		"bounce":{"bounceType":"Permanent","bouncedRecipients":[{
			"emailAddress":"BOUNCED@EXTERNAL.TEST","diagnosticCode":"550 no such user"
		}]}
	}`))
	if err != nil {
		t.Fatalf("ParseSESNotification: %v", err)
	}
	if err := delivery.NewConsumer(store, nil).Process(ctx, bounce); err != nil {
		t.Fatalf("Process bounce: %v", err)
	}

	for _, ag := range []*identity.AgentIdentity{first, second} {
		res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
			To: []string{"Bounced Recipient <BOUNCED@EXTERNAL.TEST>"}, Subject: "must be blocked", Body: "x",
		}, "send", "", nil, nil)
		if res != nil || oerr == nil || oerr.Code != "recipient_suppressed" {
			t.Fatalf("agent %s result/error = %+v/%+v, want account-wide recipient_suppressed", ag.ID, res, oerr)
		}
	}
}
