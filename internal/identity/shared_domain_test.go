package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func TestAdoptSharedDomain(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const dom = "agents.adopt.example.com"
	if err := store.EnsureSharedDomain(ctx, dom); err != nil {
		t.Fatalf("EnsureSharedDomain: %v", err)
	}
	u1, err := store.CreateOrGetUser(ctx, "probe@adopt.test", "probe", "google-adopt-1")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}

	// Adopt the ownerless, server-seeded row.
	d, err := store.AdoptSharedDomain(ctx, dom, u1.ID)
	if err != nil {
		t.Fatalf("AdoptSharedDomain (ownerless): %v", err)
	}
	if d.UserID == nil || *d.UserID != u1.ID {
		t.Fatalf("owner = %v, want %s", d.UserID, u1.ID)
	}
	if !d.Verified {
		t.Error("adopted shared domain should stay verified")
	}

	// Idempotent: the same user re-adopts without error.
	if _, err := store.AdoptSharedDomain(ctx, dom, u1.ID); err != nil {
		t.Fatalf("AdoptSharedDomain (idempotent re-adopt): %v", err)
	}

	// A different account cannot steal an already-owned domain.
	u2, err := store.CreateOrGetUser(ctx, "other@adopt.test", "other", "google-adopt-2")
	if err != nil {
		t.Fatalf("CreateOrGetUser u2: %v", err)
	}
	if _, err := store.AdoptSharedDomain(ctx, dom, u2.ID); !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("AdoptSharedDomain by another user: err = %v, want ErrDomainTaken", err)
	}

	// AdoptSharedDomain on a nonexistent domain is ErrDomainTaken, not a panic.
	if _, err := store.AdoptSharedDomain(ctx, "nope.example.com", u1.ID); !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("AdoptSharedDomain (missing): err = %v, want ErrDomainTaken", err)
	}

	// An ownerless but UNVERIFIED row is not adoptable: the verified=true guard
	// scopes the method to the server-seeded shared-domain shape, so it can never
	// hand out some other NULL-owner row even if one existed.
	const unverified = "ownerless.unverified.example.com"
	if _, err := pool.Exec(ctx, `INSERT INTO domains (domain, user_id, verified) VALUES ($1, NULL, false)`, unverified); err != nil {
		t.Fatalf("seed unverified ownerless row: %v", err)
	}
	if _, err := store.AdoptSharedDomain(ctx, unverified, u1.ID); !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("AdoptSharedDomain (unverified ownerless): err = %v, want ErrDomainTaken", err)
	}
}

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
