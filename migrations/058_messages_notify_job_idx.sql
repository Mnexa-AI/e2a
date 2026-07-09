-- 058_messages_notify_job_idx.sql
-- e2a:no-transaction
--
-- Partial index backing the HITL-notification startup reconciler
-- (hitlnotify.ReconcilePending), which scans for pending_review holds that never
-- got a notification job (status='pending_review' AND notify_job_id IS NULL).
-- Almost always the empty set — the hold accept-tx enqueues in-tx and stamps
-- notify_job_id atomically — so the partial index is tiny and the reconciler's scan
-- is an index lookup instead of a full scan of the multi-million-row messages table.
--
-- CREATE INDEX CONCURRENTLY so the build does not take a write lock on prod-sized
-- messages (a plain CREATE INDEX would block all inbound/outbound mail persistence
-- for the full build). The e2a:no-transaction directive (see
-- internal/identity/migrate.go) skips the BeginTx wrapper — required because
-- Postgres rejects CONCURRENTLY inside a transaction block. Split from 057 (which
-- adds the columns transactionally) because the no-transaction runner requires a
-- single statement.
--
-- OPS NOTE — invalid-index recovery: if the CONCURRENTLY build is interrupted,
-- Postgres leaves an INVALID index of this name. On the next startup this migration
-- re-runs, but CREATE ... IF NOT EXISTS sees the name and SKIPS the rebuild, marking
-- the migration applied over a broken index — the reconciler then falls back to a
-- seq scan at startup (a performance, not correctness, regression). To recover:
--     DROP INDEX CONCURRENTLY IF EXISTS idx_messages_pending_no_notify_job;
-- then re-run this statement. Check validity with:
--     SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_messages_pending_no_notify_job'::regclass;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_messages_pending_no_notify_job
    ON messages (created_at)
    WHERE status = 'pending_review' AND notify_job_id IS NULL;
