-- Per-user webhook signing secrets.
--
-- Replaces the previous deployment-wide signing.hmac_secret as the source
-- of truth for HMAC signatures on inbound webhook payloads and HITL
-- magic-link approval tokens. Each user owns one or more secrets; the
-- server signs with the most recently created and accepts any of the
-- user's active secrets for verification (HITL tokens issued before a
-- rotation continue to verify until the matching secret is deleted).
--
-- Rotation is fully user-driven: there is no auto-rotation, no TTL,
-- and no automatic cleanup. Users explicitly create new secrets and
-- delete old ones via the API.

-- gen_random_bytes lives in pgcrypto. Idempotent — safe to run on a DB
-- that already has the extension enabled.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS webhook_signing_secrets (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret          TEXT NOT NULL,                       -- 64 hex chars (32 bytes)
    name            TEXT NOT NULL DEFAULT '',            -- user-supplied label
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_signed_at  TIMESTAMPTZ                          -- updated when server uses to sign
);

CREATE INDEX IF NOT EXISTS webhook_signing_secrets_user_id_created_idx
    ON webhook_signing_secrets (user_id, created_at DESC);

-- Backfill: every existing user gets one secret named "default" so no
-- user is ever caught without a way to verify webhooks. New users get
-- their secret at user-creation time (handled in identity.CreateOrGetUser).
INSERT INTO webhook_signing_secrets (id, user_id, secret, name, created_at)
SELECT
    'wsec_' || encode(gen_random_bytes(8), 'hex'),
    u.id,
    encode(gen_random_bytes(32), 'hex'),
    'default',
    NOW()
FROM users u
WHERE NOT EXISTS (
    SELECT 1 FROM webhook_signing_secrets s WHERE s.user_id = u.id
);
