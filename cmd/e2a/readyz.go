package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Mnexa-AI/e2a/migrations"
)

// readyzHandler reports instance-local readiness: the DB is reachable and the
// latest embedded migration is recorded as applied. Unlike /api/health (shallow
// liveness — never restart on a DB blip), /readyz signals "ready to serve" and
// is the direct guard against the deploy-but-migration-didn't-apply failure
// mode. It must NOT exercise downstream/round-trip dependencies — that is
// /selftest's job (see docs/design/prober-selftest.md).
func readyzHandler(pool *pgxpool.Pool) http.HandlerFunc {
	latest := latestMigration()
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			writeNotReady(w, "database unreachable")
			return
		}
		if latest != "" {
			var applied bool
			err := pool.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)`, latest,
			).Scan(&applied)
			if err != nil || !applied {
				writeNotReady(w, "migrations not applied")
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ready"}`)
	}
}

func writeNotReady(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `{"status":"not_ready","reason":%q}`, reason)
}

// latestMigration returns the highest-sorted embedded migration filename (e.g.
// "037_account_class.sql"), or "" if none are embedded. Numbered filenames sort
// lexically in apply order, matching what RunMigrations records.
func latestMigration() string {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[len(names)-1]
}
