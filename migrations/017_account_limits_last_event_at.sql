-- 017_account_limits_last_event_at.sql
--
-- Adds a monotonic version field to account_limits so the external
-- provisioner (hosted billing sidecar) can safely apply caps in the
-- presence of out-of-order delivery from its own event source.
--
-- The OSS server treats last_event_at as opaque — same property as
-- plan_code / upgrade_url. Whatever external system writes the row
-- supplies a timestamp; the UPSERT in the writer compares incoming
-- vs current and only applies the update when the incoming value is
-- strictly newer. Self-host operators who write account_limits via
-- SQL or a custom admin tool can set last_event_at = now() and the
-- predicate is a no-op for them.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS, backfill from updated_at as
-- a safe floor.

ALTER TABLE account_limits
    ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ;

UPDATE account_limits
   SET last_event_at = updated_at
 WHERE last_event_at IS NULL;
