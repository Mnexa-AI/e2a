package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
)

// Conformance test account identity. Fixed values → CreateOrGetUser is
// idempotent, so re-seeding a staging DB converges on the same account.
const (
	conformanceUserEmail = "conformance@e2a.test"
	conformanceUserName  = "e2a conformance"
	conformanceGoogleSub = "e2a-conformance"
	conformanceKeyName   = "e2a-conformance"
)

// cmdSeedConformance provisions the persistent account that the black-box
// tests/e2e-prod conformance suite runs against on staging. Unlike the
// system-class probe (cmdSeed), this account is INTERNAL class so it is
// unmetered (usage.PolicyFor: only "standard" is metered) — the suite's
// heavy message churn never trips the monthly send quota — while a high
// account_limits row gives the count-based caps (max_agents/max_domains,
// which are enforced regardless of class) enough headroom for the suite's
// create/delete churn (staging's default caps are 3 agents / 1 domain).
//
// It seeds: the internal-class user, its high limits row, the shared domain
// (find-or-create — the server already seeds it on boot), a primary agent
// (the pre-existing inbox the suite reads against but never deletes), and an
// API key. It prints E2A_AGENT_EMAIL / E2A_SHARED_DOMAIN / E2A_API_KEY for the
// pipeline to capture. Idempotent: user/domain/agent are find-or-create and
// the API key is (re)created only when the account has none (its plaintext is
// unrecoverable, so an existing key can't be re-printed — reuse the first).
func cmdSeedConformance(ctx context.Context, cfg config) error {
	agentEmail := os.Getenv("E2A_CONFORMANCE_AGENT_EMAIL")
	if agentEmail == "" {
		return fmt.Errorf("E2A_CONFORMANCE_AGENT_EMAIL is required (primary agent, e.g. conformance@agents-staging.e2a.dev)")
	}
	parts := strings.SplitN(agentEmail, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid agent email %q", agentEmail)
	}
	domain := parts[1]

	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := identity.NewStore(pool)

	user, err := store.CreateOrGetUser(ctx, conformanceUserEmail, conformanceUserName, conformanceGoogleSub)
	if err != nil {
		return fmt.Errorf("create conformance user: %w", err)
	}
	if err := store.SetAccountClass(ctx, user.ID, "internal"); err != nil {
		return fmt.Errorf("set internal class: %w", err)
	}

	if err := limits.NewStore(pool).Upsert(ctx, user.ID, limits.Limits{
		PlanCode:         "conformance",
		MaxAgents:        100000,
		MaxDomains:       100000,
		MaxMessagesMonth: 100000000,
		MaxStorageBytes:  1 << 40, // 1 TiB
	}); err != nil {
		return fmt.Errorf("set conformance limits: %w", err)
	}

	if err := store.EnsureSharedDomain(ctx, domain); err != nil {
		return fmt.Errorf("ensure shared domain %s: %w", domain, err)
	}

	if _, err := store.GetAgentByID(ctx, agentEmail); err != nil {
		// Not found → create the primary agent on the shared domain.
		if _, cerr := store.CreateAgent(ctx, agentEmail, domain, "e2a conformance", "", "cloud", user.ID); cerr != nil {
			return fmt.Errorf("create primary agent: %w", cerr)
		}
	}

	var apiKey string
	keys, err := store.ListAPIKeys(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("list api keys: %w", err)
	}
	if len(keys) == 0 {
		key, err := store.CreateAPIKey(ctx, user.ID, conformanceKeyName, nil)
		if err != nil {
			return fmt.Errorf("create api key: %w", err)
		}
		apiKey = key.PlaintextKey
	}

	fmt.Printf("E2A_AGENT_EMAIL=%s\n", agentEmail)
	fmt.Printf("E2A_SHARED_DOMAIN=%s\n", domain)
	if apiKey != "" {
		fmt.Printf("E2A_API_KEY=%s\n", apiKey)
	} else {
		fmt.Printf("# conformance account already has an API key; reuse the one captured at first seed\n")
	}
	return nil
}
