package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
)

// Conformance test account identities. Fixed values → CreateOrGetUser is
// idempotent, so re-seeding a staging DB converges on the same accounts.
const (
	conformanceUserEmail = "conformance@e2a.test"
	conformanceUserName  = "e2a conformance"
	conformanceGoogleSub = "e2a-conformance"
	conformanceKeyName   = "e2a-conformance"

	// The quota account is a SEPARATE standard-class account with LOW caps, so
	// the suite can exercise real limit + rate-limit enforcement — which the
	// main (internal, unmetered, exempt, huge-cap) conformance account cannot.
	quotaUserEmail  = "conformance-quota@e2a.test"
	quotaUserName   = "e2a conformance quota"
	quotaGoogleSub  = "e2a-conformance-quota"
	quotaKeyName    = "e2a-conformance-quota"
	quotaMaxAgents  = 5 // low but > the primary agent, so the count-cap is reachable + repeatable
	quotaMaxDomains = 1
)

// seededAccount is the credential + identity a freshly-seeded test account
// exposes. APIKey is empty when the account already had one (its plaintext is
// unrecoverable, so an existing key can't be re-printed).
type seededAccount struct {
	AgentEmail string
	Domain     string
	APIKey     string
}

// seedAccount provisions one test account: a user of the given class, its
// account_limits row, the shared domain (find-or-create — the server seeds it
// on boot), a primary agent (the pre-existing inbox the suite reads against but
// never deletes), and an API key (created only when the account has none).
// Idempotent across re-seeds.
func seedAccount(ctx context.Context, store *identity.Store, pool *pgxpool.Pool, email, name, googleSub, class, keyName, agentEmail string, lim limits.Limits) (*seededAccount, error) {
	parts := strings.SplitN(agentEmail, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid agent email %q", agentEmail)
	}
	domain := parts[1]

	user, err := store.CreateOrGetUser(ctx, email, name, googleSub)
	if err != nil {
		return nil, fmt.Errorf("create user %s: %w", email, err)
	}
	if err := store.SetAccountClass(ctx, user.ID, class); err != nil {
		return nil, fmt.Errorf("set %s class on %s: %w", class, email, err)
	}
	if err := limits.NewStore(pool).Upsert(ctx, user.ID, lim); err != nil {
		return nil, fmt.Errorf("set limits on %s: %w", email, err)
	}
	if err := store.EnsureSharedDomain(ctx, domain); err != nil {
		return nil, fmt.Errorf("ensure shared domain %s: %w", domain, err)
	}
	if _, err := store.GetAgentByID(ctx, agentEmail); err != nil {
		// Not found → create the primary agent on the shared domain.
		if _, cerr := store.CreateAgent(ctx, agentEmail, domain, name, "", "cloud", user.ID); cerr != nil {
			return nil, fmt.Errorf("create primary agent %s: %w", agentEmail, cerr)
		}
	}
	res := &seededAccount{AgentEmail: agentEmail, Domain: domain}
	keys, err := store.ListAPIKeys(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list api keys for %s: %w", email, err)
	}
	if len(keys) == 0 {
		key, err := store.CreateAPIKey(ctx, user.ID, keyName, nil)
		if err != nil {
			return nil, fmt.Errorf("create api key for %s: %w", email, err)
		}
		res.APIKey = key.PlaintextKey
	}
	return res, nil
}

// cmdSeedConformance provisions the persistent account(s) the black-box
// tests/e2e-prod conformance suite runs against on staging:
//
//   - the CONFORMANCE account (internal class, unmetered, rate-limit-exempt, huge
//     caps) — the main account the suite churns against without tripping quota,
//     rate limits, or the staging default 3-agent/1-domain caps.
//   - the QUOTA account (standard class, low caps) — a SEPARATE account so the
//     suite can assert real limit + rate-limit enforcement, which the internal
//     account is by construction blind to. Seeded only when E2A_QUOTA_AGENT_EMAIL
//     is set (opt-in).
//
// Prints E2A_AGENT_EMAIL / E2A_SHARED_DOMAIN / E2A_API_KEY (conformance) and, when
// seeded, E2A_QUOTA_AGENT_EMAIL / E2A_QUOTA_API_KEY for the pipeline to capture.
func cmdSeedConformance(ctx context.Context, cfg config) error {
	confAgent := os.Getenv("E2A_CONFORMANCE_AGENT_EMAIL")
	if confAgent == "" {
		return fmt.Errorf("E2A_CONFORMANCE_AGENT_EMAIL is required (primary agent, e.g. conformance@agents-staging.e2a.dev)")
	}

	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := identity.NewStore(pool)

	conf, err := seedAccount(ctx, store, pool, conformanceUserEmail, conformanceUserName, conformanceGoogleSub, "internal", conformanceKeyName, confAgent, limits.Limits{
		PlanCode:         "conformance",
		MaxAgents:        100000,
		MaxDomains:       100000,
		MaxMessagesMonth: 100000000,
		MaxStorageBytes:  1 << 40, // 1 TiB
	})
	if err != nil {
		return err
	}
	fmt.Printf("E2A_AGENT_EMAIL=%s\n", conf.AgentEmail)
	fmt.Printf("E2A_SHARED_DOMAIN=%s\n", conf.Domain)
	if conf.APIKey != "" {
		fmt.Printf("E2A_API_KEY=%s\n", conf.APIKey)
	} else {
		fmt.Printf("# conformance account already has an API key; reuse the one captured at first seed\n")
	}

	// Opt-in standard-class quota account for enforcement coverage.
	if quotaAgent := os.Getenv("E2A_QUOTA_AGENT_EMAIL"); quotaAgent != "" {
		quota, err := seedAccount(ctx, store, pool, quotaUserEmail, quotaUserName, quotaGoogleSub, "standard", quotaKeyName, quotaAgent, limits.Limits{
			PlanCode:         "conformance-quota",
			MaxAgents:        quotaMaxAgents,
			MaxDomains:       quotaMaxDomains,
			MaxMessagesMonth: 100000000, // monthly quota accumulates across runs; not asserted — kept high
			MaxStorageBytes:  1 << 40,
		})
		if err != nil {
			return err
		}
		fmt.Printf("E2A_QUOTA_AGENT_EMAIL=%s\n", quota.AgentEmail)
		if quota.APIKey != "" {
			fmt.Printf("E2A_QUOTA_API_KEY=%s\n", quota.APIKey)
		} else {
			fmt.Printf("# quota account already has an API key; reuse the one captured at first seed\n")
		}
	}
	return nil
}
