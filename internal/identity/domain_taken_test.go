package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
)

// MED-3 — a cross-user claim of a domain another user already owns must
// return the typed sentinel identity.ErrDomainTaken so the API layer can map
// it to 409 (conflict), distinct from the 400 used for malformed input.
func TestClaimOrCreateDomain_CrossUserReturnsErrDomainTaken(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	userA, _ := store.CreateOrGetUser(ctx, "taken-a@example.com", "Owner A", "google-taken-a")
	userB, _ := store.CreateOrGetUser(ctx, "taken-b@example.com", "Owner B", "google-taken-b")

	if _, err := store.ClaimOrCreateDomain(ctx, "taken.example.com", userA.ID); err != nil {
		t.Fatalf("userA ClaimOrCreateDomain: %v", err)
	}

	_, err := store.ClaimOrCreateDomain(ctx, "taken.example.com", userB.ID)
	if err == nil {
		t.Fatal("userB reclaim should fail when userA already owns the row")
	}
	if !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("want errors.Is(err, ErrDomainTaken), got %v", err)
	}
}

func TestClaimOrCreateDomain_HierarchicalClaimsAreExclusiveAcrossAccounts(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA, _ := store.CreateOrGetUser(ctx, "tree-a@example.com", "Owner A", "google-tree-a")
	userB, _ := store.CreateOrGetUser(ctx, "tree-b@example.com", "Owner B", "google-tree-b")

	if _, err := store.ClaimOrCreateDomain(ctx, "mail.example.com", userA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "acme.mail.example.com", userB.ID); !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("parent claim must reserve descendants: got %v", err)
	}

	if _, err := store.ClaimOrCreateDomain(ctx, "child.other.example.com", userA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "other.example.com", userB.ID); !errors.Is(err, identity.ErrDomainTaken) {
		t.Fatalf("descendant claim must reserve ancestors: got %v", err)
	}
}

func TestClaimOrCreateDomain_SameAccountChildPromotesInheritedAgents(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "promote@example.com", "Owner", "google-promote")

	if _, err := store.ClaimOrCreateDomain(ctx, "mail.example.com", user.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyDomain(ctx, "mail.example.com", user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAgent(ctx, "otto@acme.mail.example.com", "mail.example.com", "", "", "", user.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ClaimOrCreateDomain(ctx, "acme.mail.example.com", user.ID); err != nil {
		t.Fatalf("same-account child registration: %v", err)
	}
	agent, err := store.GetAgentByID(ctx, "otto@acme.mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if agent.RegisteredDomain != "acme.mail.example.com" {
		t.Fatalf("registered domain = %q, want promoted child", agent.RegisteredDomain)
	}
	if agent.DomainVerified {
		t.Fatal("new explicit child is pending verification and must become authoritative immediately")
	}
}

func TestClaimOrCreateDomain_ReservesManagedMailFromSubtree(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	user, _ := store.CreateOrGetUser(ctx, "bounce@example.com", "Owner", "google-bounce")
	if _, err := store.ClaimOrCreateDomain(ctx, "mail.example.com", user.ID); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"bounce.mail.example.com", "tenant.bounce.mail.example.com"} {
		if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); !errors.Is(err, identity.ErrReservedDomain) {
			t.Errorf("%s: want ErrReservedDomain, got %v", domain, err)
		}
	}
}

func TestClaimOrCreateDomain_ConcurrentParentChildClaimsDoNotSplitOwnership(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA, _ := store.CreateOrGetUser(ctx, "race-a@example.com", "Owner A", "google-race-a")
	userB, _ := store.CreateOrGetUser(ctx, "race-b@example.com", "Owner B", "google-race-b")

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, claim := range []struct{ domain, userID string }{
		{"race.example.com", userA.ID},
		{"child.race.example.com", userB.ID},
	} {
		wg.Add(1)
		go func(domain, userID string) {
			defer wg.Done()
			<-start
			_, err := store.ClaimOrCreateDomain(ctx, domain, userID)
			results <- err
		}(claim.domain, claim.userID)
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, taken int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identity.ErrDomainTaken):
			taken++
		default:
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if successes != 1 || taken != 1 {
		t.Fatalf("want one winner and one ErrDomainTaken, got successes=%d taken=%d", successes, taken)
	}
}
