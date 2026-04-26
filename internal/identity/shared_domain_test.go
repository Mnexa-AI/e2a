package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func TestEnsureSharedDomain_EmptyIsNoop(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	if err := store.EnsureSharedDomain(ctx, ""); err != nil {
		t.Fatalf("EnsureSharedDomain(\"\"): %v", err)
	}
}

func TestEnsureSharedDomain_InsertsSystemRow(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const dom = "agents.test.example.com"
	if err := store.EnsureSharedDomain(ctx, dom); err != nil {
		t.Fatalf("EnsureSharedDomain: %v", err)
	}

	var userID *string
	var verified bool
	err := pool.QueryRow(ctx,
		`SELECT user_id, verified FROM domains WHERE domain = $1`, dom,
	).Scan(&userID, &verified)
	if err != nil {
		t.Fatalf("query seeded row: %v", err)
	}
	if userID != nil {
		t.Errorf("user_id = %v, want NULL (system-owned)", *userID)
	}
	if !verified {
		t.Error("verified = false, want true")
	}

	user, err := store.CreateOrGetUser(ctx, "alice@elsewhere.com", "Alice", "google-shared-domain-agent")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.CreateAgent(ctx, "alice@"+dom, dom, "", "https://example.com/wh", "", user.ID); err != nil {
		t.Fatalf("CreateAgent on shared domain: %v (FK to domains row should resolve)", err)
	}
}

func TestEnsureSharedDomain_Idempotent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const dom = "agents.idem.example.com"
	for i := 0; i < 3; i++ {
		if err := store.EnsureSharedDomain(ctx, dom); err != nil {
			t.Fatalf("EnsureSharedDomain attempt %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM domains WHERE domain = $1`, dom,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("row count after 3 calls = %d, want 1", n)
	}
}

func TestEnsureSharedDomain_NormalizesCase(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	if err := store.EnsureSharedDomain(ctx, "Agents.Mixed.Example.COM"); err != nil {
		t.Fatalf("EnsureSharedDomain: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM domains WHERE domain = $1`, "agents.mixed.example.com",
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row at lowercased domain, got %d", n)
	}
}
