-- 021_message_labels_index.sql
-- e2a:no-transaction
--
-- Build the GIN index on messages.labels CONCURRENTLY so production
-- deploys don't take an ACCESS EXCLUSIVE lock on the multi-million-row
-- `messages` table while the index builds. Until this index lands the
-- AND-match filter (?labels=urgent&labels=follow-up) still works — it
-- just does a sequential scan.
--
-- Must run OUTSIDE a transaction (the e2a:no-transaction directive on
-- line 2 tells the migration runner in internal/identity/migrate.go to
-- skip its BeginTx wrapper). Postgres rejects CONCURRENTLY inside a
-- transaction block. Single-statement only per the runner's contract —
-- see noTransactionDirective and looksMultiStatement.
--
-- Idempotent via IF NOT EXISTS. On a fresh DB the table is empty so
-- the build is instant; on prod the build can take a while but never
-- blocks writes.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_messages_labels_gin
    ON messages USING GIN (labels);
