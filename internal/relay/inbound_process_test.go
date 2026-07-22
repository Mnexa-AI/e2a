package relay_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/emailauth"
	"github.com/tokencanopy/e2a/internal/eventpayload"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/jobs"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/relay"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
	"github.com/tokencanopy/e2a/internal/ws"
)

// TestInbound_ProcessIntake_RealPath exercises the ACTUAL async worker Processor
// (relay.Server.ProcessIntake → processInbound with the MarkInboundIntakeProcessedTx
// hook), not a fake: an accepted intake is processed into a messages row with the
// intake flipped to 'processed' atomically, and a re-drive is a no-op that creates no
// second message (the status-guard idempotency crux).
func TestInboundAsyncLifecycleProcessIntakeRealPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const domain = "process-real.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-process-real")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}

	cfg := &config.Config{SMTP: config.SMTPConfig{Domain: domain}, Env: "development"}
	server := relay.NewServer(cfg, store, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	server.SetAuthenticationChecker(func(context.Context, net.IP, string, string, []byte, emailauth.AuthorIdentity) *emailauth.Authentication {
		domain := "sender.test"
		aligned := true
		return &emailauth.Authentication{
			SPF:   emailauth.SPFResult{Status: emailauth.StatusPass, Domain: &domain, Aligned: &aligned},
			DKIM:  []emailauth.DKIMResult{},
			DMARC: emailauth.DMARCResult{Status: emailauth.StatusPass, Domain: &domain, AlignedBy: []emailauth.AlignmentMechanism{emailauth.AlignedBySPF}},
		}
	})

	// Plant an accepted intake row (as acceptInbound would).
	raw := []byte("From: alice@sender.test\r\nTo: " + agentEmail + "\r\nMessage-ID: <rp1@sender.test>\r\nSubject: real path\r\n\r\nbody")
	id := identity.NewInboundIntakeID()
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, e := store.InsertInboundIntakeTx(ctx, tx, id, agentEmail, "alice@sender.test", "mx.sender.test", "1.2.3.4", "<rp1@sender.test>", "hash-rp", raw)
		if e != nil {
			return e
		}
		return store.StampInboundIntakeJobIDTx(ctx, tx, id, 4242)
	}); err != nil {
		t.Fatalf("plant intake: %v", err)
	}

	it, err := store.LoadInboundIntake(ctx, id)
	if err != nil || it == nil {
		t.Fatalf("load intake: %v", err)
	}
	if err := server.ProcessIntake(ctx, it); err != nil {
		t.Fatalf("ProcessIntake: %v", err)
	}

	// messages row created + intake flipped processed + linked, atomically.
	var subject, status string
	if err := pool.QueryRow(ctx, `SELECT subject, status FROM messages WHERE agent_id=$1 AND direction='inbound'`, agentEmail).Scan(&subject, &status); err != nil {
		t.Fatalf("messages row: %v", err)
	}
	if subject != "real path" {
		t.Errorf("subject = %q, want %q", subject, "real path")
	}
	var intakeStatus string
	var fk *string
	if err := pool.QueryRow(ctx, `SELECT status, message_fk FROM inbound_intake WHERE id=$1`, id).Scan(&intakeStatus, &fk); err != nil {
		t.Fatalf("read intake: %v", err)
	}
	if intakeStatus != identity.IntakeStatusProcessed || fk == nil {
		t.Fatalf("intake status=%q fk=%v; want processed + linked", intakeStatus, fk)
	}

	ensureRiverSchema(t, pool)
	transitions, err := messagelifecycle.NewStore(pool).ListForMessage(ctx, *fk, agentEmail)
	if err != nil {
		t.Fatalf("list lifecycle: %v", err)
	}
	if len(transitions) != 3 {
		t.Fatalf("lifecycle count = %d, want acceptance + authentication + queue: %+v", len(transitions), transitions)
	}
	wantReasons := map[messagelifecycle.ReasonCode]bool{
		messagelifecycle.ReasonAcceptanceInboundSMTP:   true,
		messagelifecycle.ReasonAuthenticationDMARCPass: true,
		messagelifecycle.ReasonQueueInboundProcessing:  true,
	}
	for _, transition := range transitions {
		if !wantReasons[transition.ReasonCode] {
			t.Fatalf("unexpected lifecycle reason %q", transition.ReasonCode)
		}
		delete(wantReasons, transition.ReasonCode)
		if transition.Reconstructed {
			t.Fatalf("new inbound transition reconstructed=true: %+v", transition)
		}
	}
	if len(wantReasons) != 0 {
		t.Fatalf("missing lifecycle reasons: %v", wantReasons)
	}
	var eventEnvelope []byte
	if err := pool.QueryRow(ctx, `SELECT envelope FROM webhook_events WHERE message_id=$1 AND type='email.received'`, *fk).Scan(&eventEnvelope); err != nil {
		t.Fatalf("load email.received envelope: %v", err)
	}
	var envelope struct {
		Data eventpayload.EmailReceivedData `json:"data"`
	}
	if err := json.Unmarshal(eventEnvelope, &envelope); err != nil {
		t.Fatalf("decode email.received envelope: %v", err)
	}
	if !reflect.DeepEqual(envelope.Data.LifecycleTransitions, transitions) {
		t.Fatalf("event lifecycle differs from REST lifecycle\nevent: %+v\nrest:  %+v", envelope.Data.LifecycleTransitions, transitions)
	}
	queueFound := false
	for _, transition := range transitions {
		if transition.ReasonCode == messagelifecycle.ReasonQueueInboundProcessing {
			queueFound = true
			if transition.CorrelationIDs["job_id"] != "4242" {
				t.Fatalf("queue job correlation = %q, want 4242", transition.CorrelationIDs["job_id"])
			}
		}
	}
	if !queueFound {
		t.Fatal("queue lifecycle transition missing")
	}

	// Re-drive: ProcessIntake on the now-processed intake. The hook's status guard
	// (WHERE status='accepted' → 0 rows) aborts the persist tx with
	// ErrIntakeAlreadyProcessed, so NO second messages row is created.
	it2, _ := store.LoadInboundIntake(ctx, id)
	rerr := server.ProcessIntake(ctx, it2)
	if !errors.Is(rerr, identity.ErrIntakeAlreadyProcessed) {
		t.Fatalf("re-drive should return ErrIntakeAlreadyProcessed, got %v", rerr)
	}
	var msgCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1`, agentEmail).Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 1 {
		t.Fatalf("re-drive must not create a second message, got %d", msgCount)
	}
	var lifecycleCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions WHERE message_id=$1`, *fk).Scan(&lifecycleCount); err != nil {
		t.Fatalf("count lifecycle after redrive: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE message_id=$1 AND type='email.received'`, *fk).Scan(&eventCount); err != nil {
		t.Fatalf("count event after redrive: %v", err)
	}
	if lifecycleCount != 3 || eventCount != 1 {
		t.Fatalf("redrive minted observations: lifecycle=%d event=%d", lifecycleCount, eventCount)
	}
	var replayedEnvelope []byte
	if err := pool.QueryRow(ctx, `SELECT envelope FROM webhook_events WHERE message_id=$1 AND type='email.received'`, *fk).Scan(&replayedEnvelope); err != nil {
		t.Fatalf("reload event envelope: %v", err)
	}
	if !reflect.DeepEqual(replayedEnvelope, eventEnvelope) {
		t.Fatal("redrive changed the stored event envelope")
	}
}

func TestInboundLifecycleDMARCStatusMappings(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const domain = "lifecycle-auth.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-lifecycle-auth")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}

	server := relay.NewServer(&config.Config{SMTP: config.SMTPConfig{Domain: domain}, Env: "development"}, store, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	var status emailauth.Status
	var expectedAuth emailauth.Authentication
	server.SetAuthenticationChecker(func(context.Context, net.IP, string, string, []byte, emailauth.AuthorIdentity) *emailauth.Authentication {
		return &expectedAuth
	})

	cases := []struct {
		status emailauth.Status
		reason messagelifecycle.ReasonCode
	}{
		{emailauth.StatusPass, messagelifecycle.ReasonAuthenticationDMARCPass},
		{emailauth.StatusFail, messagelifecycle.ReasonAuthenticationDMARCFail},
		{emailauth.StatusNone, messagelifecycle.ReasonAuthenticationDMARCNone},
		{emailauth.StatusTempError, messagelifecycle.ReasonAuthenticationDMARCTemporaryError},
		{emailauth.StatusPermError, messagelifecycle.ReasonAuthenticationDMARCPermanentError},
	}
	for index, tc := range cases {
		status = tc.status
		authDomain := "sender.test"
		expectedAuth = emailauth.Authentication{
			SPF:   emailauth.SPFResult{Status: emailauth.StatusNone, Detail: "no SPF identity"},
			DKIM:  []emailauth.DKIMResult{},
			DMARC: emailauth.DMARCResult{Status: status, Domain: &authDomain, AlignedBy: []emailauth.AlignmentMechanism{}, Detail: "fixture " + string(status)},
		}
		intakeID := identity.NewInboundIntakeID()
		raw := []byte(fmt.Sprintf("From: alice@sender.test\r\nTo: %s\r\nMessage-ID: <auth-%d@sender.test>\r\nSubject: auth %s\r\n\r\nbody", agentEmail, index, tc.status))
		if err := store.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := store.InsertInboundIntakeTx(ctx, tx, intakeID, agentEmail, "alice@sender.test", "mx.sender.test", "1.2.3.4", fmt.Sprintf("<auth-%d@sender.test>", index), fmt.Sprintf("hash-%d", index), raw)
			if err != nil {
				return err
			}
			return store.StampInboundIntakeJobIDTx(ctx, tx, intakeID, int64(5000+index))
		}); err != nil {
			t.Fatalf("plant %s intake: %v", tc.status, err)
		}
		intake, err := store.LoadInboundIntake(ctx, intakeID)
		if err != nil || intake == nil {
			t.Fatalf("load %s intake: %v", tc.status, err)
		}
		if err := server.ProcessIntake(ctx, intake); err != nil {
			t.Fatalf("process %s intake: %v", tc.status, err)
		}
		var messageID string
		if err := pool.QueryRow(ctx, `SELECT message_fk FROM inbound_intake WHERE id=$1`, intakeID).Scan(&messageID); err != nil {
			t.Fatalf("message fk for %s: %v", tc.status, err)
		}
		var reason messagelifecycle.ReasonCode
		var evidenceJSON []byte
		if err := pool.QueryRow(ctx, `SELECT reason_code, evidence FROM message_lifecycle_transitions WHERE message_id=$1 AND stage='authentication'`, messageID).Scan(&reason, &evidenceJSON); err != nil {
			t.Fatalf("authentication lifecycle for %s: %v", tc.status, err)
		}
		if reason != tc.reason {
			t.Fatalf("DMARC %s reason = %q, want %q", tc.status, reason, tc.reason)
		}
		var evidence map[string]json.RawMessage
		if err := json.Unmarshal(evidenceJSON, &evidence); err != nil {
			t.Fatalf("decode evidence for %s: %v", tc.status, err)
		}
		var gotAuth emailauth.Authentication
		if err := json.Unmarshal(evidence["authentication"], &gotAuth); err != nil {
			t.Fatalf("decode auth evidence for %s: %v", tc.status, err)
		}
		if gotAuth.DMARC.Status != tc.status {
			t.Fatalf("auth evidence DMARC status = %q, want %q", gotAuth.DMARC.Status, tc.status)
		}
		if !reflect.DeepEqual(gotAuth, expectedAuth) {
			t.Fatalf("auth evidence for %s changed\ngot:  %+v\nwant: %+v", tc.status, gotAuth, expectedAuth)
		}
	}
}

func TestInboundAsyncLifecycleAppendFailureRollsBackMessageIntakeAndEvent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	const domain = "lifecycle-rollback.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-lifecycle-rollback")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}
	server := relay.NewServer(&config.Config{SMTP: config.SMTPConfig{Domain: domain}, Env: "development"}, store, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	server.SetAuthenticationChecker(func(context.Context, net.IP, string, string, []byte, emailauth.AuthorIdentity) *emailauth.Authentication {
		return &emailauth.Authentication{SPF: emailauth.SPFResult{Status: emailauth.StatusNone}, DKIM: []emailauth.DKIMResult{}, DMARC: emailauth.DMARCResult{Status: emailauth.StatusNone, AlignedBy: []emailauth.AlignmentMechanism{}}}
	})
	intakeID := identity.NewInboundIntakeID()
	raw := []byte("From: alice@sender.test\r\nTo: " + agentEmail + "\r\nMessage-ID: <rollback@sender.test>\r\nSubject: rollback\r\n\r\nbody")
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := store.InsertInboundIntakeTx(ctx, tx, intakeID, agentEmail, "alice@sender.test", "mx.sender.test", "1.2.3.4", "<rollback@sender.test>", "hash-rollback", raw)
		if err != nil {
			return err
		}
		return store.StampInboundIntakeJobIDTx(ctx, tx, intakeID, 7777)
	}); err != nil {
		t.Fatalf("plant intake: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_fail_inbound_lifecycle() RETURNS trigger AS $f$
		BEGIN RAISE EXCEPTION 'forced lifecycle append failure'; END; $f$ LANGUAGE plpgsql;
		CREATE TRIGGER test_fail_inbound_lifecycle BEFORE INSERT ON message_lifecycle_transitions
		FOR EACH ROW EXECUTE FUNCTION test_fail_inbound_lifecycle();`); err != nil {
		t.Fatalf("create lifecycle failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_fail_inbound_lifecycle ON message_lifecycle_transitions; DROP FUNCTION IF EXISTS test_fail_inbound_lifecycle();`)
	})
	intake, err := store.LoadInboundIntake(ctx, intakeID)
	if err != nil || intake == nil {
		t.Fatalf("load intake: %v", err)
	}
	var lifecycleBaseline int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions`).Scan(&lifecycleBaseline); err != nil {
		t.Fatalf("lifecycle baseline: %v", err)
	}
	if err := server.ProcessIntake(ctx, intake); err == nil {
		t.Fatal("ProcessIntake succeeded despite forced lifecycle append failure")
	}
	var status string
	var messageFK *string
	if err := pool.QueryRow(ctx, `SELECT status, message_fk FROM inbound_intake WHERE id=$1`, intakeID).Scan(&status, &messageFK); err != nil {
		t.Fatalf("read intake after rollback: %v", err)
	}
	if status != identity.IntakeStatusAccepted || messageFK != nil {
		t.Fatalf("intake changed despite rollback: status=%q message_fk=%v", status, messageFK)
	}
	for name, query := range map[string]string{
		"messages": `SELECT count(*) FROM messages WHERE agent_id=$1`,
		"events":   `SELECT count(*) FROM webhook_events WHERE agent_id=$1`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query, agentEmail).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s count after rollback = %d, want 0", name, count)
		}
	}
	var lifecycleAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions`).Scan(&lifecycleAfter); err != nil {
		t.Fatalf("lifecycle count after rollback: %v", err)
	}
	if lifecycleAfter != lifecycleBaseline {
		t.Fatalf("lifecycle rows survived rollback: before=%d after=%d", lifecycleBaseline, lifecycleAfter)
	}
}

func TestInboundAsyncLifecycleBoundsAdversarialMetadata(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	const domain = "async-lifecycle-bounds.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-async-lifecycle-bounds")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}
	authentication := adversarialLifecycleAuthentication()
	server := relay.NewServer(&config.Config{SMTP: config.SMTPConfig{Domain: domain}, Env: "development"}, store, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	server.SetAuthenticationChecker(func(context.Context, net.IP, string, string, []byte, emailauth.AuthorIdentity) *emailauth.Authentication {
		return authentication
	})
	intakeID := identity.NewInboundIntakeID()
	raw := []byte("From: alice@sender.test\r\nTo: " + agentEmail + "\r\n" + oversizedFoldedMessageIDHeader() + "\r\nSubject: async bounded\r\n\r\nbody")
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := store.InsertInboundIntakeTx(ctx, tx, intakeID, agentEmail, "alice@sender.test", "mx.sender.test", "1.2.3.4", strings.Repeat("m", 4096), "hash-async-bounded", raw)
		if err != nil {
			return err
		}
		return store.StampInboundIntakeJobIDTx(ctx, tx, intakeID, 8800)
	}); err != nil {
		t.Fatalf("plant intake: %v", err)
	}
	intake, err := store.LoadInboundIntake(ctx, intakeID)
	if err != nil || intake == nil {
		t.Fatalf("load intake: %v", err)
	}
	if err := server.ProcessIntake(ctx, intake); err != nil {
		t.Fatalf("ProcessIntake rejected bounded remote metadata: %v", err)
	}
	var status, messageID string
	if err := pool.QueryRow(ctx, `SELECT status, message_fk FROM inbound_intake WHERE id=$1`, intakeID).Scan(&status, &messageID); err != nil {
		t.Fatalf("read processed intake: %v", err)
	}
	if status != identity.IntakeStatusProcessed || messageID == "" {
		t.Fatalf("intake status=%q message=%q, want processed + message", status, messageID)
	}
	assertAdversarialInboundLifecycle(t, pool, messageID, agentEmail, 3, len(authentication.DKIM))
}

func adversarialLifecycleAuthentication() *emailauth.Authentication {
	tooLong := strings.Repeat("x", 3*1024)
	aligned := false
	dkim := make([]emailauth.DKIMResult, 0, 41)
	dkim = append(dkim, emailauth.DKIMResult{Status: emailauth.StatusFail, Domain: &tooLong, Selector: &tooLong, Aligned: &aligned, Detail: tooLong})
	for i := 0; i < 40; i++ {
		domain := fmt.Sprintf("sender-%d.test", i)
		selector := fmt.Sprintf("selector-%d", i)
		dkim = append(dkim, emailauth.DKIMResult{Status: emailauth.StatusFail, Domain: &domain, Selector: &selector, Aligned: &aligned, Detail: strings.Repeat("d", 2*1024)})
	}
	return &emailauth.Authentication{
		SPF:   emailauth.SPFResult{Status: emailauth.StatusFail, Domain: &tooLong, Aligned: &aligned, Detail: tooLong},
		DKIM:  dkim,
		DMARC: emailauth.DMARCResult{Status: emailauth.StatusTempError, Domain: &tooLong, AlignedBy: []emailauth.AlignmentMechanism{}, Detail: tooLong},
	}
}

func oversizedFoldedMessageIDHeader() string {
	var header strings.Builder
	header.WriteString("Message-ID: <")
	for i := 0; i < 6; i++ {
		if i > 0 {
			header.WriteString("\r\n\t")
		}
		header.WriteString(strings.Repeat(string(rune('a'+i)), 800))
	}
	header.WriteString("@sender.test>")
	return header.String()
}

func assertAdversarialInboundLifecycle(t *testing.T, pool *pgxpool.Pool, messageID, agentEmail string, wantCount, originalDKIMCount int) {
	t.Helper()
	ctx := context.Background()
	ensureRiverSchema(t, pool)
	transitions, err := messagelifecycle.NewStore(pool).ListForMessage(ctx, messageID, agentEmail)
	if err != nil {
		t.Fatalf("list lifecycle: %v", err)
	}
	if len(transitions) != wantCount {
		t.Fatalf("lifecycle count=%d, want %d: %+v", len(transitions), wantCount, transitions)
	}
	var authenticationTransition *messagelifecycle.MessageLifecycleTransition
	for i := range transitions {
		transition := &transitions[i]
		if _, ok := transition.CorrelationIDs["email_message_id"]; ok {
			t.Fatalf("unsafe Message-ID correlation survived: %+v", transition.CorrelationIDs)
		}
		if transition.Stage == messagelifecycle.StageAuthentication {
			authenticationTransition = transition
		}
	}
	if authenticationTransition == nil {
		t.Fatal("authentication lifecycle transition missing")
	}
	if authenticationTransition.ReasonCode != messagelifecycle.ReasonAuthenticationDMARCTemporaryError {
		t.Fatalf("authentication reason=%q, want temperror", authenticationTransition.ReasonCode)
	}
	evidenceJSON, err := json.Marshal(authenticationTransition.Evidence)
	if err != nil {
		t.Fatalf("marshal lifecycle evidence: %v", err)
	}
	if len(evidenceJSON) > 16*1024 {
		t.Fatalf("lifecycle evidence size=%d, exceeds 16KiB", len(evidenceJSON))
	}
	authMap := authenticationTransition.Evidence["authentication"].(map[string]any)
	if authMap["dmarc"].(map[string]any)["status"] != string(emailauth.StatusTempError) {
		t.Fatalf("bounded evidence changed DMARC verdict: %#v", authMap["dmarc"])
	}
	if got := len(authMap["dkim"].([]any)); got >= originalDKIMCount {
		t.Fatalf("bounded evidence kept all %d DKIM entries", got)
	}
	var eventEnvelope []byte
	if err := pool.QueryRow(ctx, `SELECT envelope FROM webhook_events WHERE message_id=$1 AND type='email.received'`, messageID).Scan(&eventEnvelope); err != nil {
		t.Fatalf("load email.received: %v", err)
	}
	var envelope struct {
		Data eventpayload.EmailReceivedData `json:"data"`
	}
	if err := json.Unmarshal(eventEnvelope, &envelope); err != nil {
		t.Fatalf("decode email.received: %v", err)
	}
	if !reflect.DeepEqual(envelope.Data.LifecycleTransitions, transitions) {
		t.Fatalf("event lifecycle differs from store\nevent: %+v\nstore: %+v", envelope.Data.LifecycleTransitions, transitions)
	}
	if envelope.Data.Authentication == nil || len(envelope.Data.Authentication.DKIM) != originalDKIMCount {
		t.Fatalf("main event authentication was bounded; DKIM=%d want %d", len(envelope.Data.Authentication.DKIM), originalDKIMCount)
	}
}

func ensureRiverSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := jobs.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate River schema for lifecycle read: %v", err)
	}
}
