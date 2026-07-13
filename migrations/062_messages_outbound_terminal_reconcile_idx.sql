-- 062_messages_outbound_terminal_reconcile_idx.sql
-- e2a:no-transaction
--
-- Partial covering index backing the one-minute outbound terminal reconciler,
-- which scans accepted/sending messages with a stamped send_job_id and then joins
-- those ids to river_job to find terminal or already-pruned jobs. The LIMIT bounds
-- how many candidates one pass processes, while this index bounds the messages-side
-- scan and supplies the reconciler's deterministic created_at/id order without a
-- sort on the traffic-scaled messages table.
--
-- CREATE INDEX CONCURRENTLY avoids blocking inbound/outbound message persistence
-- while the index is built on a production-sized messages table. The
-- e2a:no-transaction directive (see internal/identity/migrate.go) skips the BeginTx
-- wrapper because Postgres rejects CONCURRENTLY inside a transaction block. The
-- no-transaction runner requires this file to contain a single statement.
--
-- OPS NOTE — invalid-index recovery: if the CONCURRENTLY build is interrupted,
-- Postgres leaves an INVALID index of this name. On the next startup this migration
-- re-runs, but CREATE ... IF NOT EXISTS sees the name and skips the rebuild, marking
-- the migration applied over a broken index. To recover:
--     DROP INDEX CONCURRENTLY IF EXISTS idx_messages_outbound_terminal_reconcile;
-- then re-run this statement. Check validity with:
--     SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_messages_outbound_terminal_reconcile'::regclass;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_messages_outbound_terminal_reconcile
    ON messages (created_at, id) INCLUDE (send_job_id)
    WHERE direction = 'outbound'
      AND delivery_status IN ('accepted', 'sending')
      AND send_job_id IS NOT NULL;
