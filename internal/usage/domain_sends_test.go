package usage_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/usage"
)

// CountDomainSendsToday is the running numerator the warmup enforcer compares
// against the day's ramp cap. It must count only THIS domain's outbound rows
// from the current UTC day — not inbound, not other domains, not yesterday.
func TestCountDomainSendsToday(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	idStore := identity.NewStore(pool)
	usageStore := usage.NewStore(pool)

	user, err := idStore.CreateOrGetUser(ctx, "sends@example.com", "Sends Owner", "google-sends")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	const domain = "sends.example.com"
	const other = "other.example.com"
	for _, d := range []string{domain, other} {
		if _, err := idStore.ClaimOrCreateDomain(ctx, d, user.ID); err != nil {
			t.Fatalf("ClaimOrCreateDomain %s: %v", d, err)
		}
	}

	mkAgent := func(id, d string) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO agent_identities (id, domain, user_id, name) VALUES ($1, $2, $3, $4)`,
			id, d, user.ID, id); err != nil {
			t.Fatalf("insert agent %s: %v", id, err)
		}
	}
	mkAgent("agt_dom", domain)
	mkAgent("agt_other", other)

	mkMsg := func(id, agentID, direction string, createdAt time.Time) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO messages (id, agent_id, direction, created_at) VALUES ($1, $2, $3, $4)`,
			id, agentID, direction, createdAt); err != nil {
			t.Fatalf("insert message %s: %v", id, err)
		}
	}

	now := time.Now().UTC()
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(-2 * time.Hour)

	// 3 outbound today on the target domain (counted).
	mkMsg("m1", "agt_dom", "outbound", now)
	mkMsg("m2", "agt_dom", "outbound", now)
	mkMsg("m3", "agt_dom", "outbound", now)
	// Noise that must NOT be counted:
	mkMsg("m4", "agt_dom", "inbound", now)      // inbound
	mkMsg("m5", "agt_dom", "outbound", yesterday) // prior UTC day
	mkMsg("m6", "agt_other", "outbound", now)   // different domain

	got, err := usageStore.CountDomainSendsToday(ctx, domain)
	if err != nil {
		t.Fatalf("CountDomainSendsToday: %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3 (only today's outbound on %s)", got, domain)
	}

	// Case-insensitive: the stored domain is lowercase; an upper-case query must match.
	if got, err := usageStore.CountDomainSendsToday(ctx, "SENDS.EXAMPLE.COM"); err != nil || got != 3 {
		t.Fatalf("upper-case query: got %d err %v, want 3/nil", got, err)
	}

	// A domain with no sends is zero, not an error.
	if got, err := usageStore.CountDomainSendsToday(ctx, "never.example.com"); err != nil || got != 0 {
		t.Fatalf("empty domain: got %d err %v, want 0/nil", got, err)
	}
}
