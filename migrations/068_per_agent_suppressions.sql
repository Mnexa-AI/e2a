-- Agent-scoped recipient consent. Agent identifiers intentionally have no
-- foreign key: consent survives hard deletion and recreation of an address.
CREATE TABLE IF NOT EXISTS agent_suppressions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    address    TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL CHECK (source IN ('unsubscribe', 'manual')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, agent_id, address)
);

CREATE TABLE IF NOT EXISTS agent_unsubscribe_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    address    TEXT NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS agent_unsubscribe_tokens_scope_idx
    ON agent_unsubscribe_tokens (user_id, agent_id, address);
