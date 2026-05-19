package identity

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationMode controls how RunMigrations handles pending migrations.
//
// Set via E2A_MIGRATION_MODE env var. Default is ModeAuto.
//
//   - ModeAuto: apply pending migrations in order. Fail (return error)
//     if any apply errors. This is what the hosted deployment uses.
//   - ModeVerify: do not apply anything. Return an error listing pending
//     migrations. For cautious operators who want a separate manual
//     migration step before binary rollout.
//   - ModeSkip: do not apply or check. Log and proceed. For emergency
//     surgery — deploy a binary that won't touch schema.
type MigrationMode string

const (
	ModeAuto   MigrationMode = "auto"
	ModeVerify MigrationMode = "verify"
	ModeSkip   MigrationMode = "skip"
)

// ParseMigrationMode reads a string (typically from env) and returns the
// matching mode, defaulting to ModeAuto on empty input. Returns an error
// for unknown values so a typo is loud, not silent.
func ParseMigrationMode(s string) (MigrationMode, error) {
	switch s {
	case "", string(ModeAuto):
		return ModeAuto, nil
	case string(ModeVerify):
		return ModeVerify, nil
	case string(ModeSkip):
		return ModeSkip, nil
	default:
		return "", fmt.Errorf("invalid migration mode %q (want auto|verify|skip)", s)
	}
}

// schemaMigrationsDDL is bootstrapped by the runner before reading state.
// It can't live in migrations/ itself — chicken-and-egg.
const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    filename    TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

// RunMigrations applies every embedded migration that isn't yet recorded
// in the schema_migrations table. Migrations run in filename-sorted
// order, each in its own transaction; on the first error the function
// returns without applying later migrations.
//
// All migrations should be written idempotent (CREATE/ALTER ... IF NOT
// EXISTS) so re-runs are harmless. The tracker is the source of truth
// for "should we attempt to run this one again"; idempotence is the
// safety net.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, mode MigrationMode) error {
	if mode == ModeSkip {
		log.Printf("[migrate] mode=skip — not running migrations (operator override)")
		return nil
	}

	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	files, err := listMigrations(migrationsFS)
	if err != nil {
		return err
	}

	applied, err := loadApplied(ctx, pool)
	if err != nil {
		return fmt.Errorf("load applied migrations: %w", err)
	}

	pending := make([]string, 0, len(files))
	for _, f := range files {
		if !applied[f] {
			pending = append(pending, f)
		}
	}

	if len(pending) == 0 {
		log.Printf("[migrate] all %d migrations applied", len(files))
		return nil
	}

	if mode == ModeVerify {
		return fmt.Errorf(
			"verify mode: %d pending migration(s): %s — set E2A_MIGRATION_MODE=auto to apply or run them manually",
			len(pending), strings.Join(pending, ", "),
		)
	}

	for _, name := range pending {
		if err := applyOne(ctx, pool, migrationsFS, name); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		log.Printf("[migrate] applied %s", name)
	}
	log.Printf("[migrate] applied %d new migration(s) (%d total)", len(pending), len(files))
	return nil
}

// listMigrations returns every *.sql filename in fsys's root, sorted
// lexicographically. The numeric prefix in our naming scheme makes the
// lexicographic order the apply order.
func listMigrations(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

func loadApplied(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, "SELECT filename FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}
	return applied, rows.Err()
}

// applyOne reads a migration file and applies it in a single transaction
// alongside the schema_migrations bookkeeping insert. If the SQL fails,
// the bookkeeping is rolled back too, so a partial-failure migration is
// safe to retry.
func applyOne(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, name string) error {
	body, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return fmt.Errorf("exec migration: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO schema_migrations (filename) VALUES ($1) ON CONFLICT DO NOTHING",
		name,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

