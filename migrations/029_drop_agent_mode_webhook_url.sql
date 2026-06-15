-- Drop the legacy per-agent push model: agent_mode + webhook_url.
-- The /v1/webhooks subscriber resource is now the sole push path and
-- WebSocket is open to all agents, so neither column has any meaning.
--
-- Idempotent + non-destructive: DROP COLUMN on Postgres is a
-- metadata-only operation (no full table rewrite), so this is safe on
-- prod-sized agent_identities per CLAUDE.md. We drop the CHECK
-- constraint first because it references both columns.

-- The constraint was created inline by 001_init.sql's CREATE TABLE, so
-- Postgres assigned it a generated name. Drop by that generated name
-- AND, defensively, by the conventional name in case an environment was
-- bootstrapped differently.
ALTER TABLE agent_identities DROP CONSTRAINT IF EXISTS agent_identities_check;
ALTER TABLE agent_identities DROP CONSTRAINT IF EXISTS agent_identities_agent_mode_webhook_check;

ALTER TABLE agent_identities DROP COLUMN IF EXISTS agent_mode;
ALTER TABLE agent_identities DROP COLUMN IF EXISTS webhook_url;
