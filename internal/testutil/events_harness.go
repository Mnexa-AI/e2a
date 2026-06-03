package testutil

import (
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/idempotency"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/Mnexa-AI/e2a/internal/webhook"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewEventsAPIHarness spins up a minimal httptest server with the
// agent.API wired for the slice 6/7 events API. Used by the
// internal/e2e events tests that need to hit /api/v1/events and
// /api/v1/events/{id}/redeliver against a real HTTP layer.
//
// Returns the *httptest.Server; caller must Close() on cleanup.
func NewEventsAPIHarness(t *testing.T, pool *pgxpool.Pool, store *identity.Store, outbox webhookpub.Outbox) *httptest.Server {
	t.Helper()

	smtpRelay := outbound.NewSMTPRelay(nil)
	sender := outbound.NewSender(smtpRelay, "test.e2a.dev")
	noopUsage := usage.NewNoopUsageTracker()
	api := agent.NewAPI(store, sender, smtpRelay, nil, noopUsage, "e2a.dev", "test.e2a.dev", "agents.e2a.dev", "", false)
	api.SetIdempotencyStore(idempotency.NewStore(pool))
	api.SetSubscriberStore(webhook.NewSubscriberStore(pool))
	api.SetPublisher(webhookpub.New(store, webhookpub.NewDBInserter(pool), webhookpub.StaticFlag(true)))
	api.SetOutbox(outbox)
	api.SetPoolForEvents(pool)

	router := mux.NewRouter()
	api.RegisterRoutes(router)
	srv := httptest.NewServer(router)
	return srv
}
