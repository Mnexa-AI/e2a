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

// TestLookupCoveringDomain_CrossTenantIntrusionRejected is the QA F1 regression:
// user A owns+verified the GRANDPARENT (mnexa.ai); user B owns+verified a
// MORE-SPECIFIC name (team.mnexa.ai) inside it. A must NOT be able to cover
// team.mnexa.ai or acme.team.mnexa.ai via its grandparent — that would plant A
// inside B's verified namespace. B, the rightful owner, still resolves.
func TestLookupCoveringDomain_CrossTenantIntrusionRejected(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	a, err := store.CreateOrGetUser(ctx, "a@mnexa.ai", "A", "google-f1-a")
	if err != nil {
		t.Fatalf("CreateOrGetUser A: %v", err)
	}
	b, err := store.CreateOrGetUser(ctx, "b@example.com", "B", "google-f1-b")
	if err != nil {
		t.Fatalf("CreateOrGetUser B: %v", err)
	}
	// A owns+verifies the grandparent; B owns+verifies the more-specific parent.
	if _, err := store.ClaimOrCreateDomain(ctx, "mnexa.ai", a.ID); err != nil {
		t.Fatalf("claim mnexa.ai: %v", err)
	}
	if err := store.VerifyDomain(ctx, "mnexa.ai", a.ID); err != nil {
		t.Fatalf("verify mnexa.ai: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "team.mnexa.ai", b.ID); err != nil {
		t.Fatalf("claim team.mnexa.ai: %v", err)
	}
	if err := store.VerifyDomain(ctx, "team.mnexa.ai", b.ID); err != nil {
		t.Fatalf("verify team.mnexa.ai: %v", err)
	}

	// A: both the exact parent and a child under it are refused (would intrude).
	if got, err := store.LookupCoveringDomain(ctx, "team.mnexa.ai", a.ID); err == nil {
		t.Fatalf("F1: A must not cover team.mnexa.ai (owned by B); got %q", got.Domain)
	}
	if got, err := store.LookupCoveringDomain(ctx, "acme.team.mnexa.ai", a.ID); err == nil {
		t.Fatalf("F1: A must not cover acme.team.mnexa.ai inside B's namespace; got %q", got.Domain)
	}

	// B, the rightful owner, still resolves the child to its own parent.
	got, err := store.LookupCoveringDomain(ctx, "acme.team.mnexa.ai", b.ID)
	if err != nil {
		t.Fatalf("F1: B (rightful owner) must still cover acme.team.mnexa.ai: %v", err)
	}
	if got.Domain != "team.mnexa.ai" {
		t.Fatalf("B covering = %q, want team.mnexa.ai", got.Domain)
	}
}

// TestLookupCoveringDomain_SameUserIntermediateStillCovers proves the F1 guard
// is scoped to DIFFERENT-user ownership: a same-user intermediate (even
// unverified) does NOT block the grandparent from covering, and the legitimate
// single-owner case still works.
func TestLookupCoveringDomain_SameUserIntermediateStillCovers(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	a, err := store.CreateOrGetUser(ctx, "a@mnexa.ai", "A", "google-f1-same")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "mnexa.ai", a.ID); err != nil {
		t.Fatalf("claim grandparent: %v", err)
	}
	if err := store.VerifyDomain(ctx, "mnexa.ai", a.ID); err != nil {
		t.Fatalf("verify grandparent: %v", err)
	}
	// A also holds the intermediate, UNVERIFIED — same user, so it must not block.
	if _, err := store.ClaimOrCreateDomain(ctx, "team.mnexa.ai", a.ID); err != nil {
		t.Fatalf("claim intermediate: %v", err)
	}

	got, err := store.LookupCoveringDomain(ctx, "acme.team.mnexa.ai", a.ID)
	if err != nil {
		t.Fatalf("same-user intermediate must not block grandparent cover: %v", err)
	}
	// Most-specific VERIFIED owned ancestor is the grandparent (intermediate is
	// unverified), so the bind is to mnexa.ai.
	if got.Domain != "mnexa.ai" {
		t.Fatalf("covering = %q, want mnexa.ai (verified grandparent)", got.Domain)
	}

	// And the plain single-owner case still works (no other owner anywhere).
	got2, err := store.LookupCoveringDomain(ctx, "otto.mnexa.ai", a.ID)
	if err != nil {
		t.Fatalf("legitimate same-user cover must still work: %v", err)
	}
	if got2.Domain != "mnexa.ai" {
		t.Fatalf("covering = %q, want mnexa.ai", got2.Domain)
	}
}

// TestLookupCoveringDomain_ExactNameOwnedByOtherNotCoverable (QA test 2): an
// EXACT name owned by another user is not coverable by the requester's parent,
// even when no intermediate exists — the address domain X itself is checked.
func TestLookupCoveringDomain_ExactNameOwnedByOtherNotCoverable(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	a, _ := store.CreateOrGetUser(ctx, "a@mnexa.ai", "A", "google-f1-exact-a")
	b, _ := store.CreateOrGetUser(ctx, "b@example.com", "B", "google-f1-exact-b")
	// A owns+verifies the parent; B owns the exact child address domain.
	if _, err := store.ClaimOrCreateDomain(ctx, "mnexa.ai", a.ID); err != nil {
		t.Fatalf("claim parent: %v", err)
	}
	if err := store.VerifyDomain(ctx, "mnexa.ai", a.ID); err != nil {
		t.Fatalf("verify parent: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "bot.mnexa.ai", b.ID); err != nil {
		t.Fatalf("claim exact child (B): %v", err)
	}

	if got, err := store.LookupCoveringDomain(ctx, "bot.mnexa.ai", a.ID); err == nil {
		t.Fatalf("F1: A must not cover bot.mnexa.ai (exact name owned by B); got %q", got.Domain)
	}
}

// TestLookupCoveringDomain_PublicSuffixParentNotCoverable (QA test 4): a public
// suffix (e.g. co.uk) can never act as a covering parent even if a stray row
// existed — candidate generation drops public suffixes, so a subdomain whose
// only ancestor is a public suffix has no cover.
func TestLookupCoveringDomain_PublicSuffixParentNotCoverable(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@shop.co.uk", "Owner", "google-f1-ps")
	// A legitimate registrable domain under a multi-label public suffix.
	if _, err := store.ClaimOrCreateDomain(ctx, "shop.co.uk", user.ID); err != nil {
		t.Fatalf("claim shop.co.uk: %v", err)
	}
	if err := store.VerifyDomain(ctx, "shop.co.uk", user.ID); err != nil {
		t.Fatalf("verify shop.co.uk: %v", err)
	}

	// A child of the registrable domain IS coverable by it...
	got, err := store.LookupCoveringDomain(ctx, "eu.shop.co.uk", user.ID)
	if err != nil {
		t.Fatalf("child of registrable domain must be coverable: %v", err)
	}
	if got.Domain != "shop.co.uk" {
		t.Fatalf("covering = %q, want shop.co.uk", got.Domain)
	}
	// ...but a sibling registrable domain (other.co.uk) is NOT covered by co.uk
	// (the public suffix is never a candidate parent).
	if _, err := store.LookupCoveringDomain(ctx, "other.co.uk", user.ID); err == nil {
		t.Fatalf("co.uk (public suffix) must never cover other.co.uk")
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
