package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// TestMigration042_BackfillPreservesHolds is the regression guard for the HITL
// retirement: an agent that held outbound sends before 5b must still hold them
// after migration 042 maps the policy forward. Mirrors the two UPDATEs in
// migrations/042_retire_hitl.sql and asserts idempotency.
func TestMigration042_BackfillPreservesHolds(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const allMig = `UPDATE agent_identities
	   SET outbound_policy='allowlist', outbound_allowlist='{}', outbound_policy_action='review'
	 WHERE hitl_enabled=true AND COALESCE(hitl_mode,'all')='all' AND outbound_policy='open'`
	const hiMig = `UPDATE agent_identities
	   SET outbound_policy='domain', outbound_policy_action='review', outbound_scan='on'
	 WHERE hitl_enabled=true AND hitl_mode='high_impact' AND outbound_policy='open'`

	seed := func(domain, mode string) string {
		user, err := store.CreateOrGetUser(ctx, "o@"+domain, "O", "g-"+domain)
		if err != nil {
			t.Fatalf("CreateOrGetUser: %v", err)
		}
		if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
			t.Fatalf("ClaimOrCreateDomain: %v", err)
		}
		ag, err := store.CreateAgent(ctx, "bot@"+domain, domain, "", "", "", user.ID)
		if err != nil {
			t.Fatalf("CreateAgent: %v", err)
		}
		// Pre-042 state: HITL on with the given mode, outbound still at defaults.
		if _, err := pool.Exec(ctx, `UPDATE agent_identities SET hitl_enabled=true, hitl_mode=$2, outbound_policy='open', outbound_scan='off' WHERE id=$1`, ag.ID, mode); err != nil {
			t.Fatalf("seed pre-042 state: %v", err)
		}
		return ag.ID
	}
	get := func(id string) (policy, action, scan string) {
		if err := pool.QueryRow(ctx, `SELECT outbound_policy, outbound_policy_action, outbound_scan FROM agent_identities WHERE id=$1`, id).Scan(&policy, &action, &scan); err != nil {
			t.Fatalf("read agent: %v", err)
		}
		return
	}

	allID := seed("mig042all.example.com", "all")
	hiID := seed("mig042hi.example.com", "high_impact")

	// Run the backfill twice — must be idempotent.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, allMig); err != nil {
			t.Fatalf("allMig: %v", err)
		}
		if _, err := pool.Exec(ctx, hiMig); err != nil {
			t.Fatalf("hiMig: %v", err)
		}
	}

	// hitl_mode='all' held EVERY send → allowlist (empty) + review preserves that.
	if p, a, _ := get(allID); p != "allowlist" || a != "review" {
		t.Errorf("all-mode backfill = (%q,%q), want (allowlist,review)", p, a)
	}
	// hitl_mode='high_impact' held off-domain RECIPIENTS → a domain gate + review
	// preserves the recipient hold (the headline review finding: a content scan
	// alone would NOT, since it never inspects recipients).
	if p, a, s := get(hiID); p != "domain" || a != "review" || s != "on" {
		t.Errorf("high_impact backfill = (%q,%q,%q), want (domain,review,on)", p, a, s)
	}
}
