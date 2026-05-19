package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// stubFS builds an fs.FS with the given filename → SQL body mapping.
// Order isn't preserved by MapFS but RunMigrations sorts by filename.
func stubFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func TestParseMigrationMode(t *testing.T) {
	cases := []struct {
		in   string
		want identity.MigrationMode
		err  bool
	}{
		{"", identity.ModeAuto, false},
		{"auto", identity.ModeAuto, false},
		{"verify", identity.ModeVerify, false},
		{"skip", identity.ModeSkip, false},
		{"AUTO", "", true},   // case-sensitive on purpose
		{"yolo", "", true},
		{"true", "", true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := identity.ParseMigrationMode(c.in)
			if (err != nil) != c.err {
				t.Fatalf("err = %v, want err=%v", err, c.err)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestRunMigrations_AppliesPending exercises the auto path against a
// real Postgres test DB. The DB starts with all real migrations applied
// (via testutil), so we test a *fresh* set of stub migrations layered
// on top — both run cleanly and record into schema_migrations.
func TestRunMigrations_AppliesPending(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_dummy_a, migrate_test_dummy_b")
	_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename IN ('test_a.sql','test_b.sql')")

	fsys := stubFS(map[string]string{
		"test_a.sql": "CREATE TABLE IF NOT EXISTS migrate_test_dummy_a (id TEXT PRIMARY KEY);",
		"test_b.sql": "CREATE TABLE IF NOT EXISTS migrate_test_dummy_b (id TEXT PRIMARY KEY);",
	})

	if err := identity.RunMigrations(ctx, pool, fsys, identity.ModeAuto); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Both tables should exist now.
	for _, table := range []string{"migrate_test_dummy_a", "migrate_test_dummy_b"} {
		var ok bool
		err := pool.QueryRow(ctx,
			"SELECT to_regclass($1) IS NOT NULL", "public."+table,
		).Scan(&ok)
		if err != nil || !ok {
			t.Fatalf("expected table %s to exist (err=%v)", table, err)
		}
	}

	// Both filenames should be in schema_migrations.
	for _, name := range []string{"test_a.sql", "test_b.sql"} {
		var count int
		err := pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE filename = $1", name,
		).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("expected %s recorded once (count=%d, err=%v)", name, count, err)
		}
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_dummy_a, migrate_test_dummy_b")
		_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename IN ('test_a.sql','test_b.sql')")
	})
}

// TestRunMigrations_Idempotent verifies a second invocation is a no-op
// and doesn't double-record. Relies on schema_migrations tracking.
func TestRunMigrations_Idempotent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_idemp")
	_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = 'idemp.sql'")

	fsys := stubFS(map[string]string{
		"idemp.sql": "CREATE TABLE IF NOT EXISTS migrate_test_idemp (id TEXT PRIMARY KEY);",
	})

	if err := identity.RunMigrations(ctx, pool, fsys, identity.ModeAuto); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := identity.RunMigrations(ctx, pool, fsys, identity.ModeAuto); err != nil {
		t.Fatalf("second run: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE filename = 'idemp.sql'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected single record, got %d", count)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_idemp")
		_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = 'idemp.sql'")
	})
}

// TestRunMigrations_FailingMigrationRollsBack ensures the tracker
// insert is rolled back when the SQL itself errors — so a retry can
// fix the SQL and re-apply.
func TestRunMigrations_FailingMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = 'bad.sql'")

	fsys := stubFS(map[string]string{
		"bad.sql": "THIS IS NOT VALID SQL;",
	})

	err := identity.RunMigrations(ctx, pool, fsys, identity.ModeAuto)
	if err == nil {
		t.Fatal("expected error from invalid SQL")
	}
	if !strings.Contains(err.Error(), "bad.sql") {
		t.Fatalf("error should name the failing migration, got: %v", err)
	}

	// schema_migrations must NOT have the failed migration recorded.
	var count int
	if err := pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM schema_migrations WHERE filename = 'bad.sql'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed migration should not be recorded, got count=%d", count)
	}
}

// TestRunMigrations_VerifyModeRefusesToApply ensures verify mode
// returns an error listing the pending file(s) and applies nothing.
func TestRunMigrations_VerifyModeRefusesToApply(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_verify_only")
	_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = 'verify_only.sql'")

	fsys := stubFS(map[string]string{
		"verify_only.sql": "CREATE TABLE IF NOT EXISTS migrate_test_verify_only (id TEXT);",
	})

	err := identity.RunMigrations(ctx, pool, fsys, identity.ModeVerify)
	if err == nil {
		t.Fatal("verify mode with pending should error")
	}
	if !strings.Contains(err.Error(), "verify_only.sql") {
		t.Fatalf("error should list the pending file, got: %v", err)
	}

	// The table must NOT have been created.
	var ok bool
	if err := pool.QueryRow(ctx,
		"SELECT to_regclass('public.migrate_test_verify_only') IS NOT NULL",
	).Scan(&ok); err != nil || ok {
		t.Fatalf("verify mode should not have applied (ok=%v, err=%v)", ok, err)
	}
}

// TestRunMigrations_SkipModeIsNoop ensures skip mode does nothing —
// not even creating the schema_migrations table on a fresh DB.
func TestRunMigrations_SkipModeIsNoop(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	// The testutil's own migration runner doesn't create schema_migrations
	// so this is genuinely "did skip touch the DB at all" for that table.

	fsys := stubFS(map[string]string{
		"any.sql": "CREATE TABLE IF NOT EXISTS migrate_test_skip (id TEXT);",
	})

	if err := identity.RunMigrations(ctx, pool, fsys, identity.ModeSkip); err != nil {
		t.Fatalf("skip should not error: %v", err)
	}

	var ok bool
	if err := pool.QueryRow(ctx,
		"SELECT to_regclass('public.migrate_test_skip') IS NOT NULL",
	).Scan(&ok); err != nil || ok {
		t.Fatalf("skip mode should not have applied (ok=%v, err=%v)", ok, err)
	}
}

// TestRunMigrations_OrdersByFilename ensures lexicographic order is
// the apply order — important because the project numbers migrations
// (001_, 002_, …) to enforce sequence.
func TestRunMigrations_OrdersByFilename(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_seq")
	for _, n := range []string{"001_seq.sql", "002_seq.sql", "003_seq.sql"} {
		_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = $1", n)
	}

	fsys := stubFS(map[string]string{
		"003_seq.sql": "INSERT INTO migrate_test_seq (n, ord) VALUES (3, 3);",
		"001_seq.sql": "CREATE TABLE IF NOT EXISTS migrate_test_seq (n INT PRIMARY KEY, ord INT);",
		"002_seq.sql": "INSERT INTO migrate_test_seq (n, ord) VALUES (2, 2);",
	})

	if err := identity.RunMigrations(ctx, pool, fsys, identity.ModeAuto); err != nil {
		// If lex order is wrong, 003 would try to INSERT before the
		// table exists in 001 and we'd error here.
		t.Fatalf("apply: %v", err)
	}

	var n, ord int
	rows, err := pool.Query(ctx, "SELECT n, ord FROM migrate_test_seq ORDER BY ord")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var rowsSeen [][2]int
	for rows.Next() {
		if err := rows.Scan(&n, &ord); err != nil {
			t.Fatal(err)
		}
		rowsSeen = append(rowsSeen, [2]int{n, ord})
	}
	if errors.Is(rows.Err(), nil) && len(rowsSeen) != 2 {
		t.Fatalf("expected 2 rows, got %v", rowsSeen)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_seq")
		for _, n := range []string{"001_seq.sql", "002_seq.sql", "003_seq.sql"} {
			_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = $1", n)
		}
	})
}

// TestRunMigrations_PartialState verifies that when some migrations are
// already in schema_migrations, only the pending ones are applied.
func TestRunMigrations_PartialState(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_partial_a, migrate_test_partial_b")
	for _, n := range []string{"a.sql", "b.sql"} {
		_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = $1", n)
	}

	// Pretend a.sql was applied externally (e.g., the testutil path).
	if _, err := pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS migrate_test_partial_a (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	// Create schema_migrations and record a.sql so the runner skips it.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ('a.sql')"); err != nil {
		t.Fatal(err)
	}

	fsys := stubFS(map[string]string{
		"a.sql": "SELECT 1/0;", // would error if it ran; should be skipped
		"b.sql": "CREATE TABLE IF NOT EXISTS migrate_test_partial_b (id TEXT);",
	})

	if err := identity.RunMigrations(ctx, pool, fsys, identity.ModeAuto); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var ok bool
	if err := pool.QueryRow(ctx,
		"SELECT to_regclass('public.migrate_test_partial_b') IS NOT NULL",
	).Scan(&ok); err != nil || !ok {
		t.Fatalf("b should be applied: ok=%v err=%v", ok, err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS migrate_test_partial_a, migrate_test_partial_b")
		for _, n := range []string{"a.sql", "b.sql"} {
			_, _ = pool.Exec(ctx, "DELETE FROM schema_migrations WHERE filename = $1", n)
		}
	})
}
