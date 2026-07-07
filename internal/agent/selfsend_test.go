package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

	res, oerr := api.DeliverOutbound(ctx, user, ag, outbound.SendRequest{
		To: []string{ag.EmailAddress()}, Subject: "note to self", Body: "remember to refill coffee",
	}, "send", "", nil, nil)
	if oerr != nil {
		t.Fatalf("DeliverOutbound: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if res.Method != "loopback" {
		t.Errorf("method=%q want loopback", res.Method)
	}
	if !strings.HasPrefix(res.MessageID, "<") || !strings.Contains(res.MessageID, "loopback.") {
		t.Errorf("message_id=%q should look like an RFC 5322 Message-ID with loopback host", res.MessageID)
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

	var sender, recipient string
	pool.QueryRow(ctx,
		`SELECT sender, recipient FROM messages WHERE agent_id=$1 AND direction='inbound' AND subject='note to self'`,
		ag.ID).Scan(&sender, &recipient)
	if sender != ag.EmailAddress() || recipient != ag.EmailAddress() {
		t.Errorf("self-note row sender=%q recipient=%q; both must be the agent's own address", sender, recipient)
	}

	var method string
	pool.QueryRow(ctx,
		`SELECT method FROM messages WHERE agent_id=$1 AND direction='outbound' AND subject='note to self'`,
		ag.ID).Scan(&method)
	if method != "loopback" {
		t.Errorf("outbound method=%q want loopback", method)
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
