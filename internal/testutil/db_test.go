package testutil

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const testDBErrorChildEnv = "E2A_TEST_DB_ERROR_CHILD"

func TestTruncateAll_CleansInboundIntakeWithoutExclusiveTableLock(t *testing.T) {
	pool := TestDB(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO inbound_intake (id, recipient, raw_message, content_hash)
		VALUES ('intk_cleanup_lock', 'agent@example.com', 'raw', 'hash')
	`)
	if err != nil {
		t.Fatalf("seed inbound_intake: %v", err)
	}

	reader, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reader transaction: %v", err)
	}
	defer reader.Rollback(ctx) //nolint:errcheck

	var before int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM inbound_intake`).Scan(&before); err != nil {
		t.Fatalf("read inbound_intake: %v", err)
	}
	if before != 1 {
		t.Fatalf("inbound_intake rows before cleanup = %d, want 1", before)
	}

	cleanupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := truncateAll(cleanupCtx, pool); err != nil {
		t.Fatalf("cleanup should not require exclusive access to inbound_intake: %v", err)
	}

	if err := reader.Rollback(ctx); err != nil {
		t.Fatalf("rollback reader transaction: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbound_intake`).Scan(&after); err != nil {
		t.Fatalf("count inbound_intake after cleanup: %v", err)
	}
	if after != 0 {
		t.Fatalf("inbound_intake rows after cleanup = %d, want 0", after)
	}
}

func TestRunMigrations_RepeatedPreparationUsesTrackerWithoutConsumingAttributeSlots(t *testing.T) {
	pool := TestDB(t)
	ctx := context.Background()

	var before int
	if err := pool.QueryRow(ctx, `
		SELECT max(attnum)
		FROM pg_attribute
		WHERE attrelid = 'public.agent_identities'::regclass
		  AND attnum > 0
	`).Scan(&before); err != nil {
		t.Fatalf("read agent_identities max attnum before replay: %v", err)
	}

	if err := runMigrations(ctx, pool); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx, `
		SELECT max(attnum)
		FROM pg_attribute
		WHERE attrelid = 'public.agent_identities'::regclass
		  AND attnum > 0
	`).Scan(&after); err != nil {
		t.Fatalf("read agent_identities max attnum after replay: %v", err)
	}

	var hasTracker bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.schema_migrations') IS NOT NULL`,
	).Scan(&hasTracker); err != nil {
		t.Fatalf("check schema_migrations tracker: %v", err)
	}

	if after != before || !hasTracker {
		t.Fatalf("repeated migration preparation: max attnum %d -> %d, schema_migrations exists=%v; want unchanged attnum and tracker", before, after, hasTracker)
	}

	var tracked int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM schema_migrations
		WHERE filename IN ('003_hitl.sql', '036_hitl_mode.sql', '043_drop_hitl_columns.sql')
	`).Scan(&tracked); err != nil {
		t.Fatalf("count tracked HITL migrations: %v", err)
	}
	if tracked != 3 {
		t.Fatalf("tracked HITL migrations = %d, want 3", tracked)
	}
}

func TestTestDB_PreparationFailureIsFatal(t *testing.T) {
	if os.Getenv(testDBErrorChildEnv) == t.Name() {
		_ = TestDB(t)
		return
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, TestDBURL())
	if err != nil {
		t.Skipf("test database not available: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("test database not available: %v", err)
	}

	dbURL, err := url.Parse(TestDBURL())
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	query := dbURL.Query()
	query.Set("search_path", "e2a_test_missing_schema")
	dbURL.RawQuery = query.Encode()

	output, err := runTestDBErrorChild(t, dbURL.String())
	if err == nil {
		t.Fatalf("TestDB preparation failure exited successfully; want fatal failure\n%s", output)
	}
	if !strings.Contains(output, "failed to prepare test database") {
		t.Fatalf("fatal output missing preparation context:\n%s", output)
	}
}

func TestTestDB_UnavailableDatabaseStillSkips(t *testing.T) {
	if os.Getenv(testDBErrorChildEnv) == t.Name() {
		_ = TestDB(t)
		return
	}

	const unavailableURL = "postgres://e2a:e2a@127.0.0.1:1/e2a_test?sslmode=disable&connect_timeout=1"
	output, err := runTestDBErrorChild(t, unavailableURL)
	if err != nil {
		t.Fatalf("unavailable test database should skip, not fail: %v\n%s", err, output)
	}
	if !strings.Contains(output, "SKIP") {
		t.Fatalf("unavailable test database output missing skip:\n%s", output)
	}
}

func runTestDBErrorChild(t *testing.T, dbURL string) (string, error) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	cmd.Env = testDBChildEnv(t.Name(), dbURL)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func testDBChildEnv(testName, dbURL string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, testDBErrorChildEnv+"=") ||
			strings.HasPrefix(item, "E2A_TEST_DATABASE_URL=") {
			continue
		}
		env = append(env, item)
	}
	return append(env, testDBErrorChildEnv+"="+testName, "E2A_TEST_DATABASE_URL="+dbURL)
}
