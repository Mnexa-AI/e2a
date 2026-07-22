-- 075_webhook_events_message_idx.sql
-- e2a:no-transaction
--
-- Message-lifecycle historical reconstruction reads retained observations by
-- message_id. This partial index keeps that one-message lookup bounded as the
-- traffic-scaled webhook event outbox grows, while excluding events that are
-- not associated with a retained message.
--
-- CREATE INDEX CONCURRENTLY avoids taking a write lock on webhook event
-- ingestion. The e2a:no-transaction directive is required because Postgres
-- rejects CONCURRENTLY inside a transaction block; consequently this migration
-- contains exactly one statement.
--
-- OPS NOTE — invalid-index recovery: if the CONCURRENTLY build is interrupted,
-- Postgres leaves an INVALID index of this name. IF NOT EXISTS will then skip
-- rebuilding it. Recover with:
--     DROP INDEX CONCURRENTLY IF EXISTS idx_webhook_events_message_created;
-- then re-run this statement. Check validity with:
--     SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_webhook_events_message_created'::regclass;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_webhook_events_message_created
    ON webhook_events (message_id, created_at, id)
    WHERE message_id IS NOT NULL;
