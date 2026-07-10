package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

const (
	probeUserEmail  = "prober@e2a.system"
	probeUserName   = "e2a prober"
	probeGoogleSub  = "e2a-prober-system" // fixed → CreateOrGetUser is idempotent
	probeWebhookDsc = "e2a-prober selftest sink"
)

// seedResult holds the credentials a freshly-seeded probe needs. The API key and
// webhook secret are only recoverable at creation, so the operator must capture
// them into the prober's env (E2A_PROBE_API_KEY / E2A_PROBE_WEBHOOK_SECRET).
type seedResult struct {
	AgentEmail    string
	APIKey        string // empty if not (re)created this run
	WebhookSecret string // empty if the webhook already existed
}

// seedProbe idempotently provisions the synthetic probe account and identity:
// a system-class user, a verified domain, the probe agent (open inbound policy,
// HITL off — both defaults), an API key, and a webhook → sinkURL. The user,
// domain, and agent are find-or-create; the API key is always (re)created; the
// webhook is created only if none already targets sinkURL.
func seedProbe(ctx context.Context, store *identity.Store, agentEmail, sinkURL string) (*seedResult, error) {
	parts := strings.SplitN(agentEmail, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid agent email %q", agentEmail)
	}
	domain := parts[1]

	user, err := store.CreateOrGetUser(ctx, probeUserEmail, probeUserName, probeGoogleSub)
	if err != nil {
		return nil, fmt.Errorf("create probe user: %w", err)
	}
	if err := store.SetAccountClass(ctx, user.ID, "system"); err != nil {
		return nil, fmt.Errorf("set system class: %w", err)
	}

	dom, err := store.ClaimOrCreateDomain(ctx, domain, user.ID)
	if err != nil {
		return nil, fmt.Errorf("claim domain %s: %w", domain, err)
	}
	// VerifyDomain is not idempotent (it errors on an already-verified domain),
	// so only verify when needed — keeps re-seeding safe.
	if !dom.Verified {
		if err := store.VerifyDomain(ctx, domain, user.ID); err != nil {
			return nil, fmt.Errorf("verify domain %s: %w", domain, err)
		}
	}

	if _, err := store.GetAgentByID(ctx, agentEmail); err != nil {
		// Not found → create. (GetAgentByID returns an error for a missing agent.)
		if _, cerr := store.CreateAgent(ctx, agentEmail, domain, "e2a prober", "", "cloud", user.ID); cerr != nil {
			return nil, fmt.Errorf("create probe agent: %w", cerr)
		}
	}

	res := &seedResult{AgentEmail: agentEmail}

	// Create an API key only if the probe user has none — re-seeding otherwise
	// accumulates orphan keys (the plaintext is unrecoverable, so an existing
	// key can't be re-displayed; the operator reuses the one captured first).
	keys, err := store.ListAPIKeys(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	if len(keys) == 0 {
		key, err := store.CreateAPIKey(ctx, user.ID, "e2a-prober", nil)
		if err != nil {
			return nil, fmt.Errorf("create api key: %w", err)
		}
		res.APIKey = key.PlaintextKey
	}

	existing, err := store.ListWebhooksByUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	var found bool
	for _, wh := range existing {
		if wh.URL == sinkURL {
			found = true
			break
		}
	}
	if !found {
		wh, err := store.CreateWebhook(ctx, user.ID, sinkURL, probeWebhookDsc,
			[]string{"email.received", "email.sent"}, identity.WebhookFilters{})
		if err != nil {
			return nil, fmt.Errorf("create webhook: %w", err)
		}
		res.WebhookSecret = wh.SigningSecret
	}
	return res, nil
}

// cmdSeed provisions the probe identity and prints credentials to capture in env.
func cmdSeed(ctx context.Context, cfg config) error {
	if cfg.AgentEmail == "" {
		return fmt.Errorf("E2A_PROBE_AGENT_EMAIL is required")
	}
	if cfg.SinkURL == "" {
		return fmt.Errorf("E2A_PROBE_SINK_URL is required (the webhook target)")
	}
	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := identity.NewStore(pool)

	res, err := seedProbe(ctx, store, cfg.AgentEmail, cfg.SinkURL)
	if err != nil {
		return err
	}
	fmt.Printf("probe agent:    %s\n", res.AgentEmail)
	if res.APIKey != "" {
		fmt.Printf("E2A_PROBE_API_KEY=%s\n", res.APIKey)
	} else {
		fmt.Printf("# probe user already has an API key; reuse the one captured at first seed\n")
	}
	if res.WebhookSecret != "" {
		fmt.Printf("E2A_PROBE_WEBHOOK_SECRET=%s\n", res.WebhookSecret)
	} else {
		fmt.Printf("# webhook to %s already existed; reuse its stored secret\n", cfg.SinkURL)
	}
	return nil
}
