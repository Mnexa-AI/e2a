-- OAuth 2.1 schema for the MCP authorization layer.
--
-- Per .claude/design/mcp-system.md §4.3. Three tables, all idempotent:
--
--   oauth_clients               — registered OAuth clients (Claude Code, Cursor,
--                                  Cline, etc.). Public via Dynamic Client
--                                  Registration (RFC 7591) or admin-curated.
--   oauth_authorization_codes   — one-shot codes minted at consent and
--                                  exchanged at /api/oauth/token. 60s lifetime.
--   oauth_tokens                — access + refresh tokens. Access ~1h, refresh
--                                  ~30d. refresh_chain_id groups rotations so
--                                  refresh-token reuse triggers chain-wide
--                                  revocation per RFC 6749 §10.4.
--
-- Token format conventions (enforced in application code, not DB):
--   client_id      = 'mcp_'   + nanoid(12)
--   code           = 'oace_'  + hex(32)
--   access_token   = 'ate2a_' + hex(32)
--   refresh_token  = 'rte2a_' + hex(32)
--
-- All FKs CASCADE on user_id / client_id deletion: tokens die with their owner.

CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id            TEXT PRIMARY KEY,
    client_name          TEXT NOT NULL,
    redirect_uris        TEXT[] NOT NULL,
    client_type          TEXT NOT NULL CHECK (client_type IN ('public', 'confidential')),
    -- Null for public clients (PKCE-only authentication). Stored as a
    -- hash, never plaintext, so a DB leak can't reveal client secrets
    -- (which authenticate at /api/oauth/token).
    client_secret_hash   TEXT,
    metadata             JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_via          TEXT NOT NULL CHECK (created_via IN ('dcr', 'admin'))
);

CREATE TABLE IF NOT EXISTS oauth_authorization_codes (
    code                    TEXT PRIMARY KEY,
    client_id               TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id                 TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Selected by the user on the consent screen; null when the
    -- user has zero agents and declined auto-create.
    agent_email             TEXT,
    redirect_uri            TEXT NOT NULL,
    code_challenge          TEXT NOT NULL,
    code_challenge_method   TEXT NOT NULL CHECK (code_challenge_method = 'S256'),
    scope                   TEXT NOT NULL,
    expires_at              TIMESTAMPTZ NOT NULL,
    -- Single-use: set to NOW() on first successful exchange at
    -- /api/oauth/token. A second exchange attempt sees consumed_at
    -- IS NOT NULL and triggers token-chain revocation (defense in
    -- depth against replay attacks).
    consumed_at             TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS oauth_codes_user ON oauth_authorization_codes(user_id);

CREATE TABLE IF NOT EXISTS oauth_tokens (
    access_token         TEXT PRIMARY KEY,
    -- Unique when present; null after rotation (a refresh-grant
    -- invalidates the previous refresh_token by NULLing it out).
    refresh_token        TEXT UNIQUE,
    -- Groups all access/refresh tokens in a rotation chain so reuse
    -- of an old refresh_token can revoke every sibling at once.
    refresh_chain_id     TEXT NOT NULL,
    client_id            TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Pinned default agent inbox for this connection. Tool calls
    -- can override per-call; this is the fallback.
    agent_email          TEXT,
    scope                TEXT NOT NULL,
    expires_at           TIMESTAMPTZ NOT NULL,
    refresh_expires_at   TIMESTAMPTZ,
    revoked_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Hot path: validate Bearer token for an API request.
-- Partial index because revoked tokens are dead weight in the index.
CREATE INDEX IF NOT EXISTS oauth_tokens_user_active
    ON oauth_tokens(user_id) WHERE revoked_at IS NULL;

-- Refresh-grant lookup: find by refresh_token.
-- Partial because rotated rows have refresh_token = NULL.
CREATE INDEX IF NOT EXISTS oauth_tokens_refresh
    ON oauth_tokens(refresh_token) WHERE refresh_token IS NOT NULL;

-- "List my OAuth connections" query — per (client, user) pair.
CREATE INDEX IF NOT EXISTS oauth_tokens_client
    ON oauth_tokens(client_id, user_id);

-- Chain-wide revocation: find every sibling of a rotated token.
CREATE INDEX IF NOT EXISTS oauth_tokens_chain
    ON oauth_tokens(refresh_chain_id);
