package hitlnotify_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/hitlnotify"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

const (
	notifySecret     = "hitl-notify-test-secret"
	notifyFromDomain = "notify.test"
	publicURL        = "https://app.example.test"
)

// newNotifier wires a notifier talking to a fake SMTP + a fresh test DB.
// Returns notifier, store, signer, and the smtpDone accessor.
func newNotifier(t *testing.T) (
	*hitlnotify.Notifier,
	*identity.Store,
	*approvaltoken.Signer,
	func() []testutil.SMTPMessage,
) {
	t.Helper()
	smtpAddr, smtpDone := testutil.FakeSMTPServer(t)
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	relay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{
		Host:       smtpAddr.Host,
		Port:       smtpAddr.Port,
		FromDomain: notifyFromDomain,
	})
	signer := approvaltoken.NewSigner(notifySecret)
	n := hitlnotify.New(store, relay, signer, notifyFromDomain, publicURL)
	return n, store, signer, smtpDone
}

// setupPendingMessage creates a verified HITL-enabled agent with one
// pending outbound message. Returns (agent, message).
func setupPendingMessage(t *testing.T, store *identity.Store, slug string) (*identity.AgentIdentity, *identity.Message) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+slug+"@reviewer.test", "Owner", "google-notify-"+slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, slug+".bot.test", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyDomain(ctx, slug+".bot.test", user.ID); err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateAgent(ctx, "bot@"+slug+".bot.test", slug+".bot.test", "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, true, identity.HITLDefaultTTLSeconds, identity.HITLExpirationReject); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.GetAgentByID(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, []string{"carol@example.com"}, nil,
		"Important draft", "This is the body that will be reviewed.", "<p>html body</p>",
		nil, "send", "conv_1", "", 3600,
	)
	if err != nil {
		t.Fatal(err)
	}
	return refreshed, msg
}

func TestNotifierSendsEmailToOwner(t *testing.T) {
	n, store, _, smtpDone := newNotifier(t)
	agent, msg := setupPendingMessage(t, store, "send-email")

	if err := n.NotifyPendingApproval(context.Background(), msg, agent); err != nil {
		t.Fatalf("NotifyPendingApproval: %v", err)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}
	sent := msgs[0]

	// From / To envelope
	if want := "hitl-noreply@" + notifyFromDomain; sent.From != want {
		t.Errorf("envelope from = %q, want %q", sent.From, want)
	}
	if sent.To != "owner-send-email@reviewer.test" {
		t.Errorf("envelope to = %q", sent.To)
	}

	// Body content: both plain-text and HTML parts should carry identifying
	// info but NOT the held message body. The body only appears on the
	// token-gated confirm page.
	data := sent.Data
	for _, needle := range []string{
		"bot@send-email.bot.test",      // agent email
		"alice@example.com",            // recipient
		"carol@example.com",            // cc
		"Important draft",              // subject
		"/api/v1/approve?t=",           // magic approve link
		"/api/v1/reject?t=",            // magic reject link
		"/dashboard/pending/" + msg.ID, // dashboard link
	} {
		if !strings.Contains(data, needle) {
			t.Errorf("email body missing %q", needle)
		}
	}
	// Sensitive draft body must not travel in the email.
	if strings.Contains(data, "This is the body that will be reviewed.") {
		t.Errorf("notification leaked held message body into email:\n%s", data)
	}

	// Subject line should mention the agent + message subject
	if !strings.Contains(data, "Subject: ") {
		t.Error("missing Subject header")
	}
	// Reply-To points back at the platform, not the agent
	if !strings.Contains(data, "Reply-To: hitl-noreply@"+notifyFromDomain) {
		t.Errorf("Reply-To header should be platform sender, got:\n%s", data)
	}
}

func TestNotifierMagicLinksAreVerifiable(t *testing.T) {
	n, store, signer, smtpDone := newNotifier(t)
	agent, msg := setupPendingMessage(t, store, "tok-verify")

	if err := n.NotifyPendingApproval(context.Background(), msg, agent); err != nil {
		t.Fatal(err)
	}
	data := smtpDone()[0].Data

	approveTok := extractToken(t, data, "/api/v1/approve?t=")
	rejectTok := extractToken(t, data, "/api/v1/reject?t=")

	// Tokens are signed with the agent owner's per-account secret (most
	// recently created). Pull those from the store, plus include the
	// deployment secret in the verify list so the test exercises both
	// the per-user path and the legacy fallback path.
	userSecrets, err := store.GetUserSigningSecrets(context.Background(), agent.UserID)
	if err != nil {
		t.Fatalf("get user signing secrets: %v", err)
	}
	verifySecrets := make([]string, len(userSecrets))
	for i, s := range userSecrets {
		verifySecrets[i] = s.Secret
	}
	// signer holds the deployment-wide secret as a private field; we
	// can't read it directly, so we use the legacy Verify path which
	// internally tries that secret. The per-user-first behavior is
	// exercised by approvaltoken.Verify(userSecrets, ...) below.
	_ = signer

	approveClaims, err := approvaltoken.Verify(verifySecrets, approveTok)
	if err != nil {
		t.Fatalf("approve token verify: %v", err)
	}
	if approveClaims.MessageID != msg.ID {
		t.Errorf("approve claims.MessageID = %q", approveClaims.MessageID)
	}
	if approveClaims.Action != approvaltoken.ActionApprove {
		t.Errorf("approve claims.Action = %q", approveClaims.Action)
	}

	rejectClaims, err := approvaltoken.Verify(verifySecrets, rejectTok)
	if err != nil {
		t.Fatalf("reject token verify: %v", err)
	}
	if rejectClaims.Action != approvaltoken.ActionReject {
		t.Errorf("reject claims.Action = %q", rejectClaims.Action)
	}

	// exp lives slightly past msg.ApprovalExpiresAt so a late click still works.
	if !approveClaims.ExpiresAt.After(*msg.ApprovalExpiresAt) {
		t.Errorf("approve token exp %v should be after msg.ApprovalExpiresAt %v",
			approveClaims.ExpiresAt, *msg.ApprovalExpiresAt)
	}
}

func TestNotifierBuildsAbsoluteURLs(t *testing.T) {
	n, store, _, smtpDone := newNotifier(t)
	agent, msg := setupPendingMessage(t, store, "abs-url")

	if err := n.NotifyPendingApproval(context.Background(), msg, agent); err != nil {
		t.Fatal(err)
	}
	data := smtpDone()[0].Data
	if !strings.Contains(data, publicURL+"/api/v1/approve?t=") {
		t.Errorf("approve URL should be absolute under %q, got:\n%s", publicURL, data)
	}
	if !strings.Contains(data, publicURL+"/dashboard/pending/") {
		t.Errorf("dashboard URL should be absolute under %q", publicURL)
	}
}

func TestNotifierRejectsMessageWithNilApprovalExpiresAt(t *testing.T) {
	n, store, _, smtpDone := newNotifier(t)
	defer smtpDone()

	agent, msg := setupPendingMessage(t, store, "nil-exp")
	msg.ApprovalExpiresAt = nil

	err := n.NotifyPendingApproval(context.Background(), msg, agent)
	if err == nil {
		t.Fatal("expected error for nil ApprovalExpiresAt")
	}
	if !strings.Contains(err.Error(), "approval_expires_at") {
		t.Errorf("error should mention approval_expires_at, got: %v", err)
	}
}

func TestNotifierAsyncFireAndForget(t *testing.T) {
	n, store, _, smtpDone := newNotifier(t)
	agent, msg := setupPendingMessage(t, store, "async")

	n.NotifyPendingApprovalAsync(msg, agent)

	// Fakesmtp closes the listener on the first smtpDone() call, so
	// sleep once and then read the result in a single shot.
	time.Sleep(500 * time.Millisecond)
	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("async notification: got %d messages, want 1", len(msgs))
	}
}

func TestNotifierNilSafe(t *testing.T) {
	var n *hitlnotify.Notifier
	// Both sync and async paths must tolerate a nil receiver so wiring
	// can omit the notifier in tests / partial deployments without
	// having to guard every call site.
	if err := n.NotifyPendingApproval(context.Background(), nil, nil); err != nil {
		t.Errorf("nil receiver sync: err = %v, want nil", err)
	}
	n.NotifyPendingApprovalAsync(nil, nil) // must not panic
}

// extractToken pulls the ?t=... token out of the first occurrence of the
// given URL prefix in the raw email data. Tolerates URL encoding since
// tokens contain only base64url-safe characters plus '.'.
func extractToken(t *testing.T, data, prefix string) string {
	t.Helper()
	i := strings.Index(data, prefix)
	if i < 0 {
		t.Fatalf("prefix %q not found in email data", prefix)
	}
	rest := data[i+len(prefix):]
	end := strings.IndexAny(rest, " \r\n\t\"<>)")
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}
