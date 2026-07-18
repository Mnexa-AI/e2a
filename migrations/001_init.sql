-- e2a schema — single idempotent migration
-- Safe to rerun: uses IF NOT EXISTS throughout, no DROP statements.
-- See docs/design-schema-reset.md for full context.

-- Accounts
CREATE TABLE IF NOT EXISTS users (
    id              TEXT PRIMARY KEY,
    email           TEXT UNIQUE NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    google_subject  TEXT UNIQUE NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_sessions (
    token        TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_expires ON user_sessions(expires_at);

-- Domains
CREATE TABLE IF NOT EXISTS domains (
    domain             TEXT PRIMARY KEY,
    user_id            TEXT REFERENCES users(id) ON DELETE CASCADE,
    verified           BOOLEAN NOT NULL DEFAULT false,
    verification_token TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_at        TIMESTAMPTZ,
    CHECK (domain = lower(domain))
);

CREATE INDEX IF NOT EXISTS idx_domains_user ON domains(user_id);

-- Seed shared domain system row
INSERT INTO domains (domain, user_id, verified, verified_at)
VALUES ('agents.e2a.dev', NULL, true, now())
ON CONFLICT (domain) DO NOTHING;

-- Agent identities
CREATE TABLE IF NOT EXISTS agent_identities (
    id                TEXT PRIMARY KEY,
    registered_domain TEXT NOT NULL REFERENCES domains(domain) ON DELETE NO ACTION,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name              TEXT NOT NULL DEFAULT '',
    webhook_url       TEXT NOT NULL DEFAULT '',
    agent_mode        TEXT NOT NULL DEFAULT 'cloud' CHECK (agent_mode IN ('cloud', 'local')),
    public            BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (agent_mode = 'local') OR
        (agent_mode = 'cloud' AND webhook_url <> '')
    )
);

CREATE INDEX IF NOT EXISTS idx_agents_user ON agent_identities(user_id);
CREATE INDEX IF NOT EXISTS idx_agents_registered_domain ON agent_identities(registered_domain);

-- API keys (hash-only storage)
CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    key_prefix   TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);

-- Messages
CREATE TABLE IF NOT EXISTS messages (
    id                  TEXT PRIMARY KEY,
    agent_id            TEXT NOT NULL REFERENCES agent_identities(id) ON DELETE CASCADE,
    direction           TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    sender              TEXT NOT NULL DEFAULT '',
    recipient           TEXT NOT NULL DEFAULT '',
    subject             TEXT NOT NULL DEFAULT '',
    email_message_id    TEXT NOT NULL DEFAULT '',
    provider_message_id TEXT NOT NULL DEFAULT '',
    method              TEXT CHECK (method IN ('smtp', 'webhook')),
    message_type        TEXT CHECK (message_type IN ('send', 'reply', 'test')),
    raw_message         BYTEA,
    auth_headers        JSONB,
    conversation_id     TEXT NOT NULL DEFAULT '',
    inbox_status        TEXT CHECK (inbox_status IN ('unread', 'read')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 days'
);

CREATE INDEX IF NOT EXISTS idx_messages_agent_created ON messages(agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_expires ON messages(expires_at);

-- Webhook deliveries
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    message_id      TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts    INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    last_error      TEXT NOT NULL DEFAULT '',
    last_attempt_at TIMESTAMPTZ,
    next_retry_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 days'
);

-- Beta metrics (usage tracking only, no billing enforcement)
CREATE TABLE IF NOT EXISTS usage_events (
    id          TEXT PRIMARY KEY,
    user_id     TEXT REFERENCES users(id) ON DELETE SET NULL,
    agent_id    TEXT NOT NULL,
    domain      TEXT NOT NULL,
    direction   TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    event_type  TEXT NOT NULL DEFAULT 'message',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usage_summaries (
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bucket_date     DATE NOT NULL,
    inbound_count   INTEGER NOT NULL DEFAULT 0,
    outbound_count  INTEGER NOT NULL DEFAULT 0,
    total_count     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, bucket_date)
);
