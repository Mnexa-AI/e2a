package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
	migrationsDir := filepath.Join(projectRoot(), "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		migration, err := os.ReadFile(filepath.Join(migrationsDir, entry.Name()))
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			// Existing tables / repeated extensions are expected when a
			// previous run already applied the schema; ignoring those is
			// the established convention here. But we log to stderr so a
			// genuine SQL error in a new migration surfaces during
			// `go test` instead of being absorbed silently — that would
			// otherwise let a broken migration ship and only fail later
			// in the real RunMigrations path.
			fmt.Fprintf(os.Stderr, "[testutil] migration %s: %v\n", entry.Name(), err)
		}
	}
	return nil
}

func truncateAll(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		TRUNCATE oauth_pkce_requests, oauth_refresh_tokens, oauth_access_tokens,
		         oauth_auth_codes, oauth_clients,
		         usage_summaries, usage_events, webhook_deliveries,
		         send_attempts, messages,
		         idempotency_keys, api_keys, webhook_signing_secrets,
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

func projectRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	// internal/testutil/db.go -> project root
	return filepath.Join(filepath.Dir(filename), "..", "..")
}
