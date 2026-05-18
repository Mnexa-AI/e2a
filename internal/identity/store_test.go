package identity_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func TestCreateAgent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-create-agent")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	if _, err := store.ClaimOrCreateDomain(ctx, "bot.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	a, err := store.CreateAgent(ctx, "agent@bot.example.com", "bot.example.com", "", "https://example.com/webhook", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if a.ID != "agent@bot.example.com" {
		t.Errorf("ID = %q, want %q", a.ID, "agent@bot.example.com")
	}
	if a.Domain != "bot.example.com" {
		t.Errorf("Domain = %q, want %q", a.Domain, "bot.example.com")
	}
	if a.DomainVerified {
		t.Error("expected DomainVerified=false for new agent")
	}
}

func TestCreateAgentDuplicate(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-dup-agent")
	store.ClaimOrCreateDomain(ctx, "dup.example.com", user.ID)

	store.CreateAgent(ctx, "agent@dup.example.com", "dup.example.com", "", "https://example.com/webhook", "", user.ID)
	_, err := store.CreateAgent(ctx, "agent@dup.example.com", "dup.example.com", "", "https://example.com/webhook2", "", user.ID)
	if err == nil {
		t.Error("expected error for duplicate agent")
	}
}

func TestGetAgentByID(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-agent-byid")
	store.ClaimOrCreateDomain(ctx, "agentid.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@agentid.example.com", "agentid.example.com", "", "https://example.com/webhook", "", user.ID)

	got, err := store.GetAgentByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got.Domain != "agentid.example.com" {
		t.Errorf("Domain = %q", got.Domain)
	}
}

func TestGetAgentByEmail(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-agent-byemail")
	store.ClaimOrCreateDomain(ctx, "lookup.example.com", user.ID)
	created, _ := store.CreateAgent(ctx, "agent@lookup.example.com", "lookup.example.com", "", "https://example.com/webhook", "", user.ID)

	got, err := store.GetAgentByEmail(ctx, "agent@lookup.example.com")
	if err != nil {
		t.Fatalf("GetAgentByEmail: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}
}

func TestCreateAPIKey(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "apikey-owner@example.com", "Owner", "google-apikey-create")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	key, err := store.CreateAPIKey(ctx, user.ID, "test key")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !strings.HasPrefix(key.ID, "apk_") {
		t.Errorf("ID should start with apk_, got %q", key.ID)
	}
	if !strings.HasPrefix(key.PlaintextKey, "e2a_") {
		t.Errorf("PlaintextKey should start with e2a_, got %q", key.PlaintextKey)
	}
	if key.UserID != user.ID {
		t.Errorf("UserID = %q, want %q", key.UserID, user.ID)
	}
	if key.Name != "test key" {
		t.Errorf("Name = %q, want %q", key.Name, "test key")
	}
}

func TestListAPIKeys(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "apikey-list@example.com", "Owner", "google-apikey-list")
	store.CreateAPIKey(ctx, user.ID, "key-1")
	store.CreateAPIKey(ctx, user.ID, "key-2")

	keys, err := store.ListAPIKeys(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestDeleteAPIKey(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "apikey-del@example.com", "Owner", "google-apikey-del")
	key, _ := store.CreateAPIKey(ctx, user.ID, "to-delete")

	err := store.DeleteAPIKey(ctx, key.ID, user.ID)
	if err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	keys, _ := store.ListAPIKeys(ctx, user.ID)
	if len(keys) != 0 {
		t.Errorf("expected 0 keys after delete, got %d", len(keys))
	}
}

func TestGetUserByAPIKey(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "apikey-lookup@example.com", "Owner", "google-apikey-lookup")
	key, _ := store.CreateAPIKey(ctx, user.ID, "lookup-key")

	got, err := store.GetUserByAPIKey(ctx, key.PlaintextKey)
	if err != nil {
		t.Fatalf("GetUserByAPIKey: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("ID = %q, want %q", got.ID, user.ID)
	}
	if got.Email != "apikey-lookup@example.com" {
		t.Errorf("Email = %q", got.Email)
	}
}

func TestUpdateAgentWebhook(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-update-wh")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	store.ClaimOrCreateDomain(ctx, "update-wh.example.com", user.ID)
	a, err := store.CreateAgent(ctx, "agent@update-wh.example.com", "update-wh.example.com", "", "https://example.com/old", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := store.UpdateAgentWebhook(ctx, a.ID, user.ID, "https://example.com/new"); err != nil {
		t.Fatalf("UpdateAgentWebhook: %v", err)
	}

	got, _ := store.GetAgentByEmail(ctx, "agent@update-wh.example.com")
	if got.WebhookURL != "https://example.com/new" {
		t.Errorf("WebhookURL = %q, want %q", got.WebhookURL, "https://example.com/new")
	}
}

func TestUpdateAgentWebhookNotOwned(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-update-wh-notown")
	store.ClaimOrCreateDomain(ctx, "notown-wh.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@notown-wh.example.com", "notown-wh.example.com", "", "https://example.com/webhook", "", user.ID)

	err := store.UpdateAgentWebhook(ctx, a.ID, "other-user", "https://evil.com")
	if err == nil {
		t.Error("expected error when updating agent not owned by user")
	}
}

func TestCreateAndGetInboundMessage(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-inbound")
	store.ClaimOrCreateDomain(ctx, "inbound.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@inbound.example.com", "inbound.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, err := store.CreateInboundMessage(ctx, "", a.ID, "alice@gmail.com", "bot@inbound.example.com", "<abc123@gmail.com>", "Hello Bot", "", "", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	if !strings.HasPrefix(msg.ID, "msg_") {
		t.Errorf("ID should start with msg_, got %q", msg.ID)
	}
	if msg.AgentID != a.ID {
		t.Errorf("AgentID = %q, want %q", msg.AgentID, a.ID)
	}
	if msg.Direction != "inbound" {
		t.Errorf("Direction = %q, want inbound", msg.Direction)
	}
	if msg.Sender != "alice@gmail.com" {
		t.Errorf("Sender = %q", msg.Sender)
	}
	if msg.EmailMessageID != "<abc123@gmail.com>" {
		t.Errorf("EmailMessageID = %q", msg.EmailMessageID)
	}
	if msg.Subject != "Hello Bot" {
		t.Errorf("Subject = %q", msg.Subject)
	}

	got, err := store.GetInboundMessage(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetInboundMessage: %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("ID mismatch")
	}
}

func TestInboundMessageRoundTripsToCcLists(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "tcc-owner@example.com", "Owner", "google-tcc")
	store.ClaimOrCreateDomain(ctx, "tcc.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "bot-a@tcc.example.com", "tcc.example.com", "", "https://example.com/webhook", "", user.ID)

	to := []string{"bot-a@tcc.example.com", "bot-b@tcc.example.com"}
	cc := []string{"watcher@example.com", "audit@example.com"}

	msg, err := store.CreateInboundMessage(ctx, "", a.ID, "alice@gmail.com", "bot-a@tcc.example.com", "<x@gmail.com>", "Group thread", "", "", nil, nil, to, cc)
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	got, err := store.GetInboundMessage(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetInboundMessage: %v", err)
	}
	if !reflect.DeepEqual(got.ToRecipients, to) {
		t.Errorf("ToRecipients = %v, want %v", got.ToRecipients, to)
	}
	if !reflect.DeepEqual(got.CC, cc) {
		t.Errorf("CC = %v, want %v", got.CC, cc)
	}
}

func TestGetInboundMessageNotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	_, err := store.GetInboundMessage(context.Background(), "msg_nonexistent")
	if err == nil {
		t.Error("expected error for non-existent inbound message")
	}
}

func TestGetInboundMessageExpired(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-expired-inbound")
	store.ClaimOrCreateDomain(ctx, "expired-inbound.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@expired-inbound.example.com", "expired-inbound.example.com", "", "https://example.com/webhook", "", user.ID)
	msg, _ := store.CreateInboundMessage(ctx, "", a.ID, "alice@gmail.com", "bot@expired-inbound.example.com", "", "", "", "", nil, nil, nil, nil)

	// Set expiry to the past
	pool.Exec(ctx, `UPDATE messages SET expires_at = $1 WHERE id = $2`, time.Now().Add(-1*time.Hour), msg.ID)

	_, err := store.GetInboundMessage(ctx, msg.ID)
	if err == nil {
		t.Error("expected error for expired inbound message")
	}
}

func TestDeleteExpiredMessages(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-cleanup-inbound")
	store.ClaimOrCreateDomain(ctx, "cleanup-inbound.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@cleanup-inbound.example.com", "cleanup-inbound.example.com", "", "https://example.com/webhook", "", user.ID)
	msg, _ := store.CreateInboundMessage(ctx, "", a.ID, "alice@gmail.com", "bot@cleanup-inbound.example.com", "", "", "", "", nil, nil, nil, nil)

	// Set expiry to the past
	pool.Exec(ctx, `UPDATE messages SET expires_at = $1 WHERE id = $2`, time.Now().Add(-1*time.Hour), msg.ID)

	deleted, err := store.DeleteExpiredMessages(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredMessages: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
}

func TestCreateOutboundMessage(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-outbound")
	store.ClaimOrCreateDomain(ctx, "outbound.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@outbound.example.com", "outbound.example.com", "", "https://example.com/webhook", "", user.ID)

	msg, err := store.CreateOutboundMessage(ctx, a.ID, []string{"alice@gmail.com"}, nil, nil, "Re: Hello", "reply", "smtp", "", "")
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	if msg.Direction != "outbound" {
		t.Errorf("Direction = %q, want outbound", msg.Direction)
	}
	if msg.Method != "smtp" {
		t.Errorf("Method = %q", msg.Method)
	}
	if msg.Type != "reply" {
		t.Errorf("Type = %q", msg.Type)
	}
	if msg.Recipient != "alice@gmail.com" {
		t.Errorf("Recipient = %q, want alice@gmail.com", msg.Recipient)
	}
	if len(msg.ToRecipients) != 1 || msg.ToRecipients[0] != "alice@gmail.com" {
		t.Errorf("ToRecipients = %v, want [alice@gmail.com]", msg.ToRecipients)
	}
}

func TestListActivityByAgent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-activity")
	store.ClaimOrCreateDomain(ctx, "activity.example.com", user.ID)
	a, _ := store.CreateAgent(ctx, "agent@activity.example.com", "activity.example.com", "", "https://example.com/webhook", "", user.ID)

	store.CreateInboundMessage(ctx, "", a.ID, "alice@gmail.com", "bot@activity.example.com", "", "Hello", "", "", nil, nil, nil, nil)
	store.CreateOutboundMessage(ctx, a.ID, []string{"alice@gmail.com"}, nil, nil, "Re: Hello", "reply", "smtp", "", "")
	store.CreateInboundMessage(ctx, "", a.ID, "bob@gmail.com", "bot@activity.example.com", "", "Hi", "", "", nil, nil, nil, nil)

	activity, err := store.ListActivityByAgent(ctx, a.ID, 50)
	if err != nil {
		t.Fatalf("ListActivityByAgent: %v", err)
	}
	if len(activity) != 3 {
		t.Fatalf("got %d activities, want 3", len(activity))
	}
	// Most recent first
	if activity[0].Subject != "Hi" {
		t.Errorf("first activity subject = %q, want Hi", activity[0].Subject)
	}
	if activity[1].Direction != "outbound" {
		t.Errorf("second activity direction = %q, want outbound", activity[1].Direction)
	}
}

// -- AgentIdentity helper method tests --

func TestIsSharedDomain(t *testing.T) {
	const sharedDomain = "agents.example.com"
	tests := []struct {
		name         string
		domain       string
		sharedDomain string
		want         bool
	}{
		{"custom domain not shared", "tenant.example.com", sharedDomain, false},
		{"matches configured shared domain", sharedDomain, sharedDomain, true},
		{"empty domain", "", sharedDomain, false},
		{"shared domain unconfigured", sharedDomain, "", false},
	}
	for _, tt := range tests {
		a := &identity.AgentIdentity{Domain: tt.domain}
		if got := a.IsSharedDomain(tt.sharedDomain); got != tt.want {
			t.Errorf("%s: IsSharedDomain(domain=%q, shared=%q) = %v, want %v", tt.name, tt.domain, tt.sharedDomain, got, tt.want)
		}
	}
}

func TestActualDomain(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "example.com"},
		{"agents.e2a.dev", "agents.e2a.dev"},
		{"", ""},
	}
	for _, tt := range tests {
		a := &identity.AgentIdentity{Domain: tt.domain}
		if got := a.ActualDomain(); got != tt.want {
			t.Errorf("ActualDomain(%q) = %q, want %q", tt.domain, got, tt.want)
		}
	}
}

func TestEmailAddress(t *testing.T) {
	tests := []struct {
		id     string
		domain string
		want   string
	}{
		{"agent@example.com", "example.com", "agent@example.com"},
		{"support@mail.co.com", "mail.co.com", "support@mail.co.com"},
		{"my-bot@agents.e2a.dev", "agents.e2a.dev", "my-bot@agents.e2a.dev"},
		{"test@agents.e2a.dev", "agents.e2a.dev", "test@agents.e2a.dev"},
	}
	for _, tt := range tests {
		a := &identity.AgentIdentity{ID: tt.id, Domain: tt.domain}
		if got := a.EmailAddress(); got != tt.want {
			t.Errorf("EmailAddress(id=%q, domain=%q) = %q, want %q", tt.id, tt.domain, got, tt.want)
		}
	}
}

func TestCreateAgentSharedDomain(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@example.com", "Owner", "google-shared-domain")
	a, err := store.CreateAgent(ctx, "oc-bot@agents.e2a.dev", "agents.e2a.dev", "", "https://gateway.fly.dev/hooks/agent", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent shared domain: %v", err)
	}
	if a.ID != "oc-bot@agents.e2a.dev" {
		t.Errorf("ID = %q, want %q", a.ID, "oc-bot@agents.e2a.dev")
	}
	if a.Domain != "agents.e2a.dev" {
		t.Errorf("Domain = %q, want %q", a.Domain, "agents.e2a.dev")
	}
	if !a.IsSharedDomain("agents.e2a.dev") {
		t.Error("expected IsSharedDomain() = true for shared-domain agent")
	}
}

// TestLookupConversationID_EmailThread simulates the production scenario:
//
//	1. Human sends first email → inbound stored with email_message_id, no conversation_id
//	2. Agent replies → outbound stored with provider_message_id (bare SES ID) and conversation_id
//	3. Human replies again → In-Reply-To references the SES Message-ID with @region suffix
//
// The lookup must match the second inbound's In-Reply-To against the outbound's
// provider_message_id using prefix matching, and return the conversation_id.
func TestLookupConversationID_EmailThread(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-thread@example.com", "Owner", "google-thread")
	store.ClaimOrCreateDomain(ctx, "thread.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@thread.example.com", "thread.example.com", "", "https://example.com/webhook", "", user.ID)

	// 1. First inbound email — no conversation_id yet
	_, err := store.CreateInboundMessage(ctx, "", agent.ID,
		"alice@gmail.com", "bot@thread.example.com",
		"<CAMCKtby_first@mail.gmail.com>", "Hello",
		"", // no conversation_id on first message
		"pending", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}

	// 2. Agent replies — mnexa sets conversation_id, SES returns bare Message-ID
	mnexa_conv_id := "e0533ec4-af43-4dea-9cc7-be6fff5cf440"
	_, err = store.CreateOutboundMessage(ctx, agent.ID,
		[]string{"alice@gmail.com"}, nil, nil, "Re: Hello",
		"reply", "smtp",
		"<010f019d4b3843be-53882e6f-46de-4221-a56a-ba993e8f83e8-000000>", // bare SES ID (no @region)
		mnexa_conv_id)
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}

	// 3. Human replies — Gmail's In-Reply-To has the full SES Message-ID with @region
	//    References includes both the original inbound and the agent's reply
	inReplyTo := "<010f019d4b3843be-53882e6f-46de-4221-a56a-ba993e8f83e8-000000@us-east-2.amazonses.com>"
	references := "<CAMCKtby_first@mail.gmail.com>"

	lookupIDs := []string{inReplyTo, references}
	got, err := store.LookupConversationID(ctx, agent.ID, lookupIDs)
	if err != nil {
		t.Fatalf("LookupConversationID failed: %v", err)
	}
	if got != mnexa_conv_id {
		t.Errorf("LookupConversationID = %q, want %q", got, mnexa_conv_id)
	}
}

// TestLookupConversationID_ExactMatch verifies that exact matches on
// email_message_id and provider_message_id still work.
func TestLookupConversationID_ExactMatch(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-exact@example.com", "Owner", "google-exact")
	store.ClaimOrCreateDomain(ctx, "exact.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@exact.example.com", "exact.example.com", "", "https://example.com/webhook", "", user.ID)

	convID := "conv-exact-123"

	// Outbound with full SES Message-ID (angle brackets + domain)
	_, err := store.CreateOutboundMessage(ctx, agent.ID,
		[]string{"alice@gmail.com"}, nil, nil, "Hello",
		"send", "smtp",
		"<abc123@us-east-2.amazonses.com>",
		convID)
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}

	// Exact match on provider_message_id
	got, err := store.LookupConversationID(ctx, agent.ID, []string{"<abc123@us-east-2.amazonses.com>"})
	if err != nil {
		t.Fatalf("LookupConversationID exact match failed: %v", err)
	}
	if got != convID {
		t.Errorf("got %q, want %q", got, convID)
	}
}

// TestLookupConversationID_NoMatch verifies that lookup returns an error
// when no matching messages exist.
func TestLookupConversationID_NoMatch(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner-nomatch@example.com", "Owner", "google-nomatch")
	store.ClaimOrCreateDomain(ctx, "nomatch.example.com", user.ID)
	agent, _ := store.CreateAgent(ctx, "bot@nomatch.example.com", "nomatch.example.com", "", "https://example.com/webhook", "", user.ID)

	_, err := store.LookupConversationID(ctx, agent.ID, []string{"<nonexistent@example.com>"})
	if err == nil {
		t.Error("expected error for non-matching lookup, got nil")
	}
}

// --- Per-user webhook signing secrets ---

func TestCreateOrGetUser_AutoCreatesSigningSecret(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "auto-secret@example.com", "Auto", "google-auto-secret")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	secrets, err := store.ListSigningSecrets(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListSigningSecrets: %v", err)
	}
	if len(secrets) != 1 {
		t.Fatalf("expected 1 default secret, got %d", len(secrets))
	}
	if secrets[0].Name != "default" {
		t.Errorf("default secret name = %q, want \"default\"", secrets[0].Name)
	}
	if secrets[0].SecretPrefix == "" {
		t.Error("SecretPrefix should be set on list")
	}
	// Plaintext is exposed on List so the dashboard can render the full
	// value on demand. The first 12 chars must equal the prefix.
	if len(secrets[0].Secret) != 64 {
		t.Errorf("plaintext Secret should be 64 hex chars on list, got len=%d", len(secrets[0].Secret))
	}
	if secrets[0].Secret[:12] != secrets[0].SecretPrefix {
		t.Errorf("Secret prefix mismatch: Secret[:12]=%q, SecretPrefix=%q", secrets[0].Secret[:12], secrets[0].SecretPrefix)
	}
}

func TestCreateSigningSecret_RoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "round-trip@example.com", "RT", "google-round-trip")

	created, err := store.CreateSigningSecret(ctx, user.ID, "rollover-2026-04")
	if err != nil {
		t.Fatalf("CreateSigningSecret: %v", err)
	}
	if !strings.HasPrefix(created.ID, "wsec_") {
		t.Errorf("ID should start with wsec_, got %q", created.ID)
	}
	if len(created.Secret) != 64 {
		t.Errorf("Secret should be 64 hex chars, got len=%d", len(created.Secret))
	}
	if created.Name != "rollover-2026-04" {
		t.Errorf("Name = %q", created.Name)
	}

	// GetUserSigningSecrets returns the plaintext + IDs for the relay/verifier
	secrets, err := store.GetUserSigningSecrets(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetUserSigningSecrets: %v", err)
	}
	// Default + the one we just created → 2 secrets.
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secrets))
	}
	// Most-recent-first: the one we just created should be at [0].
	if secrets[0].Secret != created.Secret {
		t.Errorf("most-recent secret value should be the just-created one")
	}
	if secrets[0].ID != created.ID {
		t.Errorf("most-recent secret ID = %q, want %q", secrets[0].ID, created.ID)
	}
}

func TestCreateSigningSecret_HardCap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "cap@example.com", "Cap", "google-cap")

	// Default secret was auto-created → 1. Create up to MaxSigningSecretsPerUser.
	for i := 0; i < identity.MaxSigningSecretsPerUser-1; i++ {
		if _, err := store.CreateSigningSecret(ctx, user.ID, ""); err != nil {
			t.Fatalf("create secret %d: %v", i, err)
		}
	}
	// One more should fail.
	_, err := store.CreateSigningSecret(ctx, user.ID, "over-the-line")
	if err == nil {
		t.Fatal("expected cap error, got nil")
	}
	if !errors.Is(err, identity.ErrSigningSecretCapReached) {
		t.Errorf("expected ErrSigningSecretCapReached, got: %v", err)
	}
}

// Empty Name should be normalized server-side so the dashboard always
// has something readable to render.
func TestCreateSigningSecret_EmptyNameDefaults(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "empty-name@example.com", "EN", "google-empty-name")

	created, err := store.CreateSigningSecret(ctx, user.ID, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "unnamed" {
		t.Errorf("Name should default to \"unnamed\", got %q", created.Name)
	}
}

func TestDeleteSigningSecret_RefusesLast(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "last-secret@example.com", "Last", "google-last-secret")

	secrets, _ := store.ListSigningSecrets(ctx, user.ID)
	if len(secrets) != 1 {
		t.Fatalf("setup expected 1 default secret, got %d", len(secrets))
	}
	err := store.DeleteSigningSecret(ctx, secrets[0].ID, user.ID)
	if err == nil {
		t.Fatal("deleting the last secret should fail")
	}
	if !errors.Is(err, identity.ErrCannotDeleteLastSigningSecret) {
		t.Errorf("expected ErrCannotDeleteLastSigningSecret, got: %v", err)
	}
}

func TestDeleteSigningSecret_HappyPath(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "delete-happy@example.com", "Del", "google-delete-happy")

	created, _ := store.CreateSigningSecret(ctx, user.ID, "extra")
	if err := store.DeleteSigningSecret(ctx, created.ID, user.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	secrets, _ := store.ListSigningSecrets(ctx, user.ID)
	if len(secrets) != 1 {
		t.Errorf("after delete, expected 1 remaining (the default), got %d", len(secrets))
	}
}

func TestDeleteSigningSecret_NotOwned(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	owner, _ := store.CreateOrGetUser(ctx, "owner-rls@example.com", "Owner", "google-rls-owner")
	other, _ := store.CreateOrGetUser(ctx, "other-rls@example.com", "Other", "google-rls-other")

	created, _ := store.CreateSigningSecret(ctx, owner.ID, "owner-only")
	// Other user tries to delete owner's secret → must fail.
	err := store.DeleteSigningSecret(ctx, created.ID, other.ID)
	if err == nil {
		t.Fatal("delete by non-owner should fail")
	}
}

func TestEnsureUserHasSigningSecret_Idempotent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "idem@example.com", "Idem", "google-idem")

	// Already has one (auto-created).
	for i := 0; i < 3; i++ {
		if err := store.EnsureUserHasSigningSecret(ctx, user.ID); err != nil {
			t.Fatalf("ensure call %d: %v", i, err)
		}
	}
	secrets, _ := store.ListSigningSecrets(ctx, user.ID)
	if len(secrets) != 1 {
		t.Errorf("ensure should be idempotent, got %d secrets", len(secrets))
	}
}

// --- Concurrency: TOCTOU race on the cap is closed ---

func TestCreateSigningSecret_ConcurrentCreates_NeverExceedCap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "race@example.com", "Race", "google-race")

	// Default secret already exists → cap headroom is MaxSigningSecretsPerUser-1.
	// Fire 4× the headroom of concurrent creates and assert we never end
	// above the cap. Without the row lock these would interleave count
	// checks and over-create.
	const concurrency = 4 * (identity.MaxSigningSecretsPerUser)
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(i int) {
			defer wg.Done()
			_, _ = store.CreateSigningSecret(ctx, user.ID, fmt.Sprintf("race-%d", i))
		}(i)
	}
	wg.Wait()

	secrets, err := store.ListSigningSecrets(ctx, user.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(secrets) > identity.MaxSigningSecretsPerUser {
		t.Errorf("cap broken: %d secrets, max %d", len(secrets), identity.MaxSigningSecretsPerUser)
	}
}

func TestDeleteSigningSecret_ConcurrentDeletes_NeverGoBelowOne(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "del-race@example.com", "DR", "google-del-race")

	// Create 3 extra so the user has 4 total. Fire 4 concurrent deletes
	// for those 3 IDs (one delete will target a non-existent ID after a
	// successful sibling delete). The floor (1 secret) must hold.
	created := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		s, err := store.CreateSigningSecret(ctx, user.ID, fmt.Sprintf("d-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, s.ID)
	}

	var wg sync.WaitGroup
	for _, id := range created {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = store.DeleteSigningSecret(ctx, id, user.ID)
		}(id)
	}
	wg.Wait()

	secrets, _ := store.ListSigningSecrets(ctx, user.ID)
	if len(secrets) < 1 {
		t.Errorf("floor broken: %d secrets remain", len(secrets))
	}
}

// --- TouchSigningSecretLastSigned ---

func TestTouchSigningSecretLastSigned(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "touch@example.com", "T", "google-touch")

	secrets, _ := store.ListSigningSecrets(ctx, user.ID)
	if len(secrets) != 1 {
		t.Fatalf("setup: expected 1 default secret, got %d", len(secrets))
	}
	if secrets[0].LastSignedAt != nil {
		t.Errorf("LastSignedAt should start nil, got %v", secrets[0].LastSignedAt)
	}

	if err := store.TouchSigningSecretLastSigned(ctx, secrets[0].ID); err != nil {
		t.Fatalf("touch: %v", err)
	}

	refreshed, _ := store.ListSigningSecrets(ctx, user.ID)
	if refreshed[0].LastSignedAt == nil {
		t.Fatal("LastSignedAt still nil after touch")
	}
	if time.Since(*refreshed[0].LastSignedAt) > 5*time.Second {
		t.Errorf("LastSignedAt should be ~now, got %v", *refreshed[0].LastSignedAt)
	}
}

// --- GetUserSigningSecrets (with IDs) ordering contract ---

func TestGetUserSigningSecrets_MostRecentFirst(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "order@example.com", "O", "google-order")

	// User starts with one default. Add a "rolling" then a "current" secret.
	rolling, _ := store.CreateSigningSecret(ctx, user.ID, "rolling")
	time.Sleep(2 * time.Millisecond) // ensure distinct created_at
	current, _ := store.CreateSigningSecret(ctx, user.ID, "current")

	got, err := store.GetUserSigningSecrets(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 secrets, got %d", len(got))
	}
	if got[0].ID != current.ID {
		t.Errorf("[0] should be most recent (%q), got %q", current.ID, got[0].ID)
	}
	if got[1].ID != rolling.ID {
		t.Errorf("[1] should be the middle one (%q), got %q", rolling.ID, got[1].ID)
	}
}
