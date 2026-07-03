package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// SendingRamp is armed as a pure side effect of a domain first becoming
// sending-verified while ramp-up is enabled (migration 050 + SetSendingStatus +
// SetSendingRampArming). This locks in the lifecycle the enforcer depends on:
// inactive until verified, active + a stamped anchor after, and — critically —
// the anchor is never reset on a later status write so a domain that already
// built reputation is not thrown back to day one.
func TestSetSendingStatusArmsSendingRampOnFirstVerified(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	store.SetSendingRampArming(true)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "ramp-up@example.com", "SendingRamp Owner", "google-ramp-up")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	const domain = "ramp-up.example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	// Fresh domain: inactive, no anchor.
	status, startedAt, err := store.GetSendingRampState(ctx, domain)
	if err != nil {
		t.Fatalf("GetSendingRampState: %v", err)
	}
	if status != "inactive" || startedAt != nil {
		t.Fatalf("fresh domain: got status=%q startedAt=%v, want inactive/nil", status, startedAt)
	}

	// A non-verified sending status must NOT arm ramp-up.
	if err := store.SetSendingStatus(ctx, domain, "pending", "", "", "", nil); err != nil {
		t.Fatalf("SetSendingStatus pending: %v", err)
	}
	if status, startedAt, _ := store.GetSendingRampState(ctx, domain); status != "inactive" || startedAt != nil {
		t.Fatalf("after pending: got status=%q startedAt=%v, want inactive/nil", status, startedAt)
	}

	// First 'verified' arms the ramp: active + anchor stamped.
	if err := store.SetSendingStatus(ctx, domain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus verified: %v", err)
	}
	status, startedAt, err = store.GetSendingRampState(ctx, domain)
	if err != nil {
		t.Fatalf("GetSendingRampState after verify: %v", err)
	}
	if status != "active" || startedAt == nil {
		t.Fatalf("after first verify: got status=%q startedAt=%v, want active/non-nil", status, startedAt)
	}
	anchor := *startedAt

	// A later re-verify (forced re-check, reconcile flap) must NOT move the anchor.
	if err := store.SetSendingStatus(ctx, domain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus re-verify: %v", err)
	}
	_, startedAt2, _ := store.GetSendingRampState(ctx, domain)
	if startedAt2 == nil || !startedAt2.Equal(anchor) {
		t.Fatalf("re-verify moved the anchor: %v -> %v", anchor, startedAt2)
	}

	// A subsequent transient 'failed' must not clear the armed ramp-up either —
	// reputation, once building, is not un-built by a flap.
	if err := store.SetSendingStatus(ctx, domain, "failed", "", "", "transient", nil); err != nil {
		t.Fatalf("SetSendingStatus failed: %v", err)
	}
	status3, startedAt3, _ := store.GetSendingRampState(ctx, domain)
	if status3 != "active" || startedAt3 == nil || !startedAt3.Equal(anchor) {
		t.Fatalf("after failed: got status=%q startedAt=%v, want active + anchor %v", status3, startedAt3, anchor)
	}
}

// With ramp-up disabled (the default — SetSendingRampArming never called), a first
// verify stamps the anchor but does NOT arm the ramp. This is what makes
// enabling ramp-up later safe: domains that verified and built volume while the
// feature was off keep 'inactive' + a non-NULL anchor, so the arm-once CASE
// (keyed on sending_ramp_started_at IS NULL) can never throttle them retroactively.
func TestSetSendingStatusDisabledStampsAnchorWithoutArming(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool) // arming off (default)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "ramp-up-off@example.com", "SendingRamp Off", "google-ramp-up-off")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	const domain = "ramp-up-off.example.com"
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	if err := store.SetSendingStatus(ctx, domain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus verified: %v", err)
	}
	status, startedAt, err := store.GetSendingRampState(ctx, domain)
	if err != nil {
		t.Fatalf("GetSendingRampState: %v", err)
	}
	if status != "inactive" || startedAt == nil {
		t.Fatalf("disabled first verify: got status=%q startedAt=%v, want inactive + stamped anchor", status, startedAt)
	}

	// Operator enables ramp-up later: a re-verify on this domain must NOT arm it
	// (the anchor is already stamped).
	store.SetSendingRampArming(true)
	if err := store.SetSendingStatus(ctx, domain, "verified", "verified", "verified", "", nil); err != nil {
		t.Fatalf("SetSendingStatus re-verify: %v", err)
	}
	if status, _, _ := store.GetSendingRampState(ctx, domain); status != "inactive" {
		t.Fatalf("enable-later re-verify armed an established domain: status=%q, want inactive", status)
	}
}
