package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// Warmup is armed as a pure side effect of a domain first becoming
// sending-verified (migration 050 + SetSendingStatus). This locks in the
// lifecycle the enforcer depends on: inactive until verified, active + a stamped
// anchor after, and — critically — the anchor is never reset on a later status
// write so a domain that already built reputation is not thrown back to day one.
func TestSetSendingStatusArmsWarmupOnFirstVerified(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "warmup@example.com", "Warmup Owner", "google-warmup")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	const domain = "warmup.example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	// Fresh domain: inactive, no anchor.
	status, startedAt, err := store.GetWarmupState(ctx, domain)
	if err != nil {
		t.Fatalf("GetWarmupState: %v", err)
	}
	if status != "inactive" || startedAt != nil {
		t.Fatalf("fresh domain: got status=%q startedAt=%v, want inactive/nil", status, startedAt)
	}

	// A non-verified sending status must NOT arm warmup.
	if err := store.SetSendingStatus(ctx, domain, "pending", "", "", "", nil); err != nil {
		t.Fatalf("SetSendingStatus pending: %v", err)
	}
	if status, startedAt, _ := store.GetWarmupState(ctx, domain); status != "inactive" || startedAt != nil {
		t.Fatalf("after pending: got status=%q startedAt=%v, want inactive/nil", status, startedAt)
	}

	// First 'verified' arms the ramp: active + anchor stamped.
	if err := store.SetSendingStatus(ctx, domain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus verified: %v", err)
	}
	status, startedAt, err = store.GetWarmupState(ctx, domain)
	if err != nil {
		t.Fatalf("GetWarmupState after verify: %v", err)
	}
	if status != "active" || startedAt == nil {
		t.Fatalf("after first verify: got status=%q startedAt=%v, want active/non-nil", status, startedAt)
	}
	anchor := *startedAt

	// A later re-verify (forced re-check, reconcile flap) must NOT move the anchor.
	if err := store.SetSendingStatus(ctx, domain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus re-verify: %v", err)
	}
	_, startedAt2, _ := store.GetWarmupState(ctx, domain)
	if startedAt2 == nil || !startedAt2.Equal(anchor) {
		t.Fatalf("re-verify moved the anchor: %v -> %v", anchor, startedAt2)
	}

	// A subsequent transient 'failed' must not clear the armed warmup either —
	// reputation, once building, is not un-built by a flap.
	if err := store.SetSendingStatus(ctx, domain, "failed", "", "", "transient", nil); err != nil {
		t.Fatalf("SetSendingStatus failed: %v", err)
	}
	status3, startedAt3, _ := store.GetWarmupState(ctx, domain)
	if status3 != "active" || startedAt3 == nil || !startedAt3.Equal(anchor) {
		t.Fatalf("after failed: got status=%q startedAt=%v, want active + anchor %v", status3, startedAt3, anchor)
	}
}
