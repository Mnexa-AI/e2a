#!/usr/bin/env bash
# agentify-verify-setup.sh — boot e2a's local verification stack for the
# autonomous-repo fix lane.
#
# The fix workflow (.github/workflows/feedback-fix.yml, config key
# `verify_setup_script`) runs this BEFORE the fix agent, so the agent can
# verify its change against a real running Postgres — not just `go build`.
# Every credential here is throwaway (demo compose values); there is no
# production to reach.
#
# Stands up: Postgres on host :5433 (CLAUDE.md convention) with the `e2a_test`
# database the Go integration/e2e tiers expect, plus Mailpit for the outbound
# / HITL-notification paths. Enough for `make test-unit`, `make
# test-integration`, and `make test-e2e`.
set -euo pipefail

DB_URL_ADMIN="postgres://e2a:e2a@localhost:5433/postgres?sslmode=disable"
DB_URL_TEST="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable"
export PGPASSWORD=e2a

echo "==> Bringing up Postgres + Mailpit"
docker compose up -d postgres mailpit

echo "==> Waiting for Postgres on :5433"
for _ in $(seq 1 30); do
  if docker compose exec -T postgres pg_isready -U e2a -d e2a >/dev/null 2>&1; then
    ready=1; break
  fi
  sleep 2
done
[ "${ready:-}" = 1 ] || { echo "Postgres did not become ready in time" >&2; exit 1; }

# The compose entrypoint applies migrations/ into the `e2a` database on first
# boot only. The Go test tiers use a separate `e2a_test` DB (see the Makefile's
# E2A_TEST_DATABASE_URL) — create it and apply every migration so the
# integration + e2e suites can run. Migrations are idempotent (CLAUDE.md), so
# re-running is safe.
echo "==> Ensuring e2a_test database exists + migrated"
psql "$DB_URL_ADMIN" -tAc "SELECT 1 FROM pg_database WHERE datname='e2a_test'" | grep -q 1 \
  || psql "$DB_URL_ADMIN" -c "CREATE DATABASE e2a_test OWNER e2a"
for f in migrations/*.sql; do
  echo "    applying $f"
  psql "$DB_URL_TEST" -f "$f" >/dev/null
done

echo "==> Verification stack ready (Postgres :5433, e2a_test migrated, Mailpit :8025)"
