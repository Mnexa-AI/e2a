package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
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
