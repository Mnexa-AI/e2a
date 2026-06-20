-- 037_screening.sql
--
-- Slice 2 of the agent email screening + HITL review feature
-- (docs/design/2026-06-20-agent-screening-hitl.md §4.4–4.5).
--
-- Two additive parts:
--   1. messages: the denormalized applied-verdict columns (review_reason,
--      scan_score, scan_action) + the inbound review-hold statuses, generalizing
--      the existing outbound pending_approval machine into a direction-aware queue.
--   2. screening_events: the durable, append-only audit log recording BOTH gate
--      violations and scan detections (distinguished by source). message_id is a
--      SOFT reference (no FK / no cascade) so the security trail outlives the 30-day
--      messages TTL.
--
-- Idempotent + non-destructive: ADD COLUMN IF NOT EXISTS, CREATE TABLE/INDEX IF NOT
-- EXISTS, and a DROP-then-ADD of the status CHECK (the only way to widen an existing
-- IN-list constraint). Existing rows keep their status; the widened CHECK is a
-- superset, so no row is invalidated.

-- 1a. Denormalized applied verdict on the message row (fast inbox/queue rendering).
ALTER TABLE messages ADD COLUMN IF NOT EXISTS review_reason TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS scan_score    REAL;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS scan_action   TEXT;

-- 1b. Widen the message status CHECK to admit the inbound review-hold lifecycle.
-- The pre-existing outbound statuses are retained unchanged; pending_review &
-- friends are the inbound analogue (approve = deliver to agent, reject = drop).
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_status_check;
DO $$ BEGIN
    ALTER TABLE messages ADD CONSTRAINT messages_status_check
        CHECK (status IN (
            -- outbound HITL (migration 003), unchanged
            'sent', 'pending_approval', 'rejected',
            'expired_approved', 'expired_rejected',
            -- inbound/screening review hold (this migration)
            'pending_review', 'review_approved', 'review_rejected',
            'review_expired_approved', 'review_expired_rejected'
        ));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Partial index keeps the inbound review-expiration sweep cheap, mirroring
-- idx_messages_pending_approval for the outbound side.
CREATE INDEX IF NOT EXISTS idx_messages_pending_review
    ON messages (approval_expires_at)
    WHERE status = 'pending_review';

-- 2. The screening_events audit log.
CREATE TABLE IF NOT EXISTS screening_events (
    id           TEXT PRIMARY KEY,                                  -- scr_<hex>
    message_id   TEXT NOT NULL,                                     -- SOFT ref (no FK/cascade)
    agent_id     TEXT NOT NULL,
    direction    TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    source       TEXT NOT NULL CHECK (source IN ('gate', 'scan')),
    reason       TEXT NOT NULL,                                     -- sender_gate|recipient_gate|inbound_scan|outbound_scan|outbound_send
    action       TEXT NOT NULL CHECK (action IN ('flag', 'review', 'block')),
    subject_addr TEXT,                                              -- sender (in) / recipient (out) that tripped a gate
    detector     TEXT,                                              -- scan only
    score        REAL,                                              -- scan only
    categories   JSONB,                                             -- scan only
    spans        JSONB,                                             -- scan only
    raw          JSONB,                                             -- scan only: provider raw, for forensics
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_screening_agent_time ON screening_events (agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_screening_message ON screening_events (message_id);
