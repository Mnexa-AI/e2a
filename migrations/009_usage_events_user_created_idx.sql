-- 009_usage_events_user_created_idx.sql
-- e2a:no-transaction
--
-- Index supporting per-user time-range scans of usage_events. Powers
-- the EXISTS checks in the hosted v_signup_funnel view and any future
-- per-user activity queries. Leading column is user_id so the index
-- also serves queries that filter on user_id alone.
--
-- CREATE INDEX CONCURRENTLY so the index build does not block writes
-- on self-hosters with sizeable usage_events tables. The runner's
-- e2a:no-transaction directive (see internal/identity/migrate.go) skips
-- the BeginTx wrapper for this file — required because Postgres
-- rejects CONCURRENTLY inside a transaction. Single-statement only
-- per the runner's contract.
--
-- Idempotent via IF NOT EXISTS. On a fresh DB the table will be small
-- and the build is near-instant; on hosted prod (data since 2026-04-25
-- when usage tracking was enabled) the table is still small as of
-- this migration.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_events_user_created
    ON usage_events (user_id, created_at);
