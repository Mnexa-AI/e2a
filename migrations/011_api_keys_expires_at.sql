-- 011_api_keys_expires_at.sql
--
-- Add an optional expiration timestamp to api_keys so users can issue
-- time-limited credentials. Authentication rejects rows where expires_at
-- has passed; keys with NULL expires_at never expire (backward-compatible
-- default for the keys that exist today).
--
-- Idempotent: ADD COLUMN IF NOT EXISTS leaves existing schemas untouched.
-- Non-destructive on prod-sized tables: ALTER TABLE ... ADD COLUMN with a
-- NULL default is a metadata-only operation in Postgres.

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NULL;

-- Partial index supports the AuthenticateRequest path's `expires_at > now()
-- OR expires_at IS NULL` predicate without scanning never-expiring rows.
-- For self-hosters with thousands of legacy keys (all expires_at NULL),
-- this keeps the auth lookup fast as time-limited keys gradually appear.
CREATE INDEX IF NOT EXISTS idx_api_keys_expires
    ON api_keys (expires_at)
    WHERE expires_at IS NOT NULL;
