// Package migrations embeds every numbered .sql file in this directory
// so the e2a binary can self-apply schema changes on startup. See
// internal/identity/migrate.go for the runner.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
