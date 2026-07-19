package agent_test

// Suppression enforcement on the HITL approval paths (GA hardening).
//
// The direct-send path has always checked the tenant suppression list at
// accept time (DeliverOutbound → checkSuppression), but the approval paths
// re-composed and queued a held draft WITHOUT a fresh check, so:
//   - an address suppressed while the draft sat in pending_review was sent
//     anyway on approve, and
//   - reviewer-added To/CC/BCC recipients were never checked at all.
//
// These tests pin the fixed behavior: human approval checks the FINAL merged
// recipient set (stored draft + reviewer overrides), refuses with 422
// recipient_suppressed, leaves the hold pending_review (retryable after the
// suppression is cleared), and never completes the approval's idempotency
// record on refusal — matching send's semantics, where a 422
// recipient_suppressed is returned before any side effect so runIdempotent
// releases (never caches) the key.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/approvaltoken"
	"github.com/tokencanopy/e2a/internal/identity"
)

// holdDraft creates a pending_review outbound draft on the agent.
func holdDraft(t *testing.T, store *identity.Store, agentID string, to, cc, bcc []string) *identity.Message {
	t.Helper()
	msg, err := store.CreatePendingOutboundMessage(context.Background(), agentID,
		to, cc, bcc, "Held", "body", "", nil, "send", "", "", "", 3600)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}
	return msg
}

// requireStillPending asserts the hold was left untouched by a refused
// approval: status pending_review, no delivery acceptance, no queued job.
func requireStillPending(t *testing.T, store *identity.Store, msgID string) {
	t.Helper()
	var status, deliveryStatus string
	var sendJobID *int64
	if err := store.WithTx(context.Background(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT status, COALESCE(delivery_status,''), send_job_id FROM messages WHERE id=$1`, msgID,
		).Scan(&status, &deliveryStatus, &sendJobID)
	}); err != nil {
		t.Fatalf("read held row: %v", err)
	}
	if status != identity.MessageStatusPendingReview {
		t.Errorf("status = %q, want pending_review (refusal must not resolve the hold)", status)
	}
	if deliveryStatus == "accepted" || deliveryStatus == "sending" || deliveryStatus == "sent" {
		t.Errorf("delivery_status = %q, want no delivery acceptance on refusal", deliveryStatus)
	}
	if sendJobID != nil {
		t.Errorf("send_job_id = %d, want nil (refusal must not enqueue)", *sendJobID)
	}
}

// TestApprovePendingCore_RecipientSuppressedWhileHeld: a draft whose ORIGINAL
// recipient became suppressed while it sat in review cannot be approved+sent.
// The refusal is a 422 recipient_suppressed, the hold stays pending_review,
// the idempotency completer never runs, and once the suppression is removed
// the same approve succeeds.
func TestApprovePendingCore_RecipientSuppressedWhileHeld(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprsuppheld")

	msg := holdDraft(t, store, ag.ID, []string{"alice@external.test"}, nil, nil)

	// Suppress with different CASE than the stored recipient — normalization
	// must still match (suppressions are stored normalized; the check
	// normalizes its input).
	if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "ALICE@External.TEST", "opted out", "unsubscribe", nil); err != nil {
		t.Fatalf("AddAgentSuppression: %v", err)
	}

	idemCalled := false
	complete := func(context.Context, pgx.Tx, *identity.Message) error {
		idemCalled = true
		return nil
	}
	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, complete)
	if oerr == nil {
		t.Fatalf("approve of a suppressed recipient succeeded: %+v", sent)
	}
	if oerr.Status != http.StatusUnprocessableEntity || oerr.Code != "recipient_suppressed" {
		t.Fatalf("error = %d %s %q, want 422 recipient_suppressed", oerr.Status, oerr.Code, oerr.Msg)
	}
	if !strings.Contains(oerr.Msg, "alice@external.test") {
		t.Errorf("error message %q should name the suppressed address", oerr.Msg)
	}
	if idemCalled {
		t.Error("idempotency completer ran on a refused approval — a 422 must not poison the key")
	}
	requireStillPending(t, store, msg.ID)

	// Clearing the suppression makes the SAME approve succeed (the refusal
	// resolved nothing and cached nothing).
	if _, err := store.RemoveAgentSuppression(ctx, user.ID, ag.ID, "alice@external.test"); err != nil {
		t.Fatalf("RemoveAgentSuppression: %v", err)
	}
	sent, oerr = api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, complete)
	if oerr != nil {
		t.Fatalf("approve after un-suppression: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if sent.DeliveryStatus != "accepted" {
		t.Errorf("post-unsuppress DeliveryStatus = %q, want accepted", sent.DeliveryStatus)
	}
	if !idemCalled {
		t.Error("successful approve must complete the idempotency record in-tx")
	}
}

// TestApprovePendingCore_ReviewerAddedSuppressedRecipient: reviewer overrides
// are applied BEFORE the suppression check, so a suppressed address smuggled
// in via any override field (to/cc/bcc, any case) is refused; the untouched
// draft then approves cleanly.
func TestApprovePendingCore_ReviewerAddedSuppressedRecipient(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprsuppovr")

	if _, _, err := store.AddAgentSuppression(ctx, user.ID, ag.ID, "bad@external.test", "opted out", "unsubscribe", nil); err != nil {
		t.Fatalf("AddAgentSuppression: %v", err)
	}

	msg := holdDraft(t, store, ag.ID, []string{"clean@external.test"}, nil, nil)

	bad := []string{"Bad@External.TEST"} // case-varied on purpose
	clean := []string{"clean@external.test"}
	cases := []struct {
		name string
		ovr  agent.ApproveOverrides
	}{
		{"to", agent.ApproveOverrides{To: &bad}},
		{"cc", agent.ApproveOverrides{To: &clean, CC: &bad}},
		{"bcc", agent.ApproveOverrides{To: &clean, BCC: &bad}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, tc.ovr, nil)
			if oerr == nil {
				t.Fatalf("approve with reviewer-added suppressed %s succeeded: %+v", tc.name, sent)
			}
			if oerr.Status != http.StatusUnprocessableEntity || oerr.Code != "recipient_suppressed" {
				t.Fatalf("error = %d %s, want 422 recipient_suppressed", oerr.Status, oerr.Code)
			}
			requireStillPending(t, store, msg.ID)
		})
	}

	// The unmodified draft (no suppressed recipient) still approves.
	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, nil)
	if oerr != nil {
		t.Fatalf("clean approve: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if sent.DeliveryStatus != "accepted" {
		t.Errorf("clean approve DeliveryStatus = %q, want accepted", sent.DeliveryStatus)
	}
}

// TestApprovePendingCore_SuppressionIsTenantScoped: another account's
// suppression of the same address must NOT block this account's approval —
// the check stays scoped to the message's owner.
func TestApprovePendingCore_SuppressionIsTenantScoped(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprsupptenant")
	otherUser, _ := selfAgent(t, store, "apprsuppother")

	if _, err := store.AddSuppression(ctx, otherUser.ID, "alice@external.test", "manual", "manual", ""); err != nil {
		t.Fatalf("AddSuppression(other tenant): %v", err)
	}

	msg := holdDraft(t, store, ag.ID, []string{"alice@external.test"}, nil, nil)
	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, nil)
	if oerr != nil {
		t.Fatalf("approve blocked by ANOTHER tenant's suppression: status=%d code=%s", oerr.Status, oerr.Code)
	}
	if sent.DeliveryStatus != "accepted" {
		t.Errorf("DeliveryStatus = %q, want accepted", sent.DeliveryStatus)
	}
}

// TestApprovePendingCore_SelfSendUnaffectedBySuppressionCheck: an unsuppressed
// self-send still resolves through the sync loopback path — the new check must
// not disturb self-send behavior.
func TestApprovePendingCore_SelfSendUnaffectedBySuppressionCheck(t *testing.T) {
	api, store, _, _ := setupAsyncAPI(t)
	ctx := context.Background()
	user, ag := selfAgent(t, store, "apprsuppself")

	msg := holdDraft(t, store, ag.ID, []string{ag.EmailAddress()}, nil, nil)
	sent, oerr := api.ApprovePendingCore(ctx, user.ID, msg.ID, ag.Email, agent.ApproveOverrides{}, nil)
	if oerr != nil {
		t.Fatalf("self-send approve: status=%d code=%s msg=%s", oerr.Status, oerr.Code, oerr.Msg)
	}
	if sent.Method != "loopback" {
		t.Errorf("Method = %q, want loopback", sent.Method)
	}
}

// TestMagicApprovePOST_SuppressedRecipientRefused: the magic-link approve path
// runs the same owner-scoped suppression check — the confirmation POST renders
// an error page and the hold stays pending_review.
func TestMagicApprovePOST_SuppressedRecipientRefused(t *testing.T) {
	server, store, signer, smtpDone := setupMagicLinkAPI(t)
	a, userID := prepareHITLAgent(t, store, "magic-suppressed")
	msg := issuePending(t, store, a.ID) // held to alice@example.com

	if _, _, err := store.AddAgentSuppression(context.Background(), userID, a.ID, "Alice@Example.COM", "opted out", "unsubscribe", nil); err != nil {
		t.Fatalf("AddAgentSuppression: %v", err)
	}

	tok, _ := signer.Sign(msg.ID, approvaltoken.ActionApprove, time.Now().Add(1*time.Hour))
	resp := postForm(t, server.URL+"/v1/approve", map[string]string{"t": tok})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("POST approve of suppressed recipient: status = %d, want 422; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "suppression") {
		t.Errorf("error page should mention the suppression list, got: %s", body)
	}
	if msgs := smtpDone(); len(msgs) != 0 {
		t.Fatalf("refused magic approve submitted %d SMTP messages, want zero", len(msgs))
	}
	requireStillPending(t, store, msg.ID)
}
