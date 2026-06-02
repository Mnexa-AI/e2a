-- 023_webhooks.sql
--
-- Top-level webhooks-as-a-resource feature, slice 1 foundation. Replaces
-- the implicit "one URL per agent" model (agent_identities.webhook_url)
-- with a multi-subscriber CRUD resource. The legacy field is preserved
-- and keeps working unchanged — both delivery pathways fire side-by-side
-- on inbound events. See the final design in tmp/e2a_webhooks_design.md
-- for the full feature scope; this migration adds only the webhooks
-- catalog table. Per-attempt delivery state lives in
-- webhook_subscriber_deliveries (migration 025).
--
-- Decisions reflected here:
-- - signing_secret stored plaintext, matching the existing per-agent
--   signing-secret pattern. A separate, system-wide work item will
--   envelope-encrypt all signing secrets before the first production
--   customer with sensitive data ships. Don't draw a new security line
--   webhook-specifically.
-- - filters live as a JSONB column (not a join table). At max 50
--   webhooks per user, in-Go filter matching over JSONB candidates is
--   trivial and the routing code stays simple. A GIN index on filters
--   can be added later non-breakingly if scale demands it.
-- - signing_secret_prev + signing_secret_prev_expires_at hold the
--   previous secret during the 24h rotation grace window. Both
--   signatures are sent during this window so receivers can roll
--   forward without dropping deliveries.
-- - auto_disabled_at is nullable and set by the auto-disable worker
--   (slice 4); the column lands here so storage doesn't churn between
--   slices.
--
-- Idempotent: all CREATE statements use IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS webhooks (
    id                              TEXT PRIMARY KEY,
    user_id                         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url                             TEXT NOT NULL CHECK (url <> '' AND length(url) <= 2048),
    description                     TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 200),
    -- events is a non-empty array of event type names. Caller-supplied
    -- entries are checked against an allowlist at the handler layer
    -- (the CHECK constraint only enforces non-emptiness so a typo at
    -- write time fails immediately rather than producing a webhook
    -- that subscribes to nothing).
    events                          TEXT[] NOT NULL DEFAULT '{}',
    -- filters carries optional scope filters as a JSONB object with
    -- keys agent_ids, conversation_ids, labels — each a string array.
    -- An absent or empty key means "no constraint of that type".
    -- Routing applies AND across keys, OR within a key (see design).
    filters                         JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Plaintext per-webhook HMAC key. Used by the delivery worker to
    -- sign the X-E2A-Signature header. Returned to the caller ONCE on
    -- POST /webhooks and POST /webhooks/{id}/rotate-secret; GET
    -- endpoints never include it.
    signing_secret                  TEXT NOT NULL,
    signing_secret_prev             TEXT,
    signing_secret_prev_expires_at  TIMESTAMPTZ,
    enabled                         BOOLEAN NOT NULL DEFAULT true,
    auto_disabled_at                TIMESTAMPTZ,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_delivered_at               TIMESTAMPTZ,
    CHECK (cardinality(events) > 0)
);

-- Partial index on (user_id) WHERE enabled = true — the routing query
-- filters on these two columns first; the partial index keeps the
-- index small (disabled webhooks are excluded from the hot path).
CREATE INDEX IF NOT EXISTS idx_webhooks_user_enabled
    ON webhooks (user_id) WHERE enabled = true;
