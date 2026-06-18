package testutil

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/apiserver"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/limits"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewEventsAPIHarness spins up a minimal httptest server with the
// agent.API wired for the slice 6/7 events API. The non-/v1 mux surface
// (OAuth, magic-link pages, health, etc.) is wrapped by the typed /v1 chi
// root (the same apiserver builder prod + TestServer use), so the e2e
// events tests hit the real /v1 handlers for events, webhooks, and agents.
//
// Returns the *httptest.Server; caller must Close() on cleanup.
func NewEventsAPIHarness(t *testing.T, pool *pgxpool.Pool, store *identity.Store, outbox webhookpub.Outbox) *httptest.Server {
	t.Helper()

	smtpRelay := outbound.NewSMTPRelay(nil)
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	subscriberStore := webhook.NewSubscriberStore(pool)
	idempotencyStore := idempotency.NewStore(pool)
	usageStore := usage.NewStore(pool)
	// Generous caps — e2e exercises behavior, not quota enforcement.
	enforcer := limits.NewEnforcer(limits.NewStore(pool), usageStore, limits.Defaults{
		PlanCode: "test", MaxAgents: 100000, MaxDomains: 100000,
		MaxMessagesMonth: 100000, MaxStorageBytes: 1 << 40,
	}, time.Minute)

	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotencyStore)
	api.SetSubscriberStore(subscriberStore)
	api.SetPublisher(webhookpub.New(store, webhookpub.NewDBInserter(pool), webhookpub.StaticFlag(true)))
	api.SetEnforcer(enforcer)
	api.SetUsageStore(usageStore)
	api.SetOutbox(outbox)
	api.SetPoolForEvents(pool)

	router := mux.NewRouter()
	api.RegisterRoutes(router)

	// Wrap the legacy mux with the typed /v1 surface so the events e2e
	// tests exercise the real /v1 handlers; still-legacy /api/v1 routes
	// fall through to the mux.
	v1 := apiserver.New(apiserver.Params{
		API: api, Store: store, Enforcer: enforcer, UsageStore: usageStore,
		SubscriberStore: subscriberStore, Idempotency: idempotencyStore, Pool: pool,
		SMTPDomain: "test.e2a.dev", SharedDomain: "agents.e2a.dev",
		PublicURL: "http://127.0.0.1", Production: false,
		Legacy: router,
	})

	srv := httptest.NewServer(v1)
	return srv
}
