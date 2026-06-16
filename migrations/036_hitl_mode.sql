-- 036_hitl_mode.sql
--
-- HITL action-gate sub-mode (decision 10 / Slice 7b). hitl_enabled stays the
-- on/off switch; hitl_mode refines WHAT gets held when it's on:
--
--   all          — hold every outbound for approval (the pre-7b behavior).
--   high_impact  — hold only a high-impact action taken on untrusted input
--                  (recipient outside the referenced inbound's participants AND
--                  that inbound failed DMARC).
--
-- Default 'all' so every existing hitl_enabled agent is byte-for-byte unchanged;
-- opting into trust-gating is an explicit hitl_mode='high_impact' update.
-- Idempotent + non-destructive (metadata-only ADD COLUMN on PG11+).
ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS hitl_mode TEXT NOT NULL DEFAULT 'all';

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_hitl_mode_check
        CHECK (hitl_mode IN ('all', 'high_impact'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
