-- 046_rename_protection_events.sql
--
-- Rename the screening audit log `screening_events` -> `protection_events` (the
-- public-facing name now that the agent config is the "protection" sub-resource),
-- and add a real FK on agent_id so the rows are deleted with their agent — and,
-- transitively, on account deletion (agent_identities ON DELETE CASCADE from
-- users). Previously the table had NO foreign keys, so a GDPR account erasure
-- left these rows (which hold counterparty addresses, content spans, and provider
-- forensics) orphaned indefinitely.
--
-- message_id stays a SOFT ref (no FK): the audit trail must outlive the 30-day
-- message TTL (the original design intent). Only agent_id gets the cascade.
--
-- Idempotent + non-destructive: every step is guarded so a re-run is a no-op.

-- 1. Rename the table (only if the old name still exists and the new one doesn't).
DO $$
BEGIN
    IF EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'screening_events')
       AND NOT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'protection_events') THEN
        ALTER TABLE screening_events RENAME TO protection_events;
    END IF;
END $$;

-- 2. Rename the indexes to match (guarded: only when the old name exists and the
--    new one does not, so a re-run or a partially-applied state is a no-op).
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_class WHERE relname = 'idx_screening_agent_time')
       AND NOT EXISTS (SELECT FROM pg_class WHERE relname = 'idx_protection_agent_time') THEN
        ALTER INDEX idx_screening_agent_time RENAME TO idx_protection_agent_time;
    END IF;
    IF EXISTS (SELECT FROM pg_class WHERE relname = 'idx_screening_message')
       AND NOT EXISTS (SELECT FROM pg_class WHERE relname = 'idx_protection_message') THEN
        ALTER INDEX idx_screening_message RENAME TO idx_protection_message;
    END IF;
END $$;

-- 3. Drop any rows whose agent no longer exists, so the FK can be validated.
--    (These are already-orphaned forensics for deleted agents — exactly the rows
--    the FK now guarantees get cleaned up.)
DELETE FROM protection_events
 WHERE agent_id NOT IN (SELECT id FROM agent_identities);

-- 4. Add the agent_id FK with ON DELETE CASCADE. NOT VALID + VALIDATE keeps the
--    add from taking a long ACCESS EXCLUSIVE scan-lock on a large table.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_constraint WHERE conname = 'protection_events_agent_fk') THEN
        ALTER TABLE protection_events
            ADD CONSTRAINT protection_events_agent_fk
            FOREIGN KEY (agent_id) REFERENCES agent_identities(id) ON DELETE CASCADE
            NOT VALID;
        ALTER TABLE protection_events VALIDATE CONSTRAINT protection_events_agent_fk;
    END IF;
END $$;
