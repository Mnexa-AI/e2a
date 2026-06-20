package identity_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// seedUserData creates one user with: 1 verified domain, 2 agents (one
// custom-domain, one shared-domain), 1 inbound + 1 outbound message,
// 2 API keys, 1 user_session, and 2 usage_events. Returns the user so
// the caller can drive Export/Delete against a known fixture.
func seedUserData(t *testing.T, store *identity.Store, ctx context.Context, label string) *identity.User {
	t.Helper()

	user, err := store.CreateOrGetUser(ctx, label+"@example.com", label, "google-"+label)
	if err != nil {
		t.Fatalf("seed: CreateOrGetUser: %v", err)
	}

	if _, err := store.ClaimOrCreateDomain(ctx, label+".example.com", user.ID); err != nil {
		t.Fatalf("seed: ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, label+".example.com", user.ID); err != nil {
		t.Fatalf("seed: VerifyDomain: %v", err)
	}

	customAgent, err := store.CreateAgent(ctx,
		"bot@"+label+".example.com", label+".example.com", "Bot",
		"https://"+label+".example.com/hook", "cloud", user.ID)
	if err != nil {
		t.Fatalf("seed: CreateAgent custom: %v", err)
	}

	// Inbound message on the custom-domain agent. Populate reply_to so
	// the export covers the GDPR-Art.15 right-of-access claim that all
	// stored data about the user is returned.
	if _, err := store.CreateInboundMessage(ctx,
		"msg_in_"+label, customAgent.ID,
		"alice@gmail.com", customAgent.EmailAddress(),
		"<orig@gmail.com>", "Hi there", "", "delivered",
		[]byte("From: alice@gmail.com\r\nSubject: Hi\r\n\r\nbody"),
		map[string]string{"X-E2A-Auth-Verified": "true"},
		nil,
		false, "",
		nil, nil, []string{"real-alice@example.com"},
		identity.InboundScreening{}); err != nil {
		t.Fatalf("seed: CreateInboundMessage: %v", err)
	}

	// Outbound message on the same agent.
	if _, err := store.CreateOutboundMessage(ctx, customAgent.ID,
		[]string{"alice@gmail.com"}, nil, nil,
		"Re: Hi", "reply", "smtp", "<sent@e2a.example>", "", nil); err != nil {
		t.Fatalf("seed: CreateOutboundMessage: %v", err)
	}

	if _, err := store.CreateAPIKey(ctx, user.ID, "primary", nil); err != nil {
		t.Fatalf("seed: CreateAPIKey 1: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, user.ID, "ci", nil); err != nil {
		t.Fatalf("seed: CreateAPIKey 2: %v", err)
	}

	if _, err := store.CreateUserSession(ctx, user.ID); err != nil {
		t.Fatalf("seed: CreateUserSession: %v", err)
	}

	return user
}

// TestExportUserData verifies the right-of-access flow. The export
// should contain the user's profile, every domain/agent/key/message
// they own, and exclude internal identifiers (google_subject, key
// hashes, session tokens).
func TestExportUserData(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user := seedUserData(t, store, ctx, "exporter")

	dump, err := store.ExportUserData(ctx, user.ID)
	if err != nil {
		t.Fatalf("ExportUserData: %v", err)
	}

	if dump.User.ID != user.ID || dump.User.Email != user.Email {
		t.Errorf("user mismatch: got id=%s email=%s, want %s/%s",
			dump.User.ID, dump.User.Email, user.ID, user.Email)
	}
	if len(dump.Domains) != 1 {
		t.Errorf("domains: got %d, want 1", len(dump.Domains))
	}
	if len(dump.Agents) != 1 {
		t.Errorf("agents: got %d, want 1", len(dump.Agents))
	}
	if len(dump.APIKeys) != 2 {
		t.Errorf("api_keys: got %d, want 2", len(dump.APIKeys))
	}
	if len(dump.Messages) != 2 {
		t.Errorf("messages: got %d, want 2 (1 inbound + 1 outbound)", len(dump.Messages))
	}

	// Right-of-access requires every stored header field to round-trip
	// through the export. Reply-To regression-guard: if a future SELECT
	// drops the column, the user's export silently loses data — exactly
	// the kind of gap that fails a data-rights audit.
	var inbound *identity.Message
	for i := range dump.Messages {
		if dump.Messages[i].Direction == "inbound" {
			inbound = &dump.Messages[i]
			break
		}
	}
	if inbound == nil {
		t.Fatal("no inbound message in export")
	}
	wantReplyTo := []string{"real-alice@example.com"}
	if !reflect.DeepEqual(inbound.ReplyTo, wantReplyTo) {
		t.Errorf("inbound ReplyTo in export = %v, want %v", inbound.ReplyTo, wantReplyTo)
	}

	// Confirm the export doesn't leak internal identifiers. We marshal
	// to JSON because the most likely accidental leak path is a struct
	// field with a `json:` tag we forgot.
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	if jsonContains(raw, "google_subject") {
		t.Error("export leaks google_subject — that's an internal OAuth identifier")
	}
	if jsonContains(raw, "key_hash") {
		t.Error("export leaks key_hash — that's a credential equivalent")
	}
	// Schema metadata should be present so consumers can detect format
	// changes across versions.
	if dump.SchemaVersion == "" {
		t.Error("export is missing schema_version — consumers can't detect format changes")
	}
	if dump.GeneratedAt.IsZero() {
		t.Error("export is missing generated_at")
	}
}

// TestDeleteUserData verifies the right-of-deletion flow does the full
// cascade and reports per-table counts. After deletion, the user record
// must be gone and orphan rows must not exist for any of: domains,
// agents, messages, api_keys, sessions, usage events/summaries.
func TestDeleteUserData(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user := seedUserData(t, store, ctx, "deleter")

	res, err := store.DeleteUserData(ctx, user.ID)
	if err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}

	if !res.UserDeleted {
		t.Error("UserDeleted should be true")
	}
	if res.DomainsDeleted != 1 {
		t.Errorf("DomainsDeleted = %d, want 1", res.DomainsDeleted)
	}
	if res.AgentsDeleted != 1 {
		t.Errorf("AgentsDeleted = %d, want 1", res.AgentsDeleted)
	}
	if res.MessagesDeleted != 2 {
		t.Errorf("MessagesDeleted = %d, want 2", res.MessagesDeleted)
	}
	if res.APIKeysDeleted != 2 {
		t.Errorf("APIKeysDeleted = %d, want 2", res.APIKeysDeleted)
	}
	if res.SessionsDeleted != 1 {
		t.Errorf("SessionsDeleted = %d, want 1", res.SessionsDeleted)
	}

	// Verify the user row itself is gone.
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, user.ID,
	).Scan(&exists); err != nil {
		t.Fatalf("post-delete user exists: %v", err)
	}
	if exists {
		t.Error("user row still exists after DeleteUserData")
	}

	// Verify no orphan rows in any user-scoped table. The cascade is in
	// the schema (ON DELETE CASCADE), but a regression that drops a
	// cascade clause would leak rows — checking explicitly catches it.
	checks := []struct {
		name  string
		query string
	}{
		{"domains", `SELECT count(*) FROM domains WHERE user_id = $1`},
		{"agents", `SELECT count(*) FROM agent_identities WHERE user_id = $1`},
		{"api_keys", `SELECT count(*) FROM api_keys WHERE user_id = $1`},
		{"sessions", `SELECT count(*) FROM user_sessions WHERE user_id = $1`},
		{"usage_events", `SELECT count(*) FROM usage_events WHERE user_id = $1`},
		{"usage_summaries", `SELECT count(*) FROM usage_summaries WHERE user_id = $1`},
	}
	for _, c := range checks {
		var n int
		if err := pool.QueryRow(ctx, c.query, user.ID).Scan(&n); err != nil {
			t.Fatalf("post-delete count(%s): %v", c.name, err)
		}
		if n != 0 {
			t.Errorf("orphan rows in %s: %d (cascade missed)", c.name, n)
		}
	}
}

// TestDeleteUserData_DoesNotAffectOtherUsers is the cross-tenant
// isolation check. Two users; deleting one must not touch the other's
// data even when both have the same domain/agent shape.
func TestDeleteUserData_DoesNotAffectOtherUsers(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	target := seedUserData(t, store, ctx, "target")
	bystander := seedUserData(t, store, ctx, "bystander")

	if _, err := store.DeleteUserData(ctx, target.ID); err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}

	// Bystander should still be intact.
	dump, err := store.ExportUserData(ctx, bystander.ID)
	if err != nil {
		t.Fatalf("bystander export: %v", err)
	}
	if dump.User.ID != bystander.ID {
		t.Errorf("bystander user wrong: got %s, want %s", dump.User.ID, bystander.ID)
	}
	if len(dump.Agents) != 1 {
		t.Errorf("bystander agents: got %d, want 1 — cross-tenant cascade leaked", len(dump.Agents))
	}
	if len(dump.Messages) != 2 {
		t.Errorf("bystander messages: got %d, want 2 — cross-tenant cascade leaked", len(dump.Messages))
	}
}

// TestDeleteUserData_NoSuchUser returns UserDeleted=false rather than
// erroring, so the operation is idempotent — re-running a deletion
// after a partial failure or a duplicate request doesn't blow up.
func TestDeleteUserData_NoSuchUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	res, err := store.DeleteUserData(ctx, "usr_does_not_exist")
	if err != nil {
		t.Fatalf("DeleteUserData on missing user should not error, got: %v", err)
	}
	if res.UserDeleted {
		t.Error("UserDeleted should be false for non-existent user")
	}
}

// TestExportUserData_AfterDeletion ensures the export query handles a
// deleted user gracefully (returns an error rather than panicking on
// the missing user row).
func TestExportUserData_AfterDeletion(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user := seedUserData(t, store, ctx, "ghost")
	if _, err := store.DeleteUserData(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUserData: %v", err)
	}

	if _, err := store.ExportUserData(ctx, user.ID); err == nil {
		t.Error("ExportUserData on deleted user should error, got nil")
	}
}

// jsonContains reports whether the JSON byte slice contains the given
// field name as a top-level or nested key. Naive substring match is
// fine for this test — we're only checking that internal identifiers
// don't appear anywhere in the export.
func jsonContains(b []byte, field string) bool {
	// Quote the field so we match `"field":` and not the substring
	// inside a value that happens to contain the word.
	needle := []byte("\"" + field + "\":")
	return containsBytes(b, needle)
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// silence unused-import lint when the time package isn't otherwise used.
var _ = time.Time{}
