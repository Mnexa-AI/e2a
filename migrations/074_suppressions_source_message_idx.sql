-- 074_suppressions_source_message_idx.sql
-- e2a:no-transaction
--
-- Message-lifecycle historical reconstruction reads causal suppressions by
-- source_message_id. This partial index keeps that one-message lookup bounded
-- as the tenant suppression table grows, while excluding manual/global rows
-- that have no causal message.
--
-- CREATE INDEX CONCURRENTLY avoids taking a write lock on the production
-- suppression path. The e2a:no-transaction directive is required because
-- Postgres rejects CONCURRENTLY inside a transaction block; consequently this
-- migration contains exactly one statement.
--
-- OPS NOTE — invalid-index recovery: if the CONCURRENTLY build is interrupted,
-- Postgres leaves an INVALID index of this name. IF NOT EXISTS will then skip
-- rebuilding it. Recover with:
--     DROP INDEX CONCURRENTLY IF EXISTS idx_suppressions_source_message_created;
-- then re-run this statement. Check validity with:
--     SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_suppressions_source_message_created'::regclass;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_suppressions_source_message_created
    ON suppressions (source_message_id, created_at, id)
    WHERE source_message_id IS NOT NULL;
