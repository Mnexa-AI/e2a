package identity_test

import (
	"context"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
)

// TestLookupCoveringDomain_MostSpecificVerifiedParent proves the happy path:
// a verified parent (team.mnexa.ai) covers a subdomain (acme.team.mnexa.ai),
// and when several ancestors are registered the MOST-SPECIFIC one wins.
func TestLookupCoveringDomain_MostSpecificVerifiedParent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@mnexa.ai", "Owner", "google-cover-1")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	// Register + verify both mnexa.ai and team.mnexa.ai — the subdomain agent
	// must resolve to the more specific of the two.
	for _, d := range []string{"mnexa.ai", "team.mnexa.ai"} {
		if _, err := store.ClaimOrCreateDomain(ctx, d, user.ID); err != nil {
			t.Fatalf("ClaimOrCreateDomain(%s): %v", d, err)
		}
		if err := store.VerifyDomain(ctx, d, user.ID); err != nil {
			t.Fatalf("VerifyDomain(%s): %v", d, err)
		}
	}

	got, err := store.LookupCoveringDomain(ctx, "acme.team.mnexa.ai", user.ID)
	if err != nil {
		t.Fatalf("LookupCoveringDomain: %v", err)
	}
	if got.Domain != "team.mnexa.ai" {
		t.Errorf("covering domain = %q, want most-specific %q", got.Domain, "team.mnexa.ai")
	}
	if !got.Verified {
		t.Errorf("covering domain must be verified")
	}
}

// TestLookupCoveringDomain_LabelBoundaryRejection is the security test: a
// registered team.mnexa.ai must NOT cover evilteam.mnexa.ai (a naive string
// suffix would), and notmnexa.ai must NOT be covered by mnexa.ai.
func TestLookupCoveringDomain_LabelBoundaryRejection(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@mnexa.ai", "Owner", "google-cover-2")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	for _, d := range []string{"mnexa.ai", "team.mnexa.ai"} {
		if _, err := store.ClaimOrCreateDomain(ctx, d, user.ID); err != nil {
			t.Fatalf("ClaimOrCreateDomain(%s): %v", d, err)
		}
		if err := store.VerifyDomain(ctx, d, user.ID); err != nil {
			t.Fatalf("VerifyDomain(%s): %v", d, err)
		}
	}

	// evilteam.mnexa.ai shares a string suffix with team.mnexa.ai but is NOT a
	// label-boundary child of it — its only real ancestor is mnexa.ai.
	got, err := store.LookupCoveringDomain(ctx, "evilteam.mnexa.ai", user.ID)
	if err != nil {
		t.Fatalf("LookupCoveringDomain(evilteam): %v", err)
	}
	if got.Domain == "team.mnexa.ai" {
		t.Fatalf("SECURITY: evilteam.mnexa.ai must NOT be covered by team.mnexa.ai (label-boundary breach)")
	}
	if got.Domain != "mnexa.ai" {
		t.Errorf("evilteam.mnexa.ai covering = %q, want mnexa.ai", got.Domain)
	}

	// notmnexa.ai is a sibling registrable domain, not a child of mnexa.ai.
	if _, err := store.LookupCoveringDomain(ctx, "notmnexa.ai", user.ID); err == nil {
		t.Fatalf("SECURITY: notmnexa.ai must NOT be covered by mnexa.ai")
	}
}

// TestLookupCoveringDomain_UnverifiedParentDoesNotCover: an unverified parent
// grants nothing — the SES identity it would sign under is not proven.
func TestLookupCoveringDomain_UnverifiedParentDoesNotCover(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@mnexa.ai", "Owner", "google-cover-3")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "team.mnexa.ai", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	// Intentionally NOT verified.

	if _, err := store.LookupCoveringDomain(ctx, "acme.team.mnexa.ai", user.ID); err == nil {
		t.Fatalf("unverified parent must not cover a subdomain")
	}
}

// TestLookupCoveringDomain_NotOwnedByUser: a parent verified by a DIFFERENT
// user must not cover this user's requested subdomain (tenant isolation).
func TestLookupCoveringDomain_NotOwnedByUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	owner, err := store.CreateOrGetUser(ctx, "owner@mnexa.ai", "Owner", "google-cover-4a")
	if err != nil {
		t.Fatalf("CreateOrGetUser owner: %v", err)
	}
	other, err := store.CreateOrGetUser(ctx, "other@example.com", "Other", "google-cover-4b")
	if err != nil {
		t.Fatalf("CreateOrGetUser other: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "team.mnexa.ai", owner.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	if err := store.VerifyDomain(ctx, "team.mnexa.ai", owner.ID); err != nil {
		t.Fatalf("VerifyDomain: %v", err)
	}

	if _, err := store.LookupCoveringDomain(ctx, "acme.team.mnexa.ai", other.ID); err == nil {
		t.Fatalf("a parent owned by another user must not cover this user's subdomain")
	}
}
