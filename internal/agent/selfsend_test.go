package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/messagelifecycle"
	"github.com/tokencanopy/e2a/internal/outbound"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
	wsnotify "github.com/tokencanopy/e2a/internal/ws"
)

type captureHub struct{ payload []byte }

func (h *captureHub) IsConnected(string) bool { return true }
func (h *captureHub) Send(_ string, payload []byte) bool {
	h.payload = append([]byte(nil), payload...)
	return true
}

// TestSelfSend_DetectionEdgeCases: case-insensitive, whitespace-
// trimmed, single-address requirement. Mixed/external recipients must
// fall through to SMTP (covered indirectly — TestSendEmailViaSMTP
// already exercises the non-loopback path).
func TestSelfSend_DetectionEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		to     []string
		cc     []string
		want   bool
		reason string
	}{
		{"exact match", []string{"bot@x.com"}, nil, true, ""},
		{"case-insensitive local", []string{"BOT@x.com"}, nil, true, "ASCII case-insensitive"},
		{"case-insensitive domain", []string{"bot@X.COM"}, nil, true, "domain comparison is case-insensitive"},
		{"whitespace trimmed", []string{"  bot@x.com  "}, nil, true, "trim should normalize"},
		{"different address", []string{"other@x.com"}, nil, false, "not self"},
		{"self plus external in To", []string{"bot@x.com", "other@x.com"}, nil, false, "external recipient → SMTP"},
		{"self plus cc", []string{"bot@x.com"}, []string{"cc@x.com"}, false, "cc → SMTP"},
		{"empty to", []string{}, nil, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := outbound.SendRequest{To: c.to, CC: c.cc}
			got := agent.IsSelfSendForTest(req, "bot@x.com")
			if got != c.want {
				t.Errorf("isSelfSend(%v, cc=%v) = %v, want %v (%s)", c.to, c.cc, got, c.want, c.reason)
			}
		})
	}
}

// setupCoreAPI builds an *agent.API wired to a real test DB so tests can drive
// the extracted outbound core (DeliverOutbound) directly. The legacy
// POST /api/v1/send route these self-send tests once rode through was removed
// in the v1 cutover; the loopback core it called lives on (and is what /v1's
// sendMessage now invokes), so it still needs DB-backed coverage here. The
// pure HTTP-shape assertions moved to internal/httpapi; the loopback delivery
// + MIME-persistence behavior below has no /v1 unit home (httpapi tests use
// fakes), so it stays at the core level.
func setupCoreAPI(t *testing.T) (*agent.API, *identity.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(),
		"e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	return api, store, pool
}

// selfAgent provisions a verified domain + agent owned by a fresh user and
// returns the user and the loaded agent identity ready for DeliverOutbound.
func selfAgent(t *testing.T, store *identity.Store, label string) (*identity.User, *identity.AgentIdentity) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "self-"+label+"@example.com", "Owner", "google-self-"+label)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	domain := "self" + label + ".example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "local", user.ID); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	ag, err := store.GetAgentByEmail(ctx, "bot@"+domain)
	if err != nil {
		t.Fatalf("GetAgentByEmail: %v", err)
	}
	return user, ag
}

// TestSelfSend_HappyPath: an agent sending to its own address short-circuits
// to the loopback path (no SMTP) and lands BOTH an outbound and an inbound row
// tagged to the agent, with the outbound row persisting method="loopback".
func TestSelfSend_HappyPath(t *testing.T) {
	api, store, pool := setupCoreAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "owner")
	hub := &captureHub{}
	api.SetWebSocketHub(hub)
	const replyTo = "Support <support@example.com>"

	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{ag.EmailAddress()}, Subject: "note to self", Body: "remember to refill coffee", ReplyTo: replyTo,
		Attachments: []outbound.Attachment{{Filename: "note.txt", ContentType: "text/plain", Data: "aGVsbG8="}},
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if res.Method != "loopback" {
		t.Errorf("method=%q want loopback", res.Method)
	}
	if !strings.HasPrefix(res.MessageID, "msg_") {
		t.Errorf("message_id=%q should be the GET-able outbound resource id", res.MessageID)
	}
	if res.ProviderMessageID != "" {
		t.Errorf("provider_message_id=%q want empty for providerless loopback delivery", res.ProviderMessageID)
	}

	var outboundCount, inboundCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='outbound' AND subject='note to self'`,
		ag.ID).Scan(&outboundCount)
	pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='note to self'`,
		ag.ID).Scan(&inboundCount)
	if outboundCount != 1 {
		t.Errorf("outbound rows=%d want 1", outboundCount)
	}
	if inboundCount != 1 {
		t.Errorf("inbound rows=%d want 1", inboundCount)
	}

	var inboundID, sender, recipient string
	pool.QueryRow(ctx,
		`SELECT id, sender, recipient FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='note to self'`,
		ag.ID).Scan(&inboundID, &sender, &recipient)
	if sender != "support@example.com" || recipient != ag.EmailAddress() {
		t.Errorf("self-note row sender=%q recipient=%q; want Reply-To and agent address", sender, recipient)
	}

	var method, deliveryStatus, providerID string
	var outboundRaw []byte
	pool.QueryRow(ctx,
		`SELECT method, COALESCE(delivery_status,''), provider_message_id, raw_message
		   FROM messages WHERE id=$1`, res.MessageID).Scan(&method, &deliveryStatus, &providerID, &outboundRaw)
	if method != "loopback" {
		t.Errorf("outbound method=%q want loopback", method)
	}
	if deliveryStatus != "sent" {
		t.Errorf("outbound delivery_status=%q want sent", deliveryStatus)
	}
	if len(outboundRaw) == 0 || !strings.Contains(string(outboundRaw), "remember to refill coffee") {
		t.Errorf("outbound sent copy must retain readable MIME; raw=%q", outboundRaw)
	}

	var inboundProviderID string
	if err := pool.QueryRow(ctx,
		`SELECT email_message_id FROM messages
		  WHERE agent_id=$1 AND direction='inbound' AND subject='note to self'`, ag.ID,
	).Scan(&inboundProviderID); err != nil {
		t.Fatalf("fetch inbound Message-ID: %v", err)
	}
	if providerID == "" || inboundProviderID != providerID {
		t.Errorf("loopback Message-ID mismatch: outbound=%q inbound=%q", providerID, inboundProviderID)
	}
	if !strings.Contains(string(outboundRaw), "Message-ID: "+providerID) {
		t.Errorf("loopback MIME missing its synthetic Message-ID header: %q", outboundRaw)
	}

	rows, err := pool.Query(ctx,
		`SELECT type, message_id FROM webhook_events
		  WHERE message_id IN (
		    SELECT id FROM messages WHERE agent_id=$1 AND subject='note to self'
		  ) ORDER BY type`, ag.ID)
	if err != nil {
		t.Fatalf("list loopback events: %v", err)
	}
	defer rows.Close()
	var eventTypes []string
	for rows.Next() {
		var eventType, messageID string
		if err := rows.Scan(&eventType, &messageID); err != nil {
			t.Fatalf("scan loopback event: %v", err)
		}
		eventTypes = append(eventTypes, eventType)
	}
	if got, want := strings.Join(eventTypes, ","), "email.received,email.sent"; got != want {
		t.Errorf("loopback events=%q want %q", got, want)
	}
	var sentEventHasProviderID bool
	if err := pool.QueryRow(ctx,
		`SELECT envelope->'data' ? 'provider_message_id'
		   FROM webhook_events WHERE message_id=$1 AND type='email.sent'`, res.MessageID,
	).Scan(&sentEventHasProviderID); err != nil {
		t.Fatalf("read loopback email.sent payload: %v", err)
	}
	if sentEventHasProviderID {
		t.Error("providerless loopback email.sent must omit provider_message_id")
	}
	assertLoopbackLifecycleParity(t, pool, res.MessageID, inboundID)
	var durableEnvelope []byte
	if err := pool.QueryRow(ctx,
		`SELECT envelope FROM webhook_events WHERE message_id=$1 AND type='email.received'`, inboundID,
	).Scan(&durableEnvelope); err != nil {
		t.Fatalf("read durable received envelope: %v", err)
	}
	var durable, live map[string]any
	if err := json.Unmarshal(durableEnvelope, &durable); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(hub.payload, &live); err != nil {
		t.Fatalf("live WebSocket payload: %v (%q)", err, hub.payload)
	}
	if !reflect.DeepEqual(live, durable) {
		t.Errorf("live WebSocket envelope drifted from durable webhook event\nlive=%v\ndurable=%v", live, durable)
	}
	listed, err := store.GetMessagesByAgent(ctx, identity.MessageListFilter{
		AgentID: ag.ID, Status: "all", Direction: "inbound", Limit: 10,
	})
	if err != nil || len(listed) != 1 {
		t.Fatalf("list reconnect message: len=%d err=%v", len(listed), err)
	}
	if listed[0].Method != "loopback" {
		t.Fatalf("reconnect row method = %q, want durable loopback marker", listed[0].Method)
	}
	var reconnect map[string]any
	if err := json.Unmarshal(wsnotify.NotificationForMessage(ctx, store, &listed[0]), &reconnect); err != nil {
		t.Fatal(err)
	}
	reconnectData := reconnect["data"].(map[string]any)
	if !reflect.DeepEqual(reconnect, durable) {
		t.Errorf("reconnect envelope drifted from durable event\nreconnect=%v\ndurable=%v", reconnect, durable)
	}
	if reconnect["id"] != live["id"] || reconnectData["header_from"] != ag.EmailAddress() || reconnectData["authentication"] != nil {
		t.Errorf("reconnect identity drift: %v", reconnect)
	}
	data := live["data"].(map[string]any)
	if data["header_from"] != ag.EmailAddress() || data["envelope_from"] != nil || data["authentication"] != nil {
		t.Errorf("received identities = header_from:%v envelope_from:%v authentication:%v", data["header_from"], data["envelope_from"], data["authentication"])
	}
	if attachments, ok := data["attachments"].([]any); !ok || len(attachments) != 1 {
		t.Errorf("live/durable/reconnect envelope lost attachment metadata: %v", data["attachments"])
	}
}

// The idempotency completion hook must share the same transaction as both
// loopback message rows and their lifecycle events. A completion failure is a
// failure to commit the local delivery, not a partial Sent/Inbox pair.
func TestSelfSend_IdempotencyCompletionFailureRollsBackDeliveryLifecycle(t *testing.T) {
	api, store, pool := setupCoreAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "idemrollback")

	_, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{ag.EmailAddress()}, Subject: "atomic rollback", Body: "must not persist",
	}, "send", "", nil, func(_ context.Context, tx pgx.Tx, result *agent.OutboundResult) error {
		if !strings.HasPrefix(result.MessageID, "msg_") {
			t.Errorf("idempotency completion message_id=%q want msg_ resource id", result.MessageID)
		}
		if result.Method != "loopback" || result.Status != "" {
			t.Errorf("idempotency completion result=%+v want terminal loopback", result)
		}
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM messages WHERE agent_id=$1 AND subject='atomic rollback'`, ag.ID,
		).Scan(&count); err != nil {
			t.Fatalf("count transaction-local loopback rows: %v", err)
		}
		if count != 2 {
			t.Errorf("transaction-local loopback rows=%d want 2", count)
		}
		return errors.New("inject idempotency completion failure")
	})
	if oerr == nil || oerr.Code != "internal_error" {
		t.Fatalf("DeliverOutbound error=%v want internal_error", oerr)
	}

	var messages, events, lifecycle int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE agent_id=$1 AND subject='atomic rollback'`, ag.ID,
	).Scan(&messages); err != nil {
		t.Fatalf("count rolled-back messages: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_events WHERE agent_id=$1 AND envelope->'data'->>'subject'='atomic rollback'`, ag.ID,
	).Scan(&events); err != nil {
		t.Fatalf("count rolled-back events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions WHERE message_id IN (SELECT id FROM messages WHERE agent_id=$1 AND subject='atomic rollback')`, ag.ID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || events != 0 || lifecycle != 0 {
		t.Fatalf("partial loopback commit: messages=%d events=%d lifecycle=%d; want all zero", messages, events, lifecycle)
	}
}

func TestSelfSend_LifecycleFailureRollsBackDelivery(t *testing.T) {
	api, store, pool := setupCoreAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "lifecyclerollback")
	_, err := pool.Exec(ctx, `CREATE OR REPLACE FUNCTION test_fail_loopback_lifecycle() RETURNS trigger AS $f$ BEGIN IF NEW.reason_code='submission.local_loopback_accepted' THEN RAISE EXCEPTION 'forced loopback lifecycle failure'; END IF; RETURN NEW; END; $f$ LANGUAGE plpgsql; CREATE TRIGGER test_fail_loopback_lifecycle BEFORE INSERT ON message_lifecycle_transitions FOR EACH ROW EXECUTE FUNCTION test_fail_loopback_lifecycle();`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS test_fail_loopback_lifecycle ON message_lifecycle_transitions; DROP FUNCTION IF EXISTS test_fail_loopback_lifecycle();`)
	})
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{To: []string{ag.EmailAddress()}, Subject: "loopback lifecycle rollback", Body: "body"}, "send", "", nil, nil)
	if res != nil || oerr == nil {
		t.Fatalf("result=%+v error=%+v want failure", res, oerr)
	}
	var messages, events, lifecycle int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE agent_id=$1 AND subject='loopback lifecycle rollback'`, ag.ID).Scan(&messages)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE agent_id=$1 AND envelope->'data'->>'subject'='loopback lifecycle rollback'`, ag.ID).Scan(&events)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM message_lifecycle_transitions WHERE message_id IN (SELECT id FROM messages WHERE agent_id=$1 AND subject='loopback lifecycle rollback')`, ag.ID).Scan(&lifecycle)
	if messages != 0 || events != 0 || lifecycle != 0 {
		t.Fatalf("partial loopback messages=%d events=%d lifecycle=%d", messages, events, lifecycle)
	}
}

func assertLoopbackLifecycleParity(t *testing.T, pool *pgxpool.Pool, outboundID, inboundID string) {
	t.Helper()
	ctx := context.Background()
	read := func(messageID string) []messagelifecycle.MessageLifecycleTransition {
		rows, err := pool.Query(ctx, `SELECT id, message_id, direction, COALESCE(recipient,''), stage, outcome, reason_code, retryable, evidence, correlation_ids, occurred_at, reconstructed FROM message_lifecycle_transitions WHERE message_id=$1 ORDER BY occurred_at,id`, messageID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []messagelifecycle.MessageLifecycleTransition
		for rows.Next() {
			var tr messagelifecycle.MessageLifecycleTransition
			var evidence, correlation []byte
			if err := rows.Scan(&tr.ID, &tr.MessageID, &tr.Direction, &tr.Recipient, &tr.Stage, &tr.Outcome, &tr.ReasonCode, &tr.Retryable, &evidence, &correlation, &tr.OccurredAt, &tr.Reconstructed); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal(evidence, &tr.Evidence)
			_ = json.Unmarshal(correlation, &tr.CorrelationIDs)
			tr.OccurredAt = tr.OccurredAt.UTC()
			got = append(got, tr)
		}
		return got
	}
	outboundTransitions, inboundTransitions := read(outboundID), read(inboundID)
	if len(outboundTransitions) != 2 || outboundTransitions[0].ReasonCode != messagelifecycle.ReasonAcceptanceOutboundAPI || outboundTransitions[1].ReasonCode != messagelifecycle.ReasonSubmissionLocalLoopbackAccepted {
		t.Fatalf("outbound loopback lifecycle=%+v", outboundTransitions)
	}
	if len(inboundTransitions) != 1 || inboundTransitions[0].ReasonCode != messagelifecycle.ReasonAcceptanceLocalLoopback {
		t.Fatalf("inbound loopback lifecycle=%+v", inboundTransitions)
	}
	for _, tc := range []struct {
		id, event string
		want      []messagelifecycle.MessageLifecycleTransition
	}{{outboundID, "email.sent", outboundTransitions}, {inboundID, "email.received", inboundTransitions}} {
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT envelope->'data'->'lifecycle_transitions' FROM webhook_events WHERE message_id=$1 AND type=$2`, tc.id, tc.event).Scan(&raw); err != nil {
			t.Fatalf("%s lifecycle payload: %v", tc.event, err)
		}
		var got []messagelifecycle.MessageLifecycleTransition
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s lifecycle differs\nevent=%+v\nstore=%+v", tc.event, got, tc.want)
		}
	}
}

func assertApprovedLoopbackLifecycleParity(t *testing.T, pool *pgxpool.Pool, outboundID, inboundID string) {
	t.Helper()
	ctx := context.Background()
	for _, tc := range []struct {
		id, event string
		reasons   []string
	}{
		{outboundID, "email.sent", []string{"review.approved", "submission.local_loopback_accepted"}},
		{inboundID, "email.received", []string{"acceptance.local_loopback"}},
	} {
		var raw []byte
		if err := pool.QueryRow(ctx, `SELECT envelope->'data'->'lifecycle_transitions' FROM webhook_events WHERE message_id=$1 AND type=$2`, tc.id, tc.event).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var eventTransitions []messagelifecycle.MessageLifecycleTransition
		if err := json.Unmarshal(raw, &eventTransitions); err != nil {
			t.Fatal(err)
		}
		if len(eventTransitions) != len(tc.reasons) {
			t.Fatalf("%s transitions=%+v", tc.event, eventTransitions)
		}
		for i, reason := range tc.reasons {
			if string(eventTransitions[i].ReasonCode) != reason {
				t.Fatalf("%s reason[%d]=%s want %s", tc.event, i, eventTransitions[i].ReasonCode, reason)
			}
			var storedID string
			if err := pool.QueryRow(ctx, `SELECT id FROM message_lifecycle_transitions WHERE message_id=$1 AND reason_code=$2`, tc.id, reason).Scan(&storedID); err != nil {
				t.Fatal(err)
			}
			if eventTransitions[i].ID != storedID {
				t.Fatalf("%s transition id=%s store=%s", tc.event, eventTransitions[i].ID, storedID)
			}
		}
	}
}

// TestSelfSend_PreservesAttachmentsInMIME: a self-send with an attachment must
// persist the attachment in the inbound row's raw_message so the SDK's MIME
// parser finds it on read. Guards a past regression where the loopback path
// stored only req.Body and silently dropped req.Attachments. Also asserts the
// synthetic Received: trace header (RFC 5321 §4.4) is present.
func TestSelfSend_PreservesAttachmentsInMIME(t *testing.T) {
	api, store, pool := setupCoreAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "attach")

	// "aGVsbG8gZmlsZQ==" is base64 of "hello file".
	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To:      []string{ag.EmailAddress()},
		Subject: "note with file",
		Body:    "see attached",
		Attachments: []outbound.Attachment{{
			Filename: "note.txt", ContentType: "text/plain", Data: "aGVsbG8gZmlsZQ==",
		}},
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: status=%d msg=%s", oerr.Status, oerr.Msg)
	}
	if res.Method != "loopback" {
		t.Errorf("method=%q want loopback", res.Method)
	}

	var rawBytes []byte
	if err := pool.QueryRow(ctx,
		`SELECT raw_message FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='note with file'`,
		ag.ID).Scan(&rawBytes); err != nil {
		t.Fatalf("fetch inbound row: %v", err)
	}
	raw := string(rawBytes)

	if !strings.HasPrefix(raw, "Received: by ") {
		t.Errorf("inbound raw_message should start with synthetic Received: header; got:\n%.200s", raw)
	}
	if !strings.Contains(raw, "with loopback id ") {
		t.Errorf("Received: header should carry 'with loopback id' keyword; got:\n%.300s", raw)
	}
	if !strings.Contains(raw, "Content-Type: multipart/") {
		t.Errorf("raw_message should be multipart MIME (attachments present); got:\n%.500s", raw)
	}
	if !strings.Contains(raw, `filename="note.txt"`) {
		t.Errorf("attachment filename header missing from MIME; got:\n%.800s", raw)
	}
	if !strings.Contains(raw, "aGVsbG8gZmlsZQ==") {
		t.Errorf("attachment base64 payload missing from MIME body; got:\n%.800s", raw)
	}
	if !strings.Contains(raw, "From: "+ag.EmailAddress()) {
		t.Errorf("From: header should be the agent's own address; got:\n%.300s", raw)
	}
	if !strings.Contains(raw, "To: "+ag.EmailAddress()) {
		t.Errorf("To: header should be the agent's own address; got:\n%.300s", raw)
	}
}

// TestSelfSend_NoAttachmentsUsesSinglePart: the attachment-less loopback path
// uses the simpler single-part composer (no multipart wrapper), keeping the
// stored MIME small for the dominant note-to-self case.
func TestSelfSend_NoAttachmentsUsesSinglePart(t *testing.T) {
	api, store, pool := setupCoreAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "plain")

	if _, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{ag.EmailAddress()}, Subject: "plain", Body: "hi me",
	}, "send", "", nil, nil); oerr != nil {
		t.Fatalf("DeliverOutbound: status=%d msg=%s", oerr.Status, oerr.Msg)
	}

	var rawBytes []byte
	pool.QueryRow(ctx,
		`SELECT raw_message FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='plain'`,
		ag.ID).Scan(&rawBytes)
	raw := string(rawBytes)

	if !strings.HasPrefix(raw, "Received: by ") {
		t.Errorf("Received: header missing on plain self-send; got:\n%.200s", raw)
	}
	if strings.Contains(raw, "Content-Type: multipart/") {
		t.Errorf("plain self-send should NOT use multipart MIME; got:\n%.400s", raw)
	}
	if !strings.Contains(raw, "hi me") {
		t.Errorf("body text missing from raw_message; got:\n%.400s", raw)
	}
}
