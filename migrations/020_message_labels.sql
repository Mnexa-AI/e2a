-- 020_message_labels.sql
--
-- Add the labels[] column on messages. Companion file 021 creates the
-- GIN index in a separate, CONCURRENTLY-built migration so prod deploys
-- don't take an ACCESS EXCLUSIVE lock on a multi-million-row table.
--
-- labels[] is the canonical store — bare strings, no join table, no
-- separate `labels` resource. Plain tags an agent emits, plus a
-- reserved `e2a:` prefix for future server-applied system labels.
--
-- Default '{}'::text[] is a constant: Postgres 11+ records the new
-- column metadata-only and rewrites no rows. Safe on prod.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS labels TEXT[] NOT NULL DEFAULT '{}';
