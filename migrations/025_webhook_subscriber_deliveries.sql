-- 025_webhook_subscriber_deliveries.sql
--
-- Per-attempt delivery state for the new webhooks resource path. Lives
-- alongside (NOT replacing) the legacy webhook_deliveries table which
-- continues to serve the agent_identities.webhook_url path unchanged.
-- Two tables coexist intentionally:
--
--   webhook_deliveries:          one row per message, keyed by message_id.
--                                Suits the legacy "one URL per agent"
--                                model where a message has at most one
--                                delivery attempt set.
--   webhook_subscriber_deliveries: one row per (event, subscriber)
--                                pair. Suits fan-out — a single inbound
--                                produces up to N rows, one per matched
--                                webhook subscriber.
--
-- message_id is nullable with ON DELETE SET NULL so the 30-day messages
-- janitor (DeleteExpiredMessages) can drop messages without orphaning
-- this table. event_payload is self-contained, so the delivery row
-- remains usable for retry + history reads even after the source
-- message is gone — at the cost of GET /deliveries showing a delivery
-- whose underlying message has been pruned (acceptable, addressed by
-- the slice-2 handler returning null for the message reference when
-- message_id is null).
--
-- Idempotent via IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS webhook_subscriber_deliveries (
    id                  TEXT PRIMARY KEY,
    webhook_id          TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    -- event_type carries the catalog name (email.received, email.sent,
    -- email.pending_approval, email.approved, email.rejected). Stored
    -- as a plain TEXT so adding a new event type in a future slice is
    -- a non-breaking change.
    event_type          TEXT NOT NULL,
    -- event_payload is the full JSON envelope ({event, id, created_at,
    -- data}) ready to be POSTed verbatim. Persisting the payload (not
    -- just a reference) lets retries fire without re-deriving the
    -- payload from upstream state — which may have changed since.
    event_payload       JSONB NOT NULL,
    -- message_id is the originating message for events that have one
    -- (email.received, email.sent, email.approved). Null for events
    -- without a direct message backing (email.pending_approval's
    -- pending row hasn't been promoted yet; email.rejected may not
    -- have a corresponding messages row depending on flow). The FK
    -- ON DELETE SET NULL keeps delivery history usable past the
    -- 30-day message TTL.
    message_id          TEXT REFERENCES messages(id) ON DELETE SET NULL,
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts            INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts        INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    last_error          TEXT NOT NULL DEFAULT '',
    last_status_code    INTEGER,
    last_attempt_at     TIMESTAMPTZ,
    next_retry_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 30-day retention matches the legacy webhook_deliveries table. The
    -- existing DeleteExpiredDeliveries janitor will be extended in
    -- slice 4 to scan this table too.
    expires_at          TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 days'
);

-- Hot path for the retry worker: find rows the worker should attempt
-- next. Partial index restricts the index size to pending rows only
-- (delivered + failed are read by the history endpoint via a separate
-- index, below).
CREATE INDEX IF NOT EXISTS idx_wsd_pending
    ON webhook_subscriber_deliveries (next_retry_at)
    WHERE status = 'pending';

-- Hot path for GET /webhooks/{id}/deliveries: most-recent-first
-- listing per webhook. Covers both delivered + failed without needing
-- to scan by status.
CREATE INDEX IF NOT EXISTS idx_wsd_webhook_created
    ON webhook_subscriber_deliveries (webhook_id, created_at DESC);
