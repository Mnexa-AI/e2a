-- 018_account_limits_last_event_id.sql
--
-- Companion to migration 017's last_event_at. Adds an event-id
-- tiebreaker column so the external provisioner can resolve
-- same-second ordering deterministically. OSS treats the value as
-- opaque, same property as plan_code / upgrade_url / last_event_at.
--
-- See e2a-ops migration 003 for the rationale and the predicate
-- shape — they mirror.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS. No backfill needed; the
-- writer's predicate handles NULL last_event_id correctly.

ALTER TABLE account_limits
    ADD COLUMN IF NOT EXISTS last_event_id TEXT;
