package identity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// Slice 5a — scope machinery. These DB-backed tests pin the credential-scope
// contract: minting, prefixes, the resolved principal, legacy backfill, and the
// cross-user / binding guards.

func setupScopeUserAgent(t *testing.T, slug string) (*identity.Store, *identity.User, *identity.AgentIdentity) {
	t.Helper()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "owner-"+slug+"@example.com", "Owner", "google-"+slug)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	dom := slug + ".example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, dom, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	ag, err := store.CreateAgent(ctx, "agent@"+dom, dom, "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return store, user, ag
}

func TestCreateAPIKey_DefaultsToAccountScope(t *testing.T) {
	store, user, _ := setupScopeUserAgent(t, "scope-acct")
	ctx := context.Background()

	key, err := store.CreateAPIKey(ctx, user.ID, "default", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if key.Scope != identity.ScopeAccount {
		t.Errorf("scope = %q, want account", key.Scope)
	}
	if key.AgentID != nil {
		t.Errorf("account key AgentID = %v, want nil", key.AgentID)
	}
	if !strings.HasPrefix(key.PlaintextKey, "e2a_acct_") {
		t.Errorf("account key prefix = %q, want e2a_acct_", key.PlaintextKey[:12])
	}

	p, err := store.GetPrincipalByAPIKey(ctx, key.PlaintextKey)
	if err != nil {
		t.Fatalf("GetPrincipalByAPIKey: %v", err)
	}
	if p.Scope != identity.ScopeAccount || p.AgentID != "" || p.User.ID != user.ID {
		t.Errorf("principal = %+v, want account/empty-agent/%s", p, user.ID)
	}
}

func TestCreateScopedAPIKey_Agent(t *testing.T) {
	store, user, ag := setupScopeUserAgent(t, "scope-agent")
	ctx := context.Background()

	key, err := store.CreateScopedAPIKey(ctx, user.ID, "runtime", identity.ScopeAgent, ag.ID, nil)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey(agent): %v", err)
	}
	if key.Scope != identity.ScopeAgent {
		t.Errorf("scope = %q, want agent", key.Scope)
	}
	if key.AgentID == nil || *key.AgentID != ag.ID {
		t.Errorf("agent key AgentID = %v, want %s", key.AgentID, ag.ID)
	}
	if !strings.HasPrefix(key.PlaintextKey, "e2a_agt_") {
		t.Errorf("agent key prefix = %q, want e2a_agt_", key.PlaintextKey[:11])
	}

	p, err := store.GetPrincipalByAPIKey(ctx, key.PlaintextKey)
	if err != nil {
		t.Fatalf("GetPrincipalByAPIKey: %v", err)
	}
	if p.Scope != identity.ScopeAgent || p.AgentID != ag.ID {
		t.Errorf("principal = %+v, want agent scope bound to %s", p, ag.ID)
	}
}

// TestCreateScopedAPIKey_Guards: an agent key must name an owned agent; an
// account key must not name one; unknown scope is rejected.
func TestCreateScopedAPIKey_Guards(t *testing.T) {
	store, user, ag := setupScopeUserAgent(t, "scope-guard")
	ctx := context.Background()

	// agent scope, no agent id → error
	if _, err := store.CreateScopedAPIKey(ctx, user.ID, "x", identity.ScopeAgent, "", nil); err == nil {
		t.Error("expected error for agent scope without agent_id")
	}
	// account scope, with agent id → error
	if _, err := store.CreateScopedAPIKey(ctx, user.ID, "x", identity.ScopeAccount, ag.ID, nil); err == nil {
		t.Error("expected error for account scope naming an agent")
	}
	// unknown scope → error
	if _, err := store.CreateScopedAPIKey(ctx, user.ID, "x", "root", "", nil); err == nil {
		t.Error("expected error for unknown scope")
	}

	// agent scope bound to ANOTHER user's agent → error (cross-tenant guard)
	store2, user2, _ := setupScopeUserAgent(t, "scope-guard2")
	_ = store2
	if _, err := store.CreateScopedAPIKey(ctx, user2.ID, "x", identity.ScopeAgent, ag.ID, nil); err == nil {
		t.Error("expected error binding a key to another user's agent")
	}
}

// TestLegacyKeyResolvesAccount: a pre-Slice-5a row with NULL scope resolves to
// account (the backfill guarantee — no key silently loses authority).
func TestLegacyKeyResolvesAccount(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, err := store.CreateOrGetUser(ctx, "legacy@example.com", "Owner", "google-legacy")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	// Simulate a legacy key inserted the pre-Slice-5a way: the INSERT does not
	// mention the scope column at all, so migration 034's NOT NULL DEFAULT
	// 'account' backfills it — the guarantee that no existing key loses
	// authority on deploy. Hash the plaintext exactly as the store does.
	plaintext := "e2a_legacykey_deadbeef"
	if _, err := pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, name, key_prefix, key_hash, created_at)
		 VALUES ($1, $2, $3, $4, encode(sha256($5::bytea), 'hex'), now())`,
		"apk_legacy", user.ID, "legacy", "e2a_legacy", plaintext,
	); err != nil {
		t.Fatalf("insert legacy key: %v", err)
	}

	p, err := store.GetPrincipalByAPIKey(ctx, plaintext)
	if err != nil {
		t.Fatalf("GetPrincipalByAPIKey(legacy): %v", err)
	}
	if p.Scope != identity.ScopeAccount {
		t.Errorf("legacy key scope = %q, want account (backfill)", p.Scope)
	}
	if p.User.ID != user.ID {
		t.Errorf("legacy key user = %s, want %s", p.User.ID, user.ID)
	}
}
