-- 043_drop_hitl_columns.sql
--
-- Second (final) step of the hitl_enabled / hitl_mode retirement. Migration 042
-- already mapped their behavior forward onto outbound_policy / outbound_scan, and
-- the code stopped reading them in Slice 5b. This drops the now-dead columns (and
-- the hitl_mode CHECK from migration 036 with them), removing 'high_impact' from
-- the system entirely.
--
-- Ordering: 042 reads these columns to backfill, so it MUST run before this drop —
-- migrations apply in filename order, so 042 < 043 is correct. Dropping a column in
-- Postgres is a metadata-only catalog change (no table rewrite), safe on
-- agent_identities. Idempotent via IF EXISTS.
--
-- Pre-GA: a one-step drop is acceptable (no prior binary to roll back to). The
-- surviving HITL mechanism columns (hitl_ttl_seconds, hitl_expiration_action) are
-- the review-queue knobs and are kept.

ALTER TABLE agent_identities DROP COLUMN IF EXISTS hitl_enabled;
ALTER TABLE agent_identities DROP COLUMN IF EXISTS hitl_mode;
