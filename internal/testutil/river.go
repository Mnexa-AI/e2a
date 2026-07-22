package testutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// resetRiverOperationalState gives each contract-server start an empty River
// runtime while retaining river_migration, River's schema-version ledger.
func resetRiverOperationalState(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin River test reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT schemaname, tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename LIKE 'river\_%' ESCAPE '\'
		  AND tablename <> 'river_migration'
		ORDER BY tablename
	`)
	if err != nil {
		return fmt.Errorf("list River operational tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			rows.Close()
			return fmt.Errorf("scan River operational table: %w", err)
		}
		tables = append(tables, pgx.Identifier{schema, table}.Sanitize())
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate River operational tables: %w", err)
	}
	rows.Close()

	if len(tables) > 0 {
		if _, err := tx.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE"); err != nil {
			return fmt.Errorf("truncate River operational tables: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit River test reset: %w", err)
	}
	return nil
}
