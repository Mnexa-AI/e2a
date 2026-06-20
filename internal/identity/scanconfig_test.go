package identity_test

import (
	"context"
	"math"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func approxEq(a, b float64) bool { return math.Abs(a-b) < 0.001 }

// TestUpdateAgentScanConfigRoundTrip is the DB-backed surfacing contract for Slice 3:
// fresh agents read the migration defaults, and UpdateAgentScanConfig persists the
// full posture surfaced by GetAgentByID.
func TestUpdateAgentScanConfigRoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "owner@scan.example.com", "Owner", "google-scan-rt")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "scan.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	agent, err := store.CreateAgent(ctx, "agent@scan.example.com", "scan.example.com", "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Defaults from migration 038.
	fresh, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if fresh.InboundPolicyAction != "flag" || fresh.OutboundPolicyAction != "flag" {
		t.Errorf("default actions = %q/%q, want flag/flag", fresh.InboundPolicyAction, fresh.OutboundPolicyAction)
	}
	if fresh.OutboundPolicy != "open" || fresh.InboundScan != "off" || fresh.OutboundScan != "off" {
		t.Errorf("default gate/scan = %q/%q/%q", fresh.OutboundPolicy, fresh.InboundScan, fresh.OutboundScan)
	}
	if !approxEq(fresh.InboundScanReviewThreshold, 0.5) || !approxEq(fresh.InboundScanBlockThreshold, 0.9) {
		t.Errorf("default thresholds = %v/%v, want 0.5/0.9", fresh.InboundScanReviewThreshold, fresh.InboundScanBlockThreshold)
	}

	cfg := identity.ScanConfig{
		InboundPolicyAction:         "review",
		OutboundPolicy:              "domain",
		OutboundAllowlist:           []string{"acme.com"},
		OutboundPolicyAction:        "block",
		InboundScan:                 "on",
		InboundScanReviewThreshold:  0.6,
		InboundScanBlockThreshold:   0.95,
		OutboundScan:                "on",
		OutboundScanReviewThreshold: 0.4,
		OutboundScanBlockThreshold:  0.8,
	}
	if err := store.UpdateAgentScanConfig(ctx, agent.ID, user.ID, cfg); err != nil {
		t.Fatalf("UpdateAgentScanConfig: %v", err)
	}

	got, err := store.GetAgentByID(ctx, agent.ID)
	if err != nil {
		t.Fatalf("GetAgentByID after update: %v", err)
	}
	if got.InboundPolicyAction != "review" || got.OutboundPolicy != "domain" || got.OutboundPolicyAction != "block" {
		t.Errorf("policy fields not persisted: %+v", got)
	}
	if got.InboundScan != "on" || got.OutboundScan != "on" {
		t.Errorf("scan toggles not persisted: in=%q out=%q", got.InboundScan, got.OutboundScan)
	}
	if !approxEq(got.InboundScanReviewThreshold, 0.6) || !approxEq(got.OutboundScanBlockThreshold, 0.8) {
		t.Errorf("thresholds not persisted: %v / %v", got.InboundScanReviewThreshold, got.OutboundScanBlockThreshold)
	}
	if len(got.OutboundAllowlist) != 1 || got.OutboundAllowlist[0] != "acme.com" {
		t.Errorf("outbound_allowlist not persisted: %v", got.OutboundAllowlist)
	}
}

// TestUpdateAgentScanConfigValidation rejects invalid postures with a clean error
// instead of a raw CHECK violation.
func TestUpdateAgentScanConfigValidation(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, _ := store.CreateOrGetUser(ctx, "owner@scanv.example.com", "Owner", "google-scanv")
	if _, err := store.ClaimOrCreateDomain(ctx, "scanv.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}
	agent, err := store.CreateAgent(ctx, "agent@scanv.example.com", "scanv.example.com", "", "", "", user.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	base := identity.ScanConfig{
		InboundPolicyAction: "flag", OutboundPolicy: "open", OutboundPolicyAction: "flag",
		InboundScan: "off", OutboundScan: "off",
		InboundScanReviewThreshold: 0.5, InboundScanBlockThreshold: 0.9,
		OutboundScanReviewThreshold: 0.5, OutboundScanBlockThreshold: 0.9,
	}
	mut := func(f func(*identity.ScanConfig)) identity.ScanConfig {
		c := base
		f(&c)
		return c
	}
	cases := map[string]identity.ScanConfig{
		"invalid action":             mut(func(c *identity.ScanConfig) { c.InboundPolicyAction = "nope" }),
		"verified_only not outbound": mut(func(c *identity.ScanConfig) { c.OutboundPolicy = "verified_only" }),
		"invalid scan toggle":        mut(func(c *identity.ScanConfig) { c.InboundScan = "maybe" }),
		"review > block":             mut(func(c *identity.ScanConfig) { c.InboundScanReviewThreshold = 0.95; c.InboundScanBlockThreshold = 0.5 }),
		"threshold out of range":     mut(func(c *identity.ScanConfig) { c.OutboundScanBlockThreshold = 1.5 }),
	}
	for name, c := range cases {
		if err := store.UpdateAgentScanConfig(ctx, agent.ID, user.ID, c); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
	}
}
