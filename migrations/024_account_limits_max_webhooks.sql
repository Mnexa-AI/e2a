-- 024_account_limits_max_webhooks.sql
--
-- Per-user cap on webhook subscriber count. Reuses the existing
-- account_limits framework (the same mechanism that gates messages
-- per month) rather than introducing a webhook-specific config knob.
-- Default 50 covers every customer use case we can foresee; paid
-- plans can raise it via the account_limits row.
--
-- The handler layer reads this column when creating a new webhook and
-- returns 400 with a clear message when the user is at cap.
--
-- ALTER TABLE ... ADD COLUMN ... DEFAULT 50 is metadata-only on
-- Postgres 11+ (constant default). Safe on the small account_limits
-- table regardless. Idempotent via IF NOT EXISTS.

ALTER TABLE account_limits
    ADD COLUMN IF NOT EXISTS max_webhooks INTEGER NOT NULL DEFAULT 50;
