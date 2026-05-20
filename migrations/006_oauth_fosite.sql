-- OAuth 2.1 schema backing the ory/fosite-based authorization server
-- (.claude/design/mcp-system.md §4.3; reshaped to fit fosite's storage
-- interfaces).
--
-- Five tables:
--   oauth_clients          — registered clients (DCR-issued or admin)
--   oauth_auth_codes       — fosite AuthorizeCodeStorage (60s, single-use)
--   oauth_access_tokens    — fosite AccessTokenStorage   (1h)
--   oauth_refresh_tokens   — fosite RefreshTokenStorage  (30d, rotating)
--   oauth_pkce_requests    — fosite PKCERequestStorage   (transient)
--
-- Storage model:
--
-- `signature` columns are fosite-managed HMAC outputs (NOT the bearer
-- plaintext). The plaintext is `signature.salt` and is shown to the
-- client exactly once at issuance — never persisted. A DB read
-- therefore yields no usable bearer credentials; an attacker would
-- need to break HMAC-SHA256 preimage on a high-entropy input.
--
-- `request` columns hold the fosite-serialized fosite.Requester:
-- client_id, scopes, granted_scopes, requested_at, granted_at, plus
-- the e2a-specific session (carrying user_id + agent_email). JSONB
-- so future fields don't require a migration.
--
-- `request_id` columns are fosite's internal grouping ID. The token
-- endpoint's reuse defense (RFC 6749 §10.4/§10.5) revokes by request
-- ID across access + refresh, so we index on it.
--
-- `active` boolean (not just delete-on-consume) lets fosite implement
-- both "single-use" and "reuse detection." When a code or refresh
-- token is consumed/rotated, the row stays around with active=false;
-- a subsequent lookup that finds an inactive row fires the §10.4/§10.5
-- chain revocation path. Rows are reaped by the retention worker
-- (see internal/oauth/store.go DeleteExpired) long after they stop
-- mattering for replay detection.
--
-- All FKs CASCADE on user_id / client_id deletion: when a user goes
-- away, their tokens go with them. oauth_clients.created_by_user_id
-- uses ON DELETE SET NULL because client_ids are shared across users
-- (Claude Code's mcp_… is the same row for every user who's
-- authorized it).

-- Clients (DCR per RFC 7591 or admin-curated)
CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id                  TEXT PRIMARY KEY,
    client_name                TEXT NOT NULL,
    redirect_uris              TEXT[] NOT NULL,
    grant_types                TEXT[] NOT NULL,
    response_types             TEXT[] NOT NULL,
    scopes                     TEXT[] NOT NULL,
    audiences                  TEXT[] NOT NULL DEFAULT '{}'::TEXT[],
    token_endpoint_auth_method TEXT NOT NULL DEFAULT 'none',
    -- NULL for public clients (PKCE-only). When present, stored as
    -- a hash (never plaintext) so DB compromise doesn't leak secrets.
    client_secret_hash         TEXT,
    public                     BOOLEAN NOT NULL DEFAULT TRUE,
    metadata                   JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_via                TEXT NOT NULL CHECK (created_via IN ('dcr', 'admin')),
    -- NULL for DCR (anonymous per RFC 7591 §2) and for admin rows
    -- where no user-of-record is meaningful. When the referenced
    -- user is deleted, this clears to NULL; the row itself persists
    -- because other users may have authorized the same client_id.
    created_by_user_id         TEXT REFERENCES users(id) ON DELETE SET NULL
);

-- Authorization codes (60s lifetime, single-use w/ RFC 6749 §10.5 reuse defense)
CREATE TABLE IF NOT EXISTS oauth_auth_codes (
    signature     TEXT PRIMARY KEY,
    request_id    TEXT NOT NULL,
    client_id     TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request       JSONB NOT NULL,
    requested_at  TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    -- false = consumed; lookup of an inactive row triggers fosite's
    -- reuse-detection path (revokes the issued access + refresh).
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS oauth_auth_codes_request
    ON oauth_auth_codes(request_id);

-- Access tokens (1h lifetime)
CREATE TABLE IF NOT EXISTS oauth_access_tokens (
    signature     TEXT PRIMARY KEY,
    request_id    TEXT NOT NULL,
    client_id     TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request       JSONB NOT NULL,
    requested_at  TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Hot path: validate Bearer for an API request. Partial index excludes
-- revoked rows — they're dead weight on every authed request.
CREATE INDEX IF NOT EXISTS oauth_access_tokens_user_active
    ON oauth_access_tokens(user_id) WHERE revoked_at IS NULL;

-- Reuse-defense revoke (RFC 6749 §10.4/§10.5): fosite calls
-- RevokeAccessToken(request_id) to wipe sibling tokens; we need a
-- fast scan by request_id.
CREATE INDEX IF NOT EXISTS oauth_access_tokens_request
    ON oauth_access_tokens(request_id);

-- "List my OAuth connections" query — per (client, user) pair.
CREATE INDEX IF NOT EXISTS oauth_access_tokens_client
    ON oauth_access_tokens(client_id, user_id);

-- Refresh tokens (30d lifetime, single-use w/ chain rotation)
CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    signature         TEXT PRIMARY KEY,
    request_id        TEXT NOT NULL,
    client_id         TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Signature of the paired access token, when known. fosite
    -- passes this on CreateRefreshTokenSession so a future revoke can
    -- cascade to the access row without a separate index lookup.
    access_signature  TEXT,
    request           JSONB NOT NULL,
    requested_at      TIMESTAMPTZ NOT NULL,
    expires_at        TIMESTAMPTZ,
    -- Same "soft inactive for reuse detection" semantic as auth codes.
    active            BOOLEAN NOT NULL DEFAULT TRUE,
    revoked_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS oauth_refresh_tokens_request
    ON oauth_refresh_tokens(request_id);

-- PKCE request sessions: transient state from authorize → token. fosite
-- stores the code_challenge + method per request_id so token-exchange
-- can validate the verifier without re-reading the auth code row.
CREATE TABLE IF NOT EXISTS oauth_pkce_requests (
    signature     TEXT PRIMARY KEY,
    request_id    TEXT NOT NULL,
    client_id     TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    request       JSONB NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS oauth_pkce_requests_request
    ON oauth_pkce_requests(request_id);
