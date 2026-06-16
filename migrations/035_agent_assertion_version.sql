-- 035_agent_assertion_version.sql
--
-- auth.md agent-token kill switch (Slice 5b-2). assertion_version is stamped
-- into every identity_assertion + access_token e2a mints for an agent. The
-- token endpoint rejects a presented assertion whose version != the live row,
-- so bumping this column immediately invalidates every token mintable from the
-- old version (the compromised-key / "log this agent out everywhere" lever) —
-- required because assertion-minted access tokens are short-lived with no
-- refresh to starve. Starts at 1; monotonically bumped on revoke.
-- Idempotent + non-destructive (metadata-only ADD COLUMN on PG11+).
ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS assertion_version INTEGER NOT NULL DEFAULT 1;
