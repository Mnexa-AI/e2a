package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/migrations"
)

const defaultTestDBURL = "postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable"

func TestDBURL() string {
	dbURL := os.Getenv("E2A_TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = defaultTestDBURL
	}
	return dbURL
}

func OpenPreparedTestDB(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	if dbURL == "" {
		dbURL = defaultTestDBURL
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	if err := truncateAll(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

func TestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	pool, err := OpenPreparedTestDB(ctx, TestDBURL())
	if err != nil {
		t.Skipf("test database not available: %v", err)
	}

	t.Cleanup(func() {
		TruncateAll(t, pool)
		pool.Close()
	})

	return pool
}

func TruncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	err := truncateAll(context.Background(), pool)
	if err != nil {
		t.Fatalf("failed to truncate tables: %v", err)
	}
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	return identity.RunMigrations(ctx, pool, migrations.FS, identity.ModeAuto)
}

// truncateAll resets the DB between tests. Most tables are reached implicitly by
// TRUNCATE ... CASCADE via their FK path to users/messages/webhooks, so they need
// no explicit mention. Tables with NO foreign key at all cannot be reached by
// CASCADE, so they need explicit cleanup. Currently that is:
//
//   - inbound_intake: written at the SMTP edge BEFORE the agent lookup, so it
//     deliberately has no FK. Omitting it left stale dedup rows behind and made
//     TestInboundIntake_InsertLoadDedup / _StampProcessAndFail fail on a re-run
//     (the "insert must be new" assertions saw the previous run's rows).
//
// Use DELETE for FK-less tables instead of adding them to TRUNCATE. The test suite
// calls this helper hundreds of times; repeatedly truncating inbound_intake also
// recreates and fsyncs its three indexes and requires an ACCESS EXCLUSIVE lock.
// Any future FK-less table MUST be added to the DELETE section here.
func truncateAll(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM inbound_intake;

		TRUNCATE oauth_pkce_requests, oauth_refresh_tokens, oauth_access_tokens,
		         oauth_auth_codes, oauth_clients,
		         usage_summaries, usage_events, webhook_deliveries,
		         send_attempts, protection_events, messages,
		         idempotency_keys, api_keys,
		         agent_identities, domains,
		         user_sessions, users CASCADE
	`)
	if err != nil {
		return err
	}
	// Re-seed shared domain (migration seeds it but truncation removes it)
	pool.Exec(ctx, `INSERT INTO domains (domain, user_id, verified, verified_at) VALUES ('agents.e2a.dev', NULL, true, now()) ON CONFLICT DO NOTHING`)
	return nil
}
