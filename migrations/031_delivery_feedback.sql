-- 031_delivery_feedback.sql
--
-- Delivery feedback (decision 9 / Slice 4b). `send` returning accepted-by-relay
-- is not delivered; this closes the loop by recording the async delivery
-- outcome SES reports (via SNS) per outbound message and per recipient, plus a
-- per-tenant suppression list.
--
-- Three additions, all idempotent + non-destructive (ADD COLUMN/CREATE TABLE
-- IF NOT EXISTS; CHECKs under guards). No ALTER TYPE, no rewrites.

-- 1) Outbound delivery lifecycle on the messages row (a convenience rollup;
--    the authoritative per-recipient breakdown lives in message_recipients).
--    NULLable: inbound messages and not-yet-sent drafts carry no delivery_status.
--    `sent_as` records which From identity was actually used (decision 4
--    fallback) so "delivered" is never mis-attributed.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS delivery_status TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS delivery_detail TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS sent_as         TEXT;

DO $$ BEGIN
    ALTER TABLE messages ADD CONSTRAINT messages_delivery_status_check
        CHECK (delivery_status IS NULL OR delivery_status IN
            ('queued','sent','delivered','bounced','complained','deferred','failed'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE messages ADD CONSTRAINT messages_sent_as_check
        CHECK (sent_as IS NULL OR sent_as IN ('own_address','relay'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- The SES notifications consumer correlates events back to a message by the
-- SES-assigned id captured at send time. Index it for that lookup.
CREATE INDEX IF NOT EXISTS idx_messages_provider_message_id
    ON messages (provider_message_id)
    WHERE provider_message_id <> '';

-- 2) Per-recipient delivery breakdown. A single message to N recipients can
--    deliver to one and bounce/complain to another; SES emits feedback
--    per-recipient. One row per (message, address); the message rollup is the
--    worst status across these by the decision-9 precedence.
CREATE TABLE IF NOT EXISTS message_recipients (
    id          TEXT PRIMARY KEY,
    message_id  TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    address     TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'to' CHECK (kind IN ('to','cc','bcc')),
    status      TEXT NOT NULL DEFAULT 'sent'
                CHECK (status IN ('queued','sent','delivered','bounced','complained','deferred','failed')),
    detail      TEXT,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, address)
);

CREATE INDEX IF NOT EXISTS idx_message_recipients_message ON message_recipients (message_id);

-- 3) Per-tenant suppression list. Keyed per (user, address) — NEVER global: a
--    complaint from one tenant must not deny delivery for another. Send-time
--    enforcement reads this; auto-suppression on a hard bounce / corroborated
--    complaint inserts here and emits domain.suppression_added.
CREATE TABLE IF NOT EXISTS suppressions (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    address           TEXT NOT NULL,
    reason            TEXT NOT NULL DEFAULT '',
    source            TEXT NOT NULL DEFAULT 'bounce' CHECK (source IN ('bounce','complaint','manual')),
    source_message_id TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, address)
);

CREATE INDEX IF NOT EXISTS idx_suppressions_user ON suppressions (user_id);
