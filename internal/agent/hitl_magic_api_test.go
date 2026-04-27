package agent_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/approvaltoken"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/config"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

const magicLinkSecret = "magic-link-test-secret"

// setupMagicLinkAPI mirrors setupAPIWithSMTP but also wires an
// approvaltoken.Signer onto the API. Returns the server, store, signer
// (for issuing tokens in tests), and the fake-SMTP accessor.
func setupMagicLinkAPI(t *testing.T) (
	*httptest.Server,
	*identity.Store,
	*approvaltoken.Signer,
	func() []testutil.SMTPMessage,
) {
	t.Helper()
	smtpAddr, smtpDone := testutil.FakeSMTPServer(t)
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{Host: smtpAddr.Host, Port: smtpAddr.Port})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	signer := approvaltoken.NewSigner(magicLinkSecret)
	api.SetApprovalSigner(signer)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, store, signer, smtpDone
}

// prepareHITLAgent creates a verified agent with HITL enabled. Returns
// agent + userID.
func prepareHITLAgent(t *testing.T, store *identity.Store, slug string) (*identity.AgentIdentity, string) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+slug+"@example.com", "Owner", "google-magic-"+slug)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, slug+".example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain(%s): %v", slug+".example.com", err)
	}
	if err := store.VerifyDomain(ctx, slug+".example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain(%s): %v", slug+".example.com", err)
	}
	a, err := store.CreateAgent(ctx, "bot@"+slug+".example.com", slug+".example.com", "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, true, identity.HITLDefaultTTLSeconds, identity.HITLExpirationReject); err != nil {
		t.Fatal(err)
	}
	return a, user.ID
}

// issuePending creates a pending_approval outbound message on the agent.
func issuePending(t *testing.T, store *identity.Store, agentID string) *identity.Message {
	t.Helper()
	msg, err := store.CreatePendingOutboundMessage(context.Background(), agentID,
		[]string{"alice@example.com"}, nil, nil,
		"Held", "plain body", "<p>html</p>", nil,
		"send", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// postForm submits a form-encoded POST to the given URL and returns the
// response. Mirrors what a browser does when the confirmation page's
// form is submitted.
func postForm(t *testing.T, url string, values map[string]string) *http.Response {
	t.Helper()
	form := make(map[string][]string, len(values))
	for k, v := range values {
		form[k] = []string{v}
	}
	resp, err := http.PostForm(url, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// --- GET confirmation page behavior ---

// TestMagicLinkGETDoesNotExecute is the core security property of the
// split GET/POST design: an email-client URL scanner that previews the
// approve link must not trigger the send.
func TestMagicLinkGETDoesNotExecute(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "get-no-execute")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp, err := http.Get(server.URL + "/api/v1/approve?t=" + url.QueryEscape(tok))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET confirm page: status = %d", resp.StatusCode)
	}

	// No SMTP activity at all — we only rendered a confirmation page.
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Errorf("GET should not have triggered a send; got %d SMTP messages", len(msgs))
	}

	// Row stays pending.
	got, _ := store.GetOutboundMessageForUser(context.Background(), msg.ID, userID)
	if got.Status != identity.MessageStatusPendingApproval {
		t.Errorf("status after GET = %q, want still pending_approval", got.Status)
	}
}

// TestMagicApproveGETRendersConfirmForm verifies the confirmation page
// contains a POST form with the token carried in a hidden field, plus
// the body preview so the reviewer can see what they're about to send.
func TestMagicApproveGETRendersConfirmForm(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, _ := prepareHITLAgent(t, store, "get-renders-form")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp, _ := http.Get(server.URL + "/api/v1/approve?t=" + url.QueryEscape(tok))
	body := readBody(t, resp)

	for _, needle := range []string{
		`method="POST"`,
		`action="/api/v1/approve"`,
		`name="t"`,
		tok, // token echoed into the hidden input
		"alice@example.com", // recipient shown
		"Held",              // subject shown
		"plain body",        // body preview is on the confirm page
		"Approve &amp; send",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("confirm page missing %q", needle)
		}
	}
	// Security headers: no indexing, no frame, no referrer.
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestMagicRejectGETRendersConfirmFormWithReasonField(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, _ := prepareHITLAgent(t, store, "get-reject-form")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionReject, time.Now().Add(1*time.Hour))
	resp, _ := http.Get(server.URL + "/api/v1/reject?t=" + url.QueryEscape(tok))
	body := readBody(t, resp)

	for _, needle := range []string{
		`method="POST"`,
		`action="/api/v1/reject"`,
		`name="t"`,
		`name="reason"`, // optional rejection reason input
		"Reject",
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("reject confirm page missing %q", needle)
		}
	}
}

// --- POST executor behavior ---

func TestMagicApprovePOSTSends(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "post-approve")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST approve: status = %d, body: %s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Approved") {
		t.Errorf("expected 'Approved' in body, got: %s", body)
	}

	msgs := smtpDone()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 SMTP message, got %d", len(msgs))
	}

	got, _ := store.GetOutboundMessageForUser(context.Background(), msg.ID, userID)
	if got.Status != identity.MessageStatusSent {
		t.Errorf("status = %q, want sent", got.Status)
	}
	if got.BodyText != "" {
		t.Errorf("body_text should be scrubbed, got %q", got.BodyText)
	}
}

func TestMagicRejectPOSTWithReason(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "post-reject")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionReject, time.Now().Add(1*time.Hour))
	resp := postForm(t, server.URL+"/api/v1/reject", map[string]string{
		"t":      tok,
		"reason": "not the right tone",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST reject: status = %d", resp.StatusCode)
	}

	if msgs := smtpDone(); len(msgs) != 0 {
		t.Errorf("reject should not call SMTP, got %d", len(msgs))
	}

	got, _ := store.GetOutboundMessageForUser(context.Background(), msg.ID, userID)
	if got.Status != identity.MessageStatusRejected {
		t.Errorf("status = %q, want rejected", got.Status)
	}
	if got.RejectionReason != "not the right tone" {
		t.Errorf("rejection_reason = %q, want 'not the right tone'", got.RejectionReason)
	}
}

func TestMagicRejectPOSTWithoutReasonUsesDefault(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "post-reject-default")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionReject, time.Now().Add(1*time.Hour))
	resp := postForm(t, server.URL+"/api/v1/reject", map[string]string{"t": tok})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	got, _ := store.GetOutboundMessageForUser(context.Background(), msg.ID, userID)
	if got.RejectionReason != "magic-link rejection" {
		t.Errorf("default reason = %q", got.RejectionReason)
	}
}

// --- Error paths (GET rejects + POST rejects consistently) ---

func TestMagicLinkGETMissingToken(t *testing.T) {
	server, _, _, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()

	resp, _ := http.Get(server.URL + "/api/v1/approve")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMagicLinkPOSTMissingToken(t *testing.T) {
	server, _, _, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()

	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMagicLinkGETInvalidToken(t *testing.T) {
	server, _, _, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()

	resp, _ := http.Get(server.URL + "/api/v1/approve?t=gibberish")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMagicLinkPOSTInvalidToken(t *testing.T) {
	server, _, _, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()

	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": "gibberish"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestMagicLinkExpiredToken(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()
	a, _ := prepareHITLAgent(t, store, "magic-expired")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(-1*time.Second))

	// GET and POST both reject expired tokens with 410.
	getResp, _ := http.Get(server.URL + "/api/v1/approve?t=" + url.QueryEscape(tok))
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusGone {
		t.Errorf("GET expired: status = %d, want 410", getResp.StatusCode)
	}
	postResp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusGone {
		t.Errorf("POST expired: status = %d, want 410", postResp.StatusCode)
	}
}

// TestMagicApproveTokenRejectedAtRejectEndpoint confirms a token issued
// for approve cannot be redeemed at /reject. Tested on both GET and
// POST since either is a potential attack surface.
func TestMagicApproveTokenRejectedAtRejectEndpoint(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()
	a, _ := prepareHITLAgent(t, store, "magic-wrong-action")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	getResp, _ := http.Get(server.URL + "/api/v1/reject?t=" + url.QueryEscape(tok))
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET wrong action: status = %d, want 400", getResp.StatusCode)
	}
	postResp := postForm(t, server.URL+"/api/v1/reject", map[string]string{"t": tok})
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusBadRequest {
		t.Errorf("POST wrong action: status = %d, want 400", postResp.StatusCode)
	}
}

func TestMagicRejectTokenRejectedAtApproveEndpoint(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()
	a, _ := prepareHITLAgent(t, store, "magic-cross-action")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionReject, time.Now().Add(1*time.Hour))
	postResp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", postResp.StatusCode)
	}
}

func TestMagicLinkSecondPOSTReturns409(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, _ := prepareHITLAgent(t, store, "magic-second-post")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp1 := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first POST: status = %d", resp1.StatusCode)
	}

	resp2 := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("second POST: status = %d, want 409", resp2.StatusCode)
	}
	body := readBody(t, resp2)
	if !strings.Contains(body, "Already resolved") {
		t.Errorf("expected 'Already resolved' in body, got: %s", body)
	}
}

// TestMagicLinkGETRendersConflictForNonPending covers the UX hole where
// the reviewer opens an approve link for a message that has already
// been resolved (by dashboard, CLI, or the worker). The GET confirm
// page should surface this before any form submission.
func TestMagicLinkGETRendersConflictForNonPending(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "get-conflict")
	msg := issuePending(t, store, a.ID)

	// Resolve via the user-scoped API so the row is no longer pending.
	if _, err := store.RejectPending(context.Background(), msg.ID, userID, "already handled"); err != nil {
		t.Fatal(err)
	}

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp, _ := http.Get(server.URL + "/api/v1/approve?t=" + url.QueryEscape(tok))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

func TestMagicLinkNotFoundForBogusMessageID(t *testing.T) {
	server, _, signer, smtpDone := setupMagicLinkAPI(t)
	defer smtpDone()

	tok, _ := signer.Sign("msg_doesnotexist", approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp, _ := http.Get(server.URL + "/api/v1/approve?t=" + url.QueryEscape(tok))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestMagicLinkDisabledWhenSignerMissing(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	smtpRelay := outbound.NewSMTPRelay(&config.OutboundSMTPConfig{})
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	api := agent.NewAPI(store, sender, smtpRelay, nil, usage.NewNoopUsageTracker(), "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	defer server.Close()

	// Both GET and POST should 404 when the signer is absent.
	getResp, _ := http.Get(server.URL + "/api/v1/approve?t=anything")
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET no signer: status = %d, want 404", getResp.StatusCode)
	}
	postResp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": "anything"})
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusNotFound {
		t.Errorf("POST no signer: status = %d, want 404", postResp.StatusCode)
	}
}

func TestMagicLinkNoCacheAndSecurityHeaders(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, _ := prepareHITLAgent(t, store, "magic-headers")
	msg := issuePending(t, store, a.ID)

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))

	for _, path := range []string{
		server.URL + "/api/v1/approve?t=" + url.QueryEscape(tok),
	} {
		resp, _ := http.Get(path)
		resp.Body.Close()
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", path, resp.Header.Get("Cache-Control"))
		}
		if resp.Header.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, resp.Header.Get("X-Frame-Options"))
		}
		if resp.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", path, resp.Header.Get("Referrer-Policy"))
		}
		if got := resp.Header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
			t.Errorf("%s: X-Robots-Tag = %q, want containing noindex", path, got)
		}
	}
}

// --- Per-user signing secret verify path + deployment fallback ---

// Tokens signed with the agent owner's per-account secret should
// verify via the primary path (verifyTokenAnySecret tries user secrets
// first). The deployment-wide signer is unrelated to this user's
// secret and should not be consulted.
func TestMagicApprove_VerifiesWithUserSecret(t *testing.T) {
	server, store, _, _ := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "user-secret-verify")
	msg := issuePending(t, store, a.ID)

	// Pull the user's most-recent secret (the auto-created default).
	ctx := context.Background()
	secrets, err := store.GetUserSigningSecrets(ctx, userID)
	if err != nil || len(secrets) == 0 {
		t.Fatalf("get user secrets: %v (n=%d)", err, len(secrets))
	}
	tok, err := approvaltoken.Sign(secrets[0].Secret, msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve via user-secret token: status %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// Tokens signed with the deployment-wide signer (the legacy/fallback
// path) must still verify even though the user has their own secret.
// This covers tokens issued before the per-user-secrets migration ran.
func TestMagicApprove_FallsBackToDeploymentSigner(t *testing.T) {
	server, store, signer, _ := setupMagicLinkAPI(t)
	a, _ := prepareHITLAgent(t, store, "deployment-fallback")
	msg := issuePending(t, store, a.ID)

	// Sign with the deployment-wide signer, NOT the user's per-account
	// secret. verifyTokenAnySecret will try the user's secret (mismatch),
	// then fall back to the deployment signer (match).
	tok, err := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve via deployment-signed token: status %d, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

// A token signed with neither the user's secret nor the deployment
// signer must be rejected. Guards against accepting any HMAC-shaped
// blob just because the message_id resolves.
func TestMagicApprove_RejectsForeignSecret(t *testing.T) {
	server, store, _, _ := setupMagicLinkAPI(t)
	a, _ := prepareHITLAgent(t, store, "foreign-secret-reject")
	msg := issuePending(t, store, a.ID)

	tok, err := approvaltoken.Sign("attacker-controlled-secret", msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("foreign-secret token MUST be rejected, got %d", resp.StatusCode)
	}
}

// Tokens signed with an OLD per-user secret should still verify after
// the user creates a new one — until the old one is deleted. This is
// the whole point of multi-secret rotation.
func TestMagicApprove_OldUserSecretStillVerifiesAfterRotation(t *testing.T) {
	server, store, _, _ := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "rotation-window")
	msg := issuePending(t, store, a.ID)

	ctx := context.Background()
	// Capture the original secret.
	beforeRotation, err := store.GetUserSigningSecrets(ctx, userID)
	if err != nil || len(beforeRotation) == 0 {
		t.Fatalf("get user secrets: %v", err)
	}
	oldSecret := beforeRotation[0].Secret

	// Sign a token with the OLD secret, then rotate (create new).
	tok, err := approvaltoken.Sign(oldSecret, msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSigningSecret(ctx, userID, "new-after-rotation"); err != nil {
		t.Fatal(err)
	}
	// User now has 2 secrets; the old one is at index [1] (most-recent
	// is the new one). The verifier should still accept the old token
	// because it tries all of the user's secrets.

	resp := postForm(t, server.URL+"/api/v1/approve", map[string]string{"t": tok})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("old-secret token must verify until that secret is deleted, got %d body=%s",
			resp.StatusCode, readBody(t, resp))
	}
}
