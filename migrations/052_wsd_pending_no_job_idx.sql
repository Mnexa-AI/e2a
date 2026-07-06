-- 052_wsd_pending_no_job_idx.sql
-- e2a:no-transaction
--
-- Partial index backing the webhook delivery reconciler
-- (webhookdelivery.ReconcileWorker), which runs every minute and scans for
-- STRANDED deliveries: pending rows that never got a River job
-- (status='pending' AND job_id IS NULL). Almost always the empty set — the
-- outbox drain enqueues in-tx and /test + redelivery enqueue right after
-- insert — so the partial index is tiny and the reconciler's scan is an
-- index-only lookup instead of a full table scan every tick.
--
-- CREATE INDEX CONCURRENTLY so the build does not take a write lock on a
-- prod-sized webhook_subscriber_deliveries. The runner's e2a:no-transaction
-- directive (see internal/identity/migrate.go) skips the BeginTx wrapper —
-- required because Postgres rejects CONCURRENTLY inside a transaction block.
--
-- OPS NOTE — invalid-index recovery: if the CONCURRENTLY build is interrupted,
-- Postgres leaves an INVALID index of this name. On the next startup this
-- migration re-runs, but CREATE ... IF NOT EXISTS sees the name and SKIPS the
-- rebuild, marking the migration applied over a broken index — the reconciler
-- then falls back to a seq scan every minute (a performance, not correctness,
-- regression: delivery still works). To recover, manually:
--     DROP INDEX CONCURRENTLY IF EXISTS idx_wsd_pending_no_job;
-- then re-run this statement. Check validity with:
--     SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_wsd_pending_no_job'::regclass;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_wsd_pending_no_job
    ON webhook_subscriber_deliveries (id)
    WHERE status = 'pending' AND job_id IS NULL;
