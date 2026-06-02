-- 026_webhook_events.sql
--
-- Outbox + customer-facing event log for the Stripe-tier webhooks
-- upgrade. Triggers write to this table inside their business-state
-- transaction (per design §4.2); a separate publisher worker (slice 2)
-- consumes pending rows and fans out into webhook_subscriber_deliveries.
--
-- See docs/design/2026-06-01-stripe-tier-webhooks.md §4.3 for the full
-- column rationale.
--
-- This slice (slice 1) creates the table and wires the trigger-side
-- writer. The publisher worker that drains this table is slice 2; until
-- it lands, rows accumulate with status='pending'. That is intentional
-- and safe — the existing legacy go publisher.Publish(...) path
-- continues to deliver events in parallel, so no customer-visible
-- delivery is lost during the rollout window.
--
-- Idempotent via IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS webhook_events (
    -- Stable per-event id (evt_<32hex>). Reused across all deliveries
    -- and replays of this event for consumer dedup. Determinism: id =
    -- "evt_" + first 32 hex chars of sha256(message_id || "|" || event_type)
    -- (per design §5.1's event-ID table). The message_id input is
    -- globally unique so collisions across 30-day retention × projected
    -- event volume are ~3e-23. Determinism is what makes the outbox
    -- write idempotent across MTA SMTP retries.
    --
    -- PRIMARY KEY (id) — single-column. We accept the cost of a future
    -- partitioning migration when row count requires it; the alternative
    -- (composite PK with created_at) silently breaks ON CONFLICT (id)
    -- idempotency under partitioning. See design §4.3 for the analysis.
    id                   TEXT PRIMARY KEY,

    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- event_type carries the catalog name (email.received in v1; the
    -- catalog is in internal/webhookpub/event.go). Stored as TEXT so
    -- adding a new event type is a non-breaking change. v1 only writes
    -- email.received; the rest stay on the legacy go publisher.Publish
    -- path until slice 4.
    type                 TEXT NOT NULL,

    -- Audience: v1 only writes 'webhook'. Column reserved without a
    -- CHECK constraint so a future v2 expansion (e.g. 'system') is
    -- non-breaking and doesn't require dropping a CHECK on a populated
    -- table. App-layer enforcement.
    aud                  TEXT NOT NULL DEFAULT 'webhook',

    -- Envelope is the full {event, id, created_at, schema_version, data}
    -- JSON ready for delivery. Persisted at trigger time so the payload
    -- is the snapshot at the moment of the event — replays use the same
    -- bytes the original delivery would have used.
    envelope             JSONB NOT NULL,

    -- Defensive SMALLINT (vs INTEGER) saves 2 B/row × millions of rows.
    -- ALTER COLUMN TYPE on a populated webhook_events would be expensive
    -- per CLAUDE.md migration rules, so right-size at creation.
    schema_version       SMALLINT NOT NULL DEFAULT 1,

    -- Indexed dimensions for /events filtering. Sourced from
    -- envelope.data at write time. Nullable because not every event
    -- type carries them (e.g. domain.verified has no agent_id). No FK
    -- on agent_id / conversation_id — those can be deleted by the user
    -- and we want the historical event log to survive deletion.
    agent_id             TEXT,
    conversation_id      TEXT,
    message_id           TEXT REFERENCES messages(id) ON DELETE SET NULL,

    -- Outbox state machine for the publisher worker (slice 2).
    --   pending   – created by trigger; awaits fan-out
    --   processed – worker fanned out and wrote matched_webhook_ids
    --   no_match  – worker ran filter logic; no subscriber matched.
    --               Distinct from processed-with-0-matches so we can
    --               audit which events would have triggered nothing.
    --
    -- v1 only writes 'pending'. Slice 2 introduces the transitions.
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'processed', 'no_match')),

    -- Worker bookkeeping (used by slice 2; columns here so the slice-2
    -- migration is purely additive code, not schema). last_error is
    -- length-capped to prevent a pathological 10KB stack trace × 30M
    -- rows blowing up disk.
    attempts             INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error           TEXT NOT NULL DEFAULT ''
                         CHECK (length(last_error) <= 4096),
    last_status_code     INTEGER,
    next_poll_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at         TIMESTAMPTZ,

    -- Snapshot of which webhooks the publisher matched at fan-out time.
    -- Used by /webhooks/{id}/redeliver-since (slice 7) so bulk replay
    -- preserves the original match decision instead of re-running the
    -- filter against the current subscriber set. TEXT[] bounded by the
    -- per-user webhook cap (50).
    matched_webhook_ids  TEXT[] NOT NULL DEFAULT '{}'
                         CHECK (cardinality(matched_webhook_ids) <= 50),

    -- created_at is the Postgres server's transaction start time (one
    -- clock per primary writer). No application-side clock skew.
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 days'
);

-- Hot path: outbox worker lease (slice 2) — partial index on pending
-- rows. Builds on the rough side every lease bumps next_poll_at,
-- which is in this index — that's the autovacuum cost noted in §6.1.
-- Acceptable at 100K events/day; tune autovacuum_vacuum_scale_factor
-- when crossing 1M/day.
CREATE INDEX IF NOT EXISTS idx_webhook_events_pending
    ON webhook_events (next_poll_at)
    WHERE status = 'pending';

-- Hot path: GET /events with cursor pagination by (created_at, id).
-- Tiebreak on id is satisfied from the heap (negligible at LIMIT 100).
CREATE INDEX IF NOT EXISTS idx_webhook_events_user_created
    ON webhook_events (user_id, created_at DESC);

-- Filter indexes for /events?type=... and /events?agent_id=...
CREATE INDEX IF NOT EXISTS idx_webhook_events_user_type_created
    ON webhook_events (user_id, type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_events_user_agent_created
    ON webhook_events (user_id, agent_id, created_at DESC)
    WHERE agent_id IS NOT NULL;

-- Hot path: janitor's DELETE WHERE expires_at <= now(). Without this,
-- the hourly cleanup full-scans a 30M-row table. Mirrors the
-- idx_messages_expires precedent.
CREATE INDEX IF NOT EXISTS idx_webhook_events_expires
    ON webhook_events (expires_at);
