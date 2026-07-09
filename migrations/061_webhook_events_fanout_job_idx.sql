-- 061_webhook_events_fanout_job_idx.sql
-- e2a:no-transaction
--
-- Partial index backing the webhook fan-out reconciler
-- (webhookfanout.ReconcilePending), which scans for pending events that never got a
-- fan-out job (status='pending' AND fanout_job_id IS NULL). Almost always the empty
-- set — the trigger's PublishTx/PublishBestEffortTx enqueues the fan-out job in-tx and
-- stamps fanout_job_id atomically — so the partial index is tiny and the reconciler's
-- scan is an index lookup instead of a full scan of the traffic-scaled webhook_events
-- table. Mirrors idx_messages_pending_no_notify_job (058).
--
-- CREATE INDEX CONCURRENTLY so the build does not take a write lock on prod-sized
-- webhook_events (a plain CREATE INDEX would block the outbox fan-out path — every
-- inbound/outbound event write — for the full build). The e2a:no-transaction directive
-- (see internal/identity/migrate.go) skips the BeginTx wrapper — required because
-- Postgres rejects CONCURRENTLY inside a transaction block; the no-transaction runner
-- requires a single statement. Split from 060 (which adds the column transactionally).
--
-- OPS NOTE — invalid-index recovery: if the CONCURRENTLY build is interrupted, Postgres
-- leaves an INVALID index of this name. On the next startup this migration re-runs, but
-- CREATE ... IF NOT EXISTS sees the name and SKIPS the rebuild, marking the migration
-- applied over a broken index — the reconciler then falls back to a seq scan (a
-- performance, not correctness, regression). To recover:
--     DROP INDEX CONCURRENTLY IF EXISTS idx_webhook_events_pending_no_fanout_job;
-- then re-run this statement. Check validity with:
--     SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_webhook_events_pending_no_fanout_job'::regclass;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_webhook_events_pending_no_fanout_job
    ON webhook_events (created_at)
    WHERE status = 'pending' AND fanout_job_id IS NULL;
