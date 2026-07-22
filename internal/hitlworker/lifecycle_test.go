package hitlworker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/jobs"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

func TestWorkerExpiredLoopbackLifecycleParityAndRedrive(t *testing.T) {
	w, store, pool, smtpDone := setupWorker(t)
	ctx := context.Background()
	agent := prepareAgent(t, store, "expired-loopback-lifecycle", identity.HITLExpirationApprove)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID, []string{agent.EmailAddress()}, nil, nil, "expired lifecycle loopback", "body", "", nil, "send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	w.RunOnce(ctx)
	if got := smtpDone(); len(got) != 0 {
		t.Fatalf("loopback touched SMTP: %d", len(got))
	}
	var inboundID string
	if err := pool.QueryRow(ctx, `SELECT id FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='expired lifecycle loopback'`, agent.ID).Scan(&inboundID); err != nil {
		t.Fatal(err)
	}
	outboundLifecycle, err := messagelifecycle.NewStore(pool).ListForMessage(ctx, msg.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	inboundLifecycle, err := messagelifecycle.NewStore(pool).ListForMessage(ctx, inboundID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertLifecycleReasonSet(t, outboundLifecycle, []messagelifecycle.ReasonCode{messagelifecycle.ReasonAcceptanceOutboundAPI, messagelifecycle.ReasonReviewHoldCreated, messagelifecycle.ReasonReviewExpiredApproved, messagelifecycle.ReasonSubmissionLocalLoopbackAccepted})
	assertLifecycleReasonSet(t, inboundLifecycle, []messagelifecycle.ReasonCode{messagelifecycle.ReasonAcceptanceLocalLoopback})
	assertWorkerEventTransition(t, pool, msg.ID, webhookpub.EventEmailSent, messagelifecycle.ReasonSubmissionLocalLoopbackAccepted, outboundLifecycle)
	assertWorkerEventTransition(t, pool, inboundID, webhookpub.EventEmailReceived, messagelifecycle.ReasonAcceptanceLocalLoopback, inboundLifecycle)

	sentEnvelope, err := store.GetEventEnvelope(ctx, msg.ID, webhookpub.EventEmailSent)
	if err != nil {
		t.Fatal(err)
	}
	receivedEnvelope, err := store.GetEventEnvelope(ctx, inboundID, webhookpub.EventEmailReceived)
	if err != nil {
		t.Fatal(err)
	}
	w.RunOnce(ctx)
	outboundAfter, err := messagelifecycle.NewStore(pool).ListForMessage(ctx, msg.ID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	inboundAfter, err := messagelifecycle.NewStore(pool).ListForMessage(ctx, inboundID, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outboundAfter, outboundLifecycle) || !reflect.DeepEqual(inboundAfter, inboundLifecycle) {
		t.Fatalf("redrive changed lifecycle\nbefore=%+v/%+v\nafter=%+v/%+v", outboundLifecycle, inboundLifecycle, outboundAfter, inboundAfter)
	}
	sentAfter, _ := store.GetEventEnvelope(ctx, msg.ID, webhookpub.EventEmailSent)
	receivedAfter, _ := store.GetEventEnvelope(ctx, inboundID, webhookpub.EventEmailReceived)
	if !bytes.Equal(sentAfter, sentEnvelope) || !bytes.Equal(receivedAfter, receivedEnvelope) {
		t.Fatal("duplicate sweep changed stored loopback envelopes")
	}
}

func TestWorkerExpiredLoopbackLifecycleFailureRollsBack(t *testing.T) {
	w, store, pool, _ := setupWorker(t)
	ctx := context.Background()
	agent := prepareAgent(t, store, "expired-loopback-rollback", identity.HITLExpirationApprove)
	msg, err := store.CreatePendingOutboundMessage(ctx, agent.ID, []string{agent.EmailAddress()}, nil, nil, "expired lifecycle rollback", "body", "", nil, "send", "", "", "", 60)
	if err != nil {
		t.Fatal(err)
	}
	backdateExpiry(t, pool, msg.ID)
	var lifecycleBaseline int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions`).Scan(&lifecycleBaseline); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE OR REPLACE FUNCTION test_fail_expired_loopback_lifecycle() RETURNS trigger AS $f$ BEGIN IF NEW.stage IN ('review','submission') THEN RAISE EXCEPTION 'forced expired loopback lifecycle failure'; END IF; RETURN NEW; END; $f$ LANGUAGE plpgsql; CREATE TRIGGER test_fail_expired_loopback_lifecycle BEFORE INSERT ON message_lifecycle_transitions FOR EACH ROW EXECUTE FUNCTION test_fail_expired_loopback_lifecycle();`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_fail_expired_loopback_lifecycle ON message_lifecycle_transitions; DROP FUNCTION IF EXISTS test_fail_expired_loopback_lifecycle();`)
	})
	w.RunOnce(ctx)
	var status string
	var sendJobID *int64
	if err := pool.QueryRow(ctx, `SELECT status,send_job_id FROM messages WHERE id=$1`, msg.ID).Scan(&status, &sendJobID); err != nil {
		t.Fatal(err)
	}
	var copies, events, lifecycleAfter int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='expired lifecycle rollback'`, agent.ID).Scan(&copies)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE message_id=$1 OR message_id IN (SELECT id FROM messages WHERE agent_id=$2 AND subject='expired lifecycle rollback')`, msg.ID, agent.ID).Scan(&events)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions`).Scan(&lifecycleAfter)
	if status != identity.MessageStatusPendingReview || sendJobID != nil || copies != 0 || events != 0 || lifecycleAfter != lifecycleBaseline {
		t.Fatalf("partial expiry rollback status=%s job=%v copies=%d events=%d lifecycle=%d/%d", status, sendJobID, copies, events, lifecycleBaseline, lifecycleAfter)
	}
}

func assertLifecycleReasonSet(t *testing.T, got []messagelifecycle.MessageLifecycleTransition, want []messagelifecycle.ReasonCode) {
	t.Helper()
	counts := map[messagelifecycle.ReasonCode]int{}
	for _, tr := range got {
		counts[tr.ReasonCode]++
	}
	if len(got) != len(want) {
		t.Fatalf("lifecycle=%+v want reasons=%v", got, want)
	}
	for _, reason := range want {
		if counts[reason] != 1 {
			t.Fatalf("reason %s count=%d lifecycle=%+v", reason, counts[reason], got)
		}
	}
}

func assertWorkerEventTransition(t *testing.T, pool *pgxpool.Pool, messageID, eventType string, reason messagelifecycle.ReasonCode, all []messagelifecycle.MessageLifecycleTransition) {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT envelope->'data'->'lifecycle_transitions' FROM webhook_events WHERE message_id=$1 AND type=$2`, messageID, eventType).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var carried []messagelifecycle.MessageLifecycleTransition
	if err := json.Unmarshal(raw, &carried); err != nil {
		t.Fatal(err)
	}
	if len(carried) != 1 || carried[0].ReasonCode != reason {
		t.Fatalf("%s carried=%+v want only %s", eventType, carried, reason)
	}
	for _, tr := range all {
		if tr.ID == carried[0].ID {
			if !reflect.DeepEqual(tr, carried[0]) {
				t.Fatalf("%s carried transition differs from public read", eventType)
			}
			return
		}
	}
	t.Fatalf("%s transition %s absent from public lifecycle read", eventType, carried[0].ID)
}
