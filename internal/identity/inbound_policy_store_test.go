package identity_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestUpdateAgentInboundPolicyRoundTrip is the DB-backed surfacing contract for
// Slice 7a: UpdateAgentInboundPolicy persists the policy + allowlist, and the
// agent reads (GetAgentByID) surface them. A fresh agent defaults to "open"
// with an empty allowlist.
func TestUpdateAgentInboundPolicyRoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@policy.example.com", "Owner", "google-policy-rt")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "policy.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	agent, err := store.CreateAgent(ctx, "agent@policy.example.com", "policy.example.com", "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Default posture (read from the DB, where the column DEFAULT 'open'
	// applies — the struct returned by CreateAgent doesn't re-read it).
	fresh, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if fresh.InboundPolicy != "open" {
		t.Errorf("fresh agent InboundPolicy = %q, want open", fresh.InboundPolicy)
	}
	if len(fresh.InboundAllowlist) != 0 {
		t.Errorf("fresh agent InboundAllowlist = %v, want empty", fresh.InboundAllowlist)
	}

	// Set an allowlist policy.
	allow := []string{"friend@trusted.com", "ally@partner.com"}
	if err := store.UpdateAgentInboundPolicy(ctx, agent.ID, user.ID, "allowlist", allow); err != nil {
		t.Fatalf("UpdateAgentInboundPolicy: %v", err)
	}

	got, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got.InboundPolicy != "allowlist" {
		t.Errorf("InboundPolicy = %q, want allowlist", got.InboundPolicy)
	}
	if !reflect.DeepEqual(got.InboundAllowlist, allow) {
		t.Errorf("InboundAllowlist = %v, want %v", got.InboundAllowlist, allow)
	}

	// Switching back to open clears the gate (allowlist may be left as-is or
	// cleared; the policy is what gates). Pin the policy transition.
	if err := store.UpdateAgentInboundPolicy(ctx, agent.ID, user.ID, "open", nil); err != nil {
		t.Fatalf("UpdateAgentInboundPolicy(open): %v", err)
	}
	got2, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got2.InboundPolicy != "open" {
		t.Errorf("after reset InboundPolicy = %q, want open", got2.InboundPolicy)
	}
}

// TestUpdateAgentInboundPolicyWrongOwner: a user cannot change another user's
// agent policy (tenant isolation — the UPDATE is scoped by user_id).
func TestUpdateAgentInboundPolicyWrongOwner(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	owner, err := store.CreateOrGetUser(ctx, "owner@iso.example.com", "Owner", "google-iso-owner")
	if err != nil {
		t.Fatalf("CreateOrGetUser owner: %v", err)
	}
	attacker, err := store.CreateOrGetUser(ctx, "attacker@iso.example.com", "Attacker", "google-iso-attacker")
	if err != nil {
		t.Fatalf("CreateOrGetUser attacker: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "iso.example.com", owner.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	agent, err := store.CreateAgent(ctx, "agent@iso.example.com", "iso.example.com", "", "", "", owner.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Attacker attempts to set a policy on the owner's agent.
	err = store.UpdateAgentInboundPolicy(ctx, agent.ID, attacker.ID, "allowlist", []string{"x@evil.com"})
	if err == nil {
		t.Fatal("expected error when non-owner updates inbound policy, got nil")
	}

	// Owner's agent policy must be unchanged (still open).
	got, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got.InboundPolicy != "open" {
		t.Errorf("InboundPolicy = %q after cross-tenant attempt, want open (unchanged)", got.InboundPolicy)
	}
}
