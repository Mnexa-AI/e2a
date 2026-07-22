package apiserver

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/agent"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/limits"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhook"
)

func TestMessageLifecycleBuildDepsWiring(t *testing.T) {
	deps := BuildDeps(Params{
		API:             &agent.API{},
		Store:           &identity.Store{},
		Enforcer:        &limits.DBEnforcer{},
		UsageStore:      &usage.Store{},
		SubscriberStore: &webhook.SubscriberStore{},
		Pool:            &pgxpool.Pool{},
	})
	if deps.ListMessageLifecycle == nil {
		t.Fatal("BuildDeps did not wire the message lifecycle store")
	}
}
