-- 034_api_key_scope.sql
--
-- Scope machinery (Slice 5a / design §5). Every credential carries one of two
-- scopes, and that scope — not the auth method — determines the blast radius:
--
--   account — account-wide admin (agent/domain/key management, account settings).
--             The pre-redesign behavior; what an `e2a_acct_…` key holds.
--   agent   — bound to a single agent (runtime/inbox tier). What a deployed agent
--             holds; pinned to agent_id and barred from account-only operations.
--
-- Backfill is the safe path: existing `e2a_…` keys default to 'account', so no
-- key's authority changes on deploy. agent_id is set only for agent-scoped keys
-- and cascades if the bound agent is deleted. Idempotent + non-destructive
-- (metadata-only ADD COLUMN on PG11+; no table rewrite).
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'account';
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS agent_id TEXT REFERENCES agent_identities(id) ON DELETE CASCADE;

DO $$ BEGIN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_check
        CHECK (scope IN ('account', 'agent'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- An agent-scoped key MUST name its agent; an account-scoped key MUST NOT.
-- This is the row-level invariant behind the runtime enforcement.
DO $$ BEGIN
    ALTER TABLE api_keys ADD CONSTRAINT api_keys_agent_scope_binding
        CHECK ((scope = 'agent') = (agent_id IS NOT NULL));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE INDEX IF NOT EXISTS idx_api_keys_agent ON api_keys(agent_id) WHERE agent_id IS NOT NULL;
