package identity_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// --- ValidateHITLConfig (pure) ---

func TestValidateHITLConfig(t *testing.T) {
	cases := []struct {
		name    string
		ttl     int
		action  string
		wantErr bool
	}{
		{"valid 7 days + reject", 604800, "reject", false},
		{"valid 1 hour + approve", 3600, "approve", false},
		{"ttl zero", 0, "reject", true},
		{"ttl negative", -1, "reject", true},
		{"ttl above max", 604801, "reject", true},
		{"action empty", 3600, "", true},
		{"action invalid", 3600, "maybe", true},
		{"action caps", 3600, "Reject", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := identity.ValidateHITLConfig(tc.ttl, tc.action)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// --- CreateAgent default HITL state ---

func TestCreateAgentDefaultHITL(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-hitl-default")
	store.ClaimOrCreateDomain(ctx, "hitl-default.example.com", user.ID)

	a, err := store.CreateAgent(ctx, "bot@hitl-default.example.com", "hitl-default.example.com", "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.HITLEnabled {
		t.Error("HITLEnabled should default to false")
	}
	if a.HITLTTLSeconds != identity.HITLDefaultTTLSeconds {
		t.Errorf("HITLTTLSeconds = %d, want %d", a.HITLTTLSeconds, identity.HITLDefaultTTLSeconds)
	}
	if a.HITLExpirationAction != identity.HITLExpirationReject {
		t.Errorf("HITLExpirationAction = %q, want %q", a.HITLExpirationAction, identity.HITLExpirationReject)
	}

	// Round-trip through the database: GetAgentByID must surface the same defaults.
	got, err := store.GetAgentByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got.HITLEnabled || got.HITLTTLSeconds != identity.HITLDefaultTTLSeconds || got.HITLExpirationAction != identity.HITLExpirationReject {
		t.Errorf("round-trip defaults mismatch: %+v", got)
	}
}

// --- UpdateAgentHITL ---

func TestUpdateAgentHITL(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-hitl-update")
	store.ClaimOrCreateDomain(ctx, "hitl-update.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@hitl-update.example.com", "hitl-update.example.com", "", "https://example.com/webhook", "", user.ID)

	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, true, 3600, identity.HITLExpirationApprove); err != nil {
		t.Fatalf("UpdateAgentHITL: %v", err)
	}

	got, err := store.GetAgentByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if !got.HITLEnabled {
		t.Error("HITLEnabled should be true")
	}
	if got.HITLTTLSeconds != 3600 {
		t.Errorf("HITLTTLSeconds = %d, want 3600", got.HITLTTLSeconds)
	}
	if got.HITLExpirationAction != identity.HITLExpirationApprove {
		t.Errorf("HITLExpirationAction = %q, want %q", got.HITLExpirationAction, identity.HITLExpirationApprove)
	}
}

// TestUpdateAgentHITLMode covers the Slice 7b sub-mode setter: default 'all',
// round-trip to 'high_impact', invalid mode rejected, cross-tenant rejected.
func TestUpdateAgentHITLMode(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "owner@hitlmode.example.com", "Owner", "google-hitlmode")
	store.ClaimOrCreateDomain(ctx, "hitlmode.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@hitlmode.example.com", "hitlmode.example.com", "", "", "", user.ID)

	// Fresh agent defaults to 'all' (column default, read via GetAgentByID).
	got, _ := store.GetAgentByID(ctx, a.ID)
	if got.HITLMode != "all" {
		t.Errorf("fresh HITLMode = %q, want all", got.HITLMode)
	}

	if err := store.UpdateAgentHITLMode(ctx, a.ID, user.ID, "high_impact"); err != nil {
		t.Fatalf("UpdateAgentHITLMode: %v", err)
	}
	got, _ = store.GetAgentByID(ctx, a.ID)
	if got.HITLMode != "high_impact" {
		t.Errorf("HITLMode = %q, want high_impact", got.HITLMode)
	}

	// Invalid mode → clean error, no mutation.
	if err := store.UpdateAgentHITLMode(ctx, a.ID, user.ID, "bogus"); err == nil {
		t.Error("expected error for invalid hitl_mode")
	}
	// Cross-tenant → error, no mutation.
	if err := store.UpdateAgentHITLMode(ctx, a.ID, "other-user", "all"); err == nil {
		t.Error("expected error updating another user's agent")
	}
	got, _ = store.GetAgentByID(ctx, a.ID)
	if got.HITLMode != "high_impact" {
		t.Errorf("HITLMode mutated by rejected updates: %q", got.HITLMode)
	}
}

func TestUpdateAgentHITLValidation(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-hitl-valid")
	store.ClaimOrCreateDomain(ctx, "hitl-valid.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@hitl-valid.example.com", "hitl-valid.example.com", "", "https://example.com/webhook", "", user.ID)

	cases := []struct {
		name   string
		ttl    int
		action string
	}{
		{"ttl zero", 0, identity.HITLExpirationReject},
		{"ttl above max", identity.HITLMaxTTLSeconds + 1, identity.HITLExpirationReject},
		{"ttl negative", -100, identity.HITLExpirationReject},
		{"bogus action", 3600, "maybe"},
		{"empty action", 3600, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := store.UpdateAgentHITL(ctx, a.ID, user.ID, true, tc.ttl, tc.action)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestUpdateAgentHITLNotOwned(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-hitl-notown")
	store.ClaimOrCreateDomain(ctx, "hitl-notown.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@hitl-notown.example.com", "hitl-notown.example.com", "", "https://example.com/webhook", "", user.ID)

	err := store.UpdateAgentHITL(ctx, a.ID, "different-user", true, 3600, identity.HITLExpirationApprove)
	if err == nil {
		t.Error("expected error when updating agent not owned by user")
	}
}

// TestHITLTTLDatabaseCap confirms the DB CHECK catches an out-of-range TTL even
// if a future caller bypasses the Go-side validation.
func TestHITLTTLDatabaseCap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-hitl-dbcap")
	store.ClaimOrCreateDomain(ctx, "hitl-dbcap.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@hitl-dbcap.example.com", "hitl-dbcap.example.com", "", "https://example.com/webhook", "", user.ID)

	_, err := pool.Exec(ctx,
		`UPDATE agent_identities SET hitl_ttl_seconds = $1 WHERE id = $2`,
		identity.HITLMaxTTLSeconds+1, a.ID,
	)
	if err == nil {
		t.Error("expected DB CHECK violation for ttl above max")
	}

	_, err = pool.Exec(ctx,
		`UPDATE agent_identities SET hitl_expiration_action = 'maybe' WHERE id = $1`,
		a.ID,
	)
	if err == nil {
		t.Error("expected DB CHECK violation for invalid expiration_action")
	}
}

// --- ListAgentsByUser includes HITL fields ---

func TestListAgentsByUserIncludesHITL(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-hitl-list")
	store.ClaimOrCreateDomain(ctx, "hitl-list.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@hitl-list.example.com", "hitl-list.example.com", "", "https://example.com/webhook", "", user.ID)
	if err := store.UpdateAgentHITL(ctx, a.ID, user.ID, true, 7200, identity.HITLExpirationApprove); err != nil {
		t.Fatalf("UpdateAgentHITL: %v", err)
	}

	agents, err := store.ListAgentsByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAgentsByUser: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if !agents[0].HITLEnabled || agents[0].HITLTTLSeconds != 7200 || agents[0].HITLExpirationAction != identity.HITLExpirationApprove {
		t.Errorf("HITL fields not populated from list: %+v", agents[0])
	}
}

// --- CreateOutboundMessage sets status='sent' ---

func TestCreateOutboundMessageStatusSent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-ob-status")
	store.ClaimOrCreateDomain(ctx, "ob-status.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@ob-status.example.com", "ob-status.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, err := store.CreateOutboundMessage(ctx, a.ID, []string{"alice@gmail.com"}, nil, nil, "Re: Hello", "reply", "smtp", "", "")
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	if msg.Status != identity.MessageStatusSent {
		t.Errorf("in-memory Status = %q, want %q", msg.Status, identity.MessageStatusSent)
	}

	var dbStatus string
	err = pool.QueryRow(ctx, `SELECT status FROM messages WHERE id = $1`, msg.ID).Scan(&dbStatus)
	if err != nil {
		t.Fatalf("read back status: %v", err)
	}
	if dbStatus != identity.MessageStatusSent {
		t.Errorf("DB status = %q, want %q", dbStatus, identity.MessageStatusSent)
	}
}

// --- CreatePendingOutboundMessage ---

func TestCreatePendingOutboundMessage(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-pending")
	store.ClaimOrCreateDomain(ctx, "pending.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@pending.example.com", "pending.example.com", "", "https://example.com/webhook", "", user.ID)

	attachments := []map[string]string{
		{"filename": "hello.txt", "content_type": "text/plain", "data": "aGVsbG8="},
	}
	attachmentsJSON, _ := json.Marshal(attachments)

	before := time.Now()
	msg, err := store.CreatePendingOutboundMessage(
		ctx, a.ID,
		[]string{"alice@example.com", "bob@example.com"},
		[]string{"carol@example.com"},
		[]string{"dave@example.com"},
		"Draft subject", "Plain body", "<p>HTML body</p>",
		attachmentsJSON,
		"reply", "conv_123", "<inbound-msgid@gmail.com>",
		3600,
	)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}
	after := time.Now()

	// In-memory shape
	if msg.Status != identity.MessageStatusPendingApproval {
		t.Errorf("Status = %q, want %q", msg.Status, identity.MessageStatusPendingApproval)
	}
	if msg.Direction != "outbound" {
		t.Errorf("Direction = %q, want outbound", msg.Direction)
	}
	if msg.Type != "reply" {
		t.Errorf("Type = %q, want reply", msg.Type)
	}
	if msg.EmailMessageID != "<inbound-msgid@gmail.com>" {
		t.Errorf("EmailMessageID = %q, want <inbound-msgid@gmail.com>", msg.EmailMessageID)
	}
	if msg.Recipient != "alice@example.com" {
		t.Errorf("Recipient = %q, want alice@example.com (first To)", msg.Recipient)
	}
	if msg.ApprovalExpiresAt == nil {
		t.Fatal("ApprovalExpiresAt is nil")
	}
	lower := before.Add(time.Hour - time.Second)
	upper := after.Add(time.Hour + time.Second)
	if msg.ApprovalExpiresAt.Before(lower) || msg.ApprovalExpiresAt.After(upper) {
		t.Errorf("ApprovalExpiresAt = %v, want within [%v, %v]", msg.ApprovalExpiresAt, lower, upper)
	}

	// DB round-trip: verify every column persisted.
	var (
		dbStatus          string
		dbSubject         string
		dbEmailMessageID  string
		dbBodyText        *string
		dbBodyHTML        *string
		dbAttachments     []byte
		dbTo, dbCC, dbBCC []string
		dbApprovalExpires time.Time
		dbReviewedAt      *time.Time
		dbRejectionReason *string
		dbEdited          bool
		dbConversationID  string
		dbMessageType     *string
	)
	err = pool.QueryRow(ctx,
		`SELECT status, subject, email_message_id, body_text, body_html, attachments_json,
		        to_recipients, cc, bcc,
		        approval_expires_at, reviewed_at, rejection_reason, edited,
		        conversation_id, message_type
		 FROM messages WHERE id = $1`, msg.ID,
	).Scan(&dbStatus, &dbSubject, &dbEmailMessageID, &dbBodyText, &dbBodyHTML, &dbAttachments,
		&dbTo, &dbCC, &dbBCC,
		&dbApprovalExpires, &dbReviewedAt, &dbRejectionReason, &dbEdited,
		&dbConversationID, &dbMessageType,
	)
	if err != nil {
		t.Fatalf("read back pending row: %v", err)
	}
	if dbStatus != identity.MessageStatusPendingApproval {
		t.Errorf("db status = %q, want %q", dbStatus, identity.MessageStatusPendingApproval)
	}
	if dbSubject != "Draft subject" {
		t.Errorf("db subject = %q", dbSubject)
	}
	if dbEmailMessageID != "<inbound-msgid@gmail.com>" {
		t.Errorf("db email_message_id = %q, want <inbound-msgid@gmail.com>", dbEmailMessageID)
	}
	if dbBodyText == nil || *dbBodyText != "Plain body" {
		t.Errorf("db body_text = %v, want 'Plain body'", dbBodyText)
	}
	if dbBodyHTML == nil || *dbBodyHTML != "<p>HTML body</p>" {
		t.Errorf("db body_html = %v", dbBodyHTML)
	}
	if len(dbAttachments) == 0 {
		t.Error("db attachments_json should be set")
	} else {
		var got []map[string]string
		if err := json.Unmarshal(dbAttachments, &got); err != nil {
			t.Fatalf("unmarshal attachments_json: %v", err)
		}
		if len(got) != 1 || got[0]["filename"] != "hello.txt" {
			t.Errorf("attachments round-trip mismatch: %v", got)
		}
	}
	if len(dbTo) != 2 || dbTo[0] != "alice@example.com" || dbTo[1] != "bob@example.com" {
		t.Errorf("db to_recipients = %v", dbTo)
	}
	if len(dbCC) != 1 || dbCC[0] != "carol@example.com" {
		t.Errorf("db cc = %v", dbCC)
	}
	if len(dbBCC) != 1 || dbBCC[0] != "dave@example.com" {
		t.Errorf("db bcc = %v", dbBCC)
	}
	if dbApprovalExpires.Before(lower) || dbApprovalExpires.After(upper) {
		t.Errorf("db approval_expires_at = %v, window [%v, %v]", dbApprovalExpires, lower, upper)
	}
	if dbReviewedAt != nil {
		t.Errorf("db reviewed_at = %v, want NULL at creation", dbReviewedAt)
	}
	if dbRejectionReason != nil {
		t.Errorf("db rejection_reason = %v, want NULL at creation", dbRejectionReason)
	}
	if dbEdited {
		t.Error("db edited should be false at creation")
	}
	if dbConversationID != "conv_123" {
		t.Errorf("db conversation_id = %q", dbConversationID)
	}
	if dbMessageType == nil || *dbMessageType != "reply" {
		t.Errorf("db message_type = %v, want reply", dbMessageType)
	}
}

func TestCreatePendingOutboundMessageEmptyBodyAsNull(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-pending-null")
	store.ClaimOrCreateDomain(ctx, "pending-null.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@pending-null.example.com", "pending-null.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, err := store.CreatePendingOutboundMessage(
		ctx, a.ID,
		[]string{"alice@example.com"}, nil, nil,
		"No body", "", "",
		nil,
		"send", "", "",
		600,
	)
	if err != nil {
		t.Fatalf("CreatePendingOutboundMessage: %v", err)
	}

	var bodyText, bodyHTML *string
	var attachments []byte
	err = pool.QueryRow(ctx,
		`SELECT body_text, body_html, attachments_json FROM messages WHERE id = $1`, msg.ID,
	).Scan(&bodyText, &bodyHTML, &attachments)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if bodyText != nil {
		t.Errorf("empty body_text should be stored as NULL, got %v", bodyText)
	}
	if bodyHTML != nil {
		t.Errorf("empty body_html should be stored as NULL, got %v", bodyHTML)
	}
	if attachments != nil {
		t.Errorf("nil attachments should be stored as NULL, got %v", attachments)
	}
}

func TestCreatePendingOutboundMessageInvalidTTL(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-pending-ttl")
	store.ClaimOrCreateDomain(ctx, "pending-ttl.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@pending-ttl.example.com", "pending-ttl.example.com", "", "https://example.com/webhook", "", user.ID)

	for _, ttl := range []int{0, -1, identity.HITLMaxTTLSeconds + 1} {
		_, err := store.CreatePendingOutboundMessage(
			ctx, a.ID,
			[]string{"alice@example.com"}, nil, nil,
			"x", "body", "", nil,
			"send", "", "", ttl,
		)
		if err == nil {
			t.Errorf("ttl=%d: expected error, got nil", ttl)
		}
	}
}

// TestMessageStatusDBCheck confirms the CHECK constraint rejects unknown status.
func TestMessageStatusDBCheck(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-status-check")
	store.ClaimOrCreateDomain(ctx, "status-check.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot@status-check.example.com", "status-check.example.com", "", "https://example.com/webhook", "", user.ID)
	msg, _ := store.CreateOutboundMessage(ctx, a.ID, []string{"alice@example.com"}, nil, nil, "x", "send", "smtp", "", "")

	_, err := pool.Exec(ctx, `UPDATE messages SET status = 'bogus' WHERE id = $1`, msg.ID)
	if err == nil {
		t.Error("expected DB CHECK violation for unknown status")
	}
}

// TestPendingApprovalIndex verifies the partial index created by migration 003
// actually exists; this is a cheap sanity check that migrations applied cleanly.
func TestPendingApprovalIndex(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()

	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM pg_indexes
		   WHERE schemaname = 'public'
		     AND tablename = 'messages'
		     AND indexname = 'idx_messages_pending_approval'
		 )`,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("pg_indexes lookup: %v", err)
	}
	if !exists {
		t.Error("idx_messages_pending_approval not found — migration 003 may not have applied")
	}
}
