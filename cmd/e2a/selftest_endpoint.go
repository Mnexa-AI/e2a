package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// selftestHandler serves /selftest: deep dependency diagnostics in IETF
// health+json shape (draft-inadarei-api-health-check) — database reachable,
// migrations applied, SMTP listener bound. It deliberately does NOT run the
// full SMTP→webhook round-trip (that is the external e2a-prober's job, which
// needs probe credentials and an internal sink); keeping the round-trip out of
// the server avoids embedding credentials and weakening the webhook deliverer's
// HTTPS requirement. Auth-gated by the internal API secret when one is
// configured (the output reveals topology). Fail-closed like /api/internal/*:
// when no secret is configured, the endpoint is open only in development and
// returns 503 in production (never serve diagnostics unauthenticated in prod).
func selftestHandler(pool *pgxpool.Pool, smtpAddr, internalSecret string, devMode bool) http.HandlerFunc {
	latest := latestMigration()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case internalSecret != "":
			if !bearerEquals(r, internalSecret) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		case !devMode:
			// No secret configured in production → fail closed.
			writeNotReady(w, "selftest requires E2A_INTERNAL_API_SECRET")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		checks := map[string]string{
			"database:reachable": checkStatus(pool.Ping(ctx) == nil),
			"smtp:listening":     checkStatus(dialOK(smtpAddr)),
			"migrations:applied": checkStatus(migrationsOK(ctx, pool, latest)),
		}
		overall := "pass"
		for _, s := range checks {
			if s != "pass" {
				overall = "fail"
				break
			}
		}

		// IETF health+json: checks keyed "component:measurement" → array of
		// detail objects, each with a status.
		detail := map[string]any{}
		for k, s := range checks {
			detail[k] = []map[string]string{{"status": s}}
		}
		w.Header().Set("Content-Type", "application/health+json")
		if overall != "pass" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": overall, "checks": detail})
	}
}

func migrationsOK(ctx context.Context, pool *pgxpool.Pool, latest string) bool {
	applied, err := latestMigrationApplied(ctx, pool, latest)
	return err == nil && applied
}

func checkStatus(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// dialOK reports whether the SMTP listener accepts a TCP connection. A bare
// ":2525" addr is dialed against loopback.
func dialOK(addr string) bool {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	addr = strings.Replace(addr, "0.0.0.0:", "127.0.0.1:", 1)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func bearerEquals(r *http.Request, secret string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}
