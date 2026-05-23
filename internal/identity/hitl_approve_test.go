package identity_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// setupPendingAgent makes a verified agent owned by a freshly-created user
// and returns (user, agent). Simplifies test scaffolding for the HITL
// approve/reject/list suite.
func setupPendingAgent(t *testing.T, store *identity.Store, slug string) (*identity.User, *identity.AgentIdentity) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+slug+"@example.com", "Owner", "google-"+slug)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, slug+".example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, slug+".example.com", user.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}
	a, err := store.CreateAgent(ctx, "bot@"+slug+".example.com", slug+".example.com", "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, true, identity.HITLDefaultTTLSeconds, identity.HITLExpirationReject); err != nil {
		t.Fatalf("UpdateAgentHITL: %v", err)
	}
	return user, a
}

func TestGetOutboundMessageForUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "get-ob")

	atts, _ := json.Marshal([]map[string]string{{"filename": "f.txt", "content_type": "text/plain", "data": "aA=="}})
	msg, err := store.CreatePendingOutboundMessage(
		ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "plain", "<p>html</p>",
		atts,
		"send", "conv_1", "",
		3600,
	)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	got, err := store.GetOutboundMessageForUser(ctx, msg.ID, user.ID)
	if err != nil {
		t.Fatalf("GetOutboundMessageForUser: %v", err)
	}
	if got.Status != identity.MessageStatusPendingApproval {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Subject != "Draft" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if got.BodyText != "plain" {
		t.Errorf("BodyText = %q", got.BodyText)
	}
	if got.BodyHTML != "<p>html</p>" {
		t.Errorf("BodyHTML = %q", got.BodyHTML)
	}
	if len(got.AttachmentsJSON) == 0 {
		t.Error("AttachmentsJSON empty")
	}
	if got.ApprovalExpiresAt == nil {
		t.Error("ApprovalExpiresAt nil")
	}
	if got.Type != "send" {
		t.Errorf("Type = %q", got.Type)
	}
}

func TestGetOutboundMessageForUserCrossUserIsNotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "cross-get")
	otherUser, err := store.CreateOrGetUser(ctx, "other@example.com", "Other", "google-other-get")
	if err != nil {
		t.Fatal(err)
	}

	msg, _ := store.CreatePendingOutboundMessage(
		ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "b", "", nil,
		"send", "", "", 3600,
	)

	_, err = store.GetOutboundMessageForUser(ctx, msg.ID, otherUser.ID)
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}
}

func TestListPendingOutboundForUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "list")

	// Three pending messages with different expiries
	_, err := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"c@example.com"}, nil, nil, "C", "x", "", nil, "send", "", "", 7200)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"a@example.com"}, nil, nil, "A", "x", "", nil, "send", "", "", 600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"b@example.com"}, nil, nil, "B", "x", "", nil, "send", "", "", 3600)
	if err != nil {
		t.Fatal(err)
	}

	// A sent message on same agent should not appear in the list
	_, err = store.CreateOutboundMessage(ctx, a.ID,
		[]string{"sent@example.com"}, nil, nil, "Already sent", "send", "smtp", "<p1>", "")
	if err != nil {
		t.Fatal(err)
	}

	got, err := store.ListPendingOutboundForUser(ctx, user.ID, 50)
	if err != nil {
		t.Fatalf("ListPendingOutboundForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 pending messages, got %d", len(got))
	}
	// Sorted by approval_expires_at ASC: A (600s) first, B (3600s), C (7200s)
	subjects := []string{got[0].Subject, got[1].Subject, got[2].Subject}
	want := []string{"A", "B", "C"}
	for i, s := range subjects {
		if s != want[i] {
			t.Errorf("pos %d: %q, want %q", i, s, want[i])
		}
	}
	// Body columns should not be populated in list output
	for i, m := range got {
		if m.BodyText != "" || m.BodyHTML != "" || len(m.AttachmentsJSON) != 0 {
			t.Errorf("row %d leaked body fields: text=%q html=%q att=%v", i, m.BodyText, m.BodyHTML, m.AttachmentsJSON)
		}
	}
}

func TestListPendingOutboundForUserScoping(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userA, agentA := setupPendingAgent(t, store, "scope-a")
	_, agentB := setupPendingAgent(t, store, "scope-b")

	_, _ = store.CreatePendingOutboundMessage(ctx, agentA.ID,
		[]string{"x@example.com"}, nil, nil, "A-msg", "b", "", nil, "send", "", "", 3600)
	_, _ = store.CreatePendingOutboundMessage(ctx, agentB.ID,
		[]string{"x@example.com"}, nil, nil, "B-msg", "b", "", nil, "send", "", "", 3600)

	got, err := store.ListPendingOutboundForUser(ctx, userA.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subject != "A-msg" {
		t.Errorf("userA should see only A-msg; got %+v", got)
	}
}

func TestApproveAndSendHappyPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-happy")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, []string{"carol@example.com"}, nil,
		"Draft", "body", "<p>body</p>",
		[]byte(`[{"filename":"x.txt","content_type":"text/plain","data":"aGk="}]`),
		"send", "conv_h", "", 3600)

	var receivedSubject string
	var receivedBody string
	sent, err := store.ApproveAndSend(ctx, msg.ID, user.ID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			receivedSubject = m.Subject
			receivedBody = m.BodyText
			return identity.SendResult{
				ProviderMessageID: "<ses-123@amazonses.com>",
				Method:            "smtp",
				To:                m.ToRecipients,
				CC:                m.CC,
				BCC:               m.BCC,
			}, nil
		})
	if err != nil {
		t.Fatalf("ApproveAndSend: %v", err)
	}
	if receivedSubject != "Draft" {
		t.Errorf("send callback got subject %q", receivedSubject)
	}
	if receivedBody != "body" {
		t.Errorf("send callback got body %q", receivedBody)
	}
	if sent.Status != identity.MessageStatusSent {
		t.Errorf("returned status = %q", sent.Status)
	}
	if sent.ProviderMessageID != "<ses-123@amazonses.com>" {
		t.Errorf("provider id = %q", sent.ProviderMessageID)
	}
	if sent.Edited {
		t.Error("edited should be false for approve-as-is")
	}

	// DB: row is now sent, body scrubbed
	var dbStatus, dbProviderID string
	var dbBodyText, dbBodyHTML *string
	var dbAttachments []byte
	var dbEdited bool
	err = pool.QueryRow(ctx,
		`SELECT status, provider_message_id, body_text, body_html, attachments_json, edited
		 FROM messages WHERE id = $1`, msg.ID,
	).Scan(&dbStatus, &dbProviderID, &dbBodyText, &dbBodyHTML, &dbAttachments, &dbEdited)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if dbStatus != identity.MessageStatusSent {
		t.Errorf("db status = %q", dbStatus)
	}
	if dbProviderID != "<ses-123@amazonses.com>" {
		t.Errorf("db provider_id = %q", dbProviderID)
	}
	if dbBodyText != nil || dbBodyHTML != nil {
		t.Errorf("body columns not scrubbed: text=%v html=%v", dbBodyText, dbBodyHTML)
	}
	if dbAttachments != nil {
		t.Errorf("attachments_json not scrubbed: %v", dbAttachments)
	}
	if dbEdited {
		t.Error("db edited should be false")
	}
}

// TestApproveAndSend_RecordsReviewedBy: migration 012 attributes the
// approval to the human reviewer. ApproveAndSend passes its userID
// argument straight into reviewed_by_user_id; GetOutboundMessageForUser's
// JOIN with users surfaces the reviewer's display name as
// ReviewedByName.
func TestApproveAndSend_RecordsReviewedBy(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-reviewer")
	// setupPendingAgent's user has Name="Owner"; the dashboard pulls
	// reviewed_by_name from the JOIN'd users.name column, so the test
	// asserts whatever that helper set.

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"With reviewer", "body", "", nil, "send", "", "", 3600)

	sent, err := store.ApproveAndSend(ctx, msg.ID, user.ID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			return identity.SendResult{ProviderMessageID: "<x@y>", Method: "smtp", To: m.ToRecipients}, nil
		})
	if err != nil {
		t.Fatalf("ApproveAndSend: %v", err)
	}
	if sent.ReviewedByUserID == nil || *sent.ReviewedByUserID != user.ID {
		t.Errorf("returned ReviewedByUserID = %v, want %q", sent.ReviewedByUserID, user.ID)
	}

	// Round-trip via GetOutboundMessageForUser — the JOIN with users
	// must populate ReviewedByName for the detail panel.
	got, err := store.GetOutboundMessageForUser(ctx, msg.ID, user.ID)
	if err != nil {
		t.Fatalf("GetOutboundMessageForUser: %v", err)
	}
	if got.ReviewedByUserID == nil || *got.ReviewedByUserID != user.ID {
		t.Errorf("ReviewedByUserID via Get = %v, want %q", got.ReviewedByUserID, user.ID)
	}
	if got.ReviewedByName == nil || *got.ReviewedByName == "" {
		t.Errorf("ReviewedByName via Get should be populated; got %v", got.ReviewedByName)
	}
}

// TestRejectPending_RecordsReviewedBy: same shape for the reject side.
func TestRejectPending_RecordsReviewedBy(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "reject-reviewer")
	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"to-reject", "body", "", nil, "send", "", "", 3600)

	rejected, err := store.RejectPending(ctx, msg.ID, user.ID, "wrong tone")
	if err != nil {
		t.Fatalf("RejectPending: %v", err)
	}
	if rejected.ReviewedByUserID == nil || *rejected.ReviewedByUserID != user.ID {
		t.Errorf("rejected ReviewedByUserID = %v, want %q", rejected.ReviewedByUserID, user.ID)
	}
	if rejected.ReviewedByName == nil || *rejected.ReviewedByName == "" {
		t.Errorf("rejected ReviewedByName should be populated; got %v", rejected.ReviewedByName)
	}
}

// TestExpireApprove_ReviewedByNil: TTL auto-approve has no human
// reviewer, so reviewed_by_user_id stays NULL. The redesign's
// pending detail panel uses this signal to render "expired"
// instead of "approved by X".
func TestExpireApprove_ReviewedByNil(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "expire-no-reviewer")
	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"to-expire", "body", "", nil, "send", "", "", 3600)
	// Backdate so ExpireApproveAndSend's pending+expired predicate fires.
	pool.Exec(ctx, `UPDATE messages SET approval_expires_at = $1 WHERE id = $2`,
		time.Now().Add(-5*time.Minute), msg.ID)

	sent, err := store.ExpireApproveAndSend(ctx, msg.ID,
		func(m *identity.Message) (identity.SendResult, error) {
			return identity.SendResult{ProviderMessageID: "<x@y>", Method: "smtp", To: m.ToRecipients}, nil
		})
	if err != nil {
		t.Fatalf("ExpireApproveAndSend: %v", err)
	}
	if sent.ReviewedByUserID != nil {
		t.Errorf("ReviewedByUserID = %v, want nil on worker-triggered approve", sent.ReviewedByUserID)
	}

	// Detail endpoint also leaves both fields nil.
	got, _ := store.GetOutboundMessageForUser(ctx, msg.ID, user.ID)
	if got.ReviewedByUserID != nil || got.ReviewedByName != nil {
		t.Errorf("detail row should have null reviewer (worker action); got userID=%v name=%v",
			got.ReviewedByUserID, got.ReviewedByName)
	}
}

func TestApproveAndSendWithEdits(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-edit")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "orig body", "", nil,
		"send", "", "", 3600)

	newSubject := "Edited subject"
	newBody := "Edited body"
	edits := identity.PendingApprovalEdit{
		Subject:  &newSubject,
		BodyText: &newBody,
		To:       []string{"bob@example.com"},
	}

	var sentTo []string
	var sentSubject, sentBody string
	sent, err := store.ApproveAndSend(ctx, msg.ID, user.ID, edits,
		func(m *identity.Message) (identity.SendResult, error) {
			sentTo = m.ToRecipients
			sentSubject = m.Subject
			sentBody = m.BodyText
			return identity.SendResult{
				ProviderMessageID: "<ses-edit@amazonses.com>",
				Method:            "smtp",
				To:                m.ToRecipients,
			}, nil
		})
	if err != nil {
		t.Fatalf("ApproveAndSend: %v", err)
	}
	if sentSubject != "Edited subject" {
		t.Errorf("send callback subject = %q, want 'Edited subject'", sentSubject)
	}
	if sentBody != "Edited body" {
		t.Errorf("send callback body = %q", sentBody)
	}
	if len(sentTo) != 1 || sentTo[0] != "bob@example.com" {
		t.Errorf("send callback To = %v, want [bob@example.com]", sentTo)
	}
	if !sent.Edited {
		t.Error("returned edited should be true")
	}

	var dbEdited bool
	var dbSubject string
	var dbTo []string
	err = pool.QueryRow(ctx,
		`SELECT edited, subject, to_recipients FROM messages WHERE id = $1`, msg.ID,
	).Scan(&dbEdited, &dbSubject, &dbTo)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !dbEdited {
		t.Error("db edited should be true")
	}
	if dbSubject != "Edited subject" {
		t.Errorf("db subject = %q", dbSubject)
	}
	if len(dbTo) != 1 || dbTo[0] != "bob@example.com" {
		t.Errorf("db to_recipients = %v", dbTo)
	}
}

func TestApproveAndSendSendFailureRollsBack(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-fail")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "body", "", nil,
		"send", "", "", 3600)

	sendErr := errors.New("smtp unavailable")
	_, err := store.ApproveAndSend(ctx, msg.ID, user.ID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			return identity.SendResult{}, sendErr
		})
	if !errors.Is(err, sendErr) {
		t.Errorf("expected send error propagated, got %v", err)
	}

	// Row stays pending; body columns still present
	var status string
	var bodyText *string
	err = pool.QueryRow(ctx,
		`SELECT status, body_text FROM messages WHERE id = $1`, msg.ID,
	).Scan(&status, &bodyText)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusPendingApproval {
		t.Errorf("status = %q, want still pending_approval after send failure", status)
	}
	if bodyText == nil || *bodyText != "body" {
		t.Errorf("body_text = %v, expected intact", bodyText)
	}
}

func TestApproveAndSendNotPending(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-np")

	// Already-sent message
	sent, _ := store.CreateOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil, "Already sent", "send", "smtp", "<p>", "")

	_, err := store.ApproveAndSend(ctx, sent.ID, user.ID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			t.Fatal("send callback should not be invoked on non-pending row")
			return identity.SendResult{}, nil
		})
	if !errors.Is(err, identity.ErrNotPendingApproval) {
		t.Errorf("expected ErrNotPendingApproval, got %v", err)
	}
}

// TestApproveAndSendConcurrentApprovers verifies the FOR UPDATE lock in
// ApproveAndSend serializes two concurrent approval attempts: one wins
// and transitions the row to sent, the other sees ErrNotPendingApproval.
// Uses a barrier so both approvers reach ApproveAndSend before either
// can finish — otherwise the second would simply see a sent row.
func TestApproveAndSendConcurrentApprovers(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-concurrent")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "body", "", nil,
		"send", "", "", 3600)

	// Barrier: both goroutines enter ApproveAndSend, then their send
	// callbacks wait on `release` before returning. The DB row lock makes
	// one of them block in ApproveAndSend's SELECT FOR UPDATE until the
	// other commits. Whichever gets past SELECT first wins.
	release := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]error, 0, 2)
	send := func(m *identity.Message) (identity.SendResult, error) {
		<-release
		return identity.SendResult{
			ProviderMessageID: "<ses-concurrent@amazonses.com>",
			Method:            "smtp",
			To:                m.ToRecipients,
		}, nil
	}

	approve := func() {
		defer wg.Done()
		_, err := store.ApproveAndSend(ctx, msg.ID, user.ID, identity.PendingApprovalEdit{}, send)
		mu.Lock()
		results = append(results, err)
		mu.Unlock()
	}

	wg.Add(2)
	go approve()
	go approve()

	// Let both goroutines begin and one of them grab the row lock.
	close(release)
	wg.Wait()

	var nilErrs, notPending int
	for _, e := range results {
		switch {
		case e == nil:
			nilErrs++
		case errors.Is(e, identity.ErrNotPendingApproval):
			notPending++
		default:
			t.Errorf("unexpected error: %v", e)
		}
	}
	if nilErrs != 1 {
		t.Errorf("exactly one approver should succeed, got %d", nilErrs)
	}
	if notPending != 1 {
		t.Errorf("exactly one approver should see ErrNotPendingApproval, got %d", notPending)
	}

	// DB reflects a single sent row.
	var status, providerID string
	err := pool.QueryRow(ctx,
		`SELECT status, provider_message_id FROM messages WHERE id = $1`, msg.ID,
	).Scan(&status, &providerID)
	if err != nil {
		t.Fatal(err)
	}
	if status != identity.MessageStatusSent {
		t.Errorf("final status = %q, want sent", status)
	}
	if providerID != "<ses-concurrent@amazonses.com>" {
		t.Errorf("provider id = %q", providerID)
	}
}

// TestApproveAndSendClearsAttachmentsWhenEditsSetEmpty verifies that an
// approver can explicitly clear attachments by passing AttachmentsSet=true
// with AttachmentsJSON=[] (as distinct from nil, which preserves).
func TestApproveAndSendClearsAttachmentsWhenEditsSetEmpty(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-clear-att")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "body", "", []byte(`[{"filename":"x","content_type":"text/plain","data":"aA=="}]`),
		"send", "", "", 3600)

	var sawAttachments int
	clearEdit := identity.PendingApprovalEdit{
		AttachmentsJSON: []byte("[]"),
		AttachmentsSet:  true,
	}
	sent, err := store.ApproveAndSend(ctx, msg.ID, user.ID, clearEdit,
		func(m *identity.Message) (identity.SendResult, error) {
			// Callback sees the post-edit message: attachments_json raw
			// should be "[]" — an empty array, not the original.
			sawAttachments = len(m.AttachmentsJSON)
			return identity.SendResult{
				ProviderMessageID: "<ses-clear@amazonses.com>",
				Method:            "smtp",
				To:                m.ToRecipients,
			}, nil
		})
	if err != nil {
		t.Fatalf("ApproveAndSend: %v", err)
	}
	if !sent.Edited {
		t.Error("edited should be true when attachments are overridden")
	}
	// json.RawMessage for "[]" is 2 bytes; for nil or original it'd be 0 or >40.
	if sawAttachments != 2 {
		t.Errorf("send callback saw AttachmentsJSON len %d, want 2 (= \"[]\")", sawAttachments)
	}
}

// TestApproveAndSendPreservesPriorEditedFlag ensures that approving an
// unedited message does NOT silently reset edited=true if the row was
// somehow already marked. (Pre-approve edits land via a direct update in
// the dashboard flow; belt-and-suspenders.)
func TestApproveAndSendPreservesPriorEditedFlag(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "approve-preserve-edit")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "body", "", nil,
		"send", "", "", 3600)

	// Simulate a prior edit flag on the stored row.
	if _, err := pool.Exec(ctx,
		`UPDATE messages SET edited = true WHERE id = $1`, msg.ID); err != nil {
		t.Fatal(err)
	}

	sent, err := store.ApproveAndSend(ctx, msg.ID, user.ID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			return identity.SendResult{
				ProviderMessageID: "<ses-pres@amazonses.com>",
				Method:            "smtp",
				To:                m.ToRecipients,
			}, nil
		})
	if err != nil {
		t.Fatalf("ApproveAndSend: %v", err)
	}
	if !sent.Edited {
		t.Error("returned edited should remain true (was already true pre-approve)")
	}
	var dbEdited bool
	err = pool.QueryRow(ctx,
		`SELECT edited FROM messages WHERE id = $1`, msg.ID,
	).Scan(&dbEdited)
	if err != nil {
		t.Fatal(err)
	}
	if !dbEdited {
		t.Error("db edited should remain true after approve-as-is")
	}
}

func TestApproveAndSendCrossUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "approve-cross")
	otherUser, _ := store.CreateOrGetUser(ctx, "other-approve@example.com", "Other", "google-other-approve")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "b", "", nil, "send", "", "", 3600)

	_, err := store.ApproveAndSend(ctx, msg.ID, otherUser.ID, identity.PendingApprovalEdit{},
		func(m *identity.Message) (identity.SendResult, error) {
			t.Fatal("send callback should not be invoked for cross-user access")
			return identity.SendResult{}, nil
		})
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("expected ErrMessageNotFound for cross-user approve, got %v", err)
	}
}

func TestRejectPendingHappyPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "reject-happy")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "bodyX", "<p>htmlX</p>",
		[]byte(`[{"filename":"x.txt","content_type":"text/plain","data":"aA=="}]`),
		"send", "", "", 3600)

	got, err := store.RejectPending(ctx, msg.ID, user.ID, "inappropriate tone")
	if err != nil {
		t.Fatalf("RejectPending: %v", err)
	}
	if got.Status != identity.MessageStatusRejected {
		t.Errorf("Status = %q", got.Status)
	}
	if got.RejectionReason != "inappropriate tone" {
		t.Errorf("RejectionReason = %q", got.RejectionReason)
	}
	if got.ReviewedAt == nil {
		t.Error("ReviewedAt nil")
	}
	if got.BodyText != "" || got.BodyHTML != "" || len(got.AttachmentsJSON) != 0 {
		t.Error("returned message still carries body fields after reject")
	}

	// DB: body scrubbed to NULL
	var bodyText, bodyHTML *string
	var attachments []byte
	var reason *string
	err = pool.QueryRow(ctx,
		`SELECT body_text, body_html, attachments_json, rejection_reason
		 FROM messages WHERE id = $1`, msg.ID,
	).Scan(&bodyText, &bodyHTML, &attachments, &reason)
	if err != nil {
		t.Fatal(err)
	}
	if bodyText != nil || bodyHTML != nil || attachments != nil {
		t.Errorf("body not scrubbed: text=%v html=%v att=%v", bodyText, bodyHTML, attachments)
	}
	if reason == nil || *reason != "inappropriate tone" {
		t.Errorf("reason = %v", reason)
	}
}

func TestRejectPendingAlreadyRejected(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, a := setupPendingAgent(t, store, "reject-twice")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "b", "", nil, "send", "", "", 3600)

	if _, err := store.RejectPending(ctx, msg.ID, user.ID, "first time"); err != nil {
		t.Fatalf("first reject: %v", err)
	}
	_, err := store.RejectPending(ctx, msg.ID, user.ID, "second time")
	if !errors.Is(err, identity.ErrNotPendingApproval) {
		t.Errorf("expected ErrNotPendingApproval on second reject, got %v", err)
	}
}

func TestRejectPendingCrossUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	_, a := setupPendingAgent(t, store, "reject-cross")
	otherUser, _ := store.CreateOrGetUser(ctx, "other-reject@example.com", "Other", "google-other-reject")

	msg, _ := store.CreatePendingOutboundMessage(ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"Draft", "b", "", nil, "send", "", "", 3600)

	_, err := store.RejectPending(ctx, msg.ID, otherUser.ID, "nope")
	if !errors.Is(err, identity.ErrMessageNotFound) {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}
}

func TestPendingApprovalEditApply(t *testing.T) {
	subj := "new subject"
	body := "new body"
	msg := &identity.Message{
		Subject:      "old",
		BodyText:     "old body",
		ToRecipients: []string{"a@x.com"},
		CC:           []string{"c@x.com"},
	}

	edits := identity.PendingApprovalEdit{
		Subject:  &subj,
		BodyText: &body,
		To:       []string{"b@x.com"},
	}
	if !edits.Apply(msg) {
		t.Error("Apply should return true when fields change")
	}
	if msg.Subject != "new subject" || msg.BodyText != "new body" {
		t.Errorf("edit not applied: %+v", msg)
	}
	if len(msg.ToRecipients) != 1 || msg.ToRecipients[0] != "b@x.com" {
		t.Errorf("To override: %v", msg.ToRecipients)
	}
	// CC not overridden (nil in edit) → preserved
	if len(msg.CC) != 1 || msg.CC[0] != "c@x.com" {
		t.Errorf("CC should be preserved, got %v", msg.CC)
	}

	// No-op edits
	msg2 := &identity.Message{Subject: "x"}
	empty := identity.PendingApprovalEdit{}
	if empty.Apply(msg2) {
		t.Error("Apply on empty edit should return false")
	}

	// Same-value edit → no change
	sameSubj := "x"
	same := identity.PendingApprovalEdit{Subject: &sameSubj}
	if same.Apply(msg2) {
		t.Error("Apply with identical value should not flag edited")
	}
}
