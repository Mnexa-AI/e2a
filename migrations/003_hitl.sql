-- Human-in-the-loop outbound approval.
-- See docs/design-hitl.md for full context.
--
-- Adds per-agent HITL configuration and a pending-approval status on messages
-- plus the columns needed to hold a fully composed outbound message while it
-- waits on human review.

-- Per-agent HITL configuration
ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS hitl_enabled BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS hitl_ttl_seconds INTEGER NOT NULL DEFAULT 604800
        CHECK (hitl_ttl_seconds > 0 AND hitl_ttl_seconds <= 604800);

ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS hitl_expiration_action TEXT NOT NULL DEFAULT 'reject'
        CHECK (hitl_expiration_action IN ('approve', 'reject'));

-- Message approval status + body retention
-- Existing rows receive status='sent' via the DEFAULT; the CHECK permits that
-- value so the add is safe for a non-empty table.
ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'sent'
        CHECK (status IN ('sent', 'pending_approval', 'rejected',
                          'expired_approved', 'expired_rejected'));

ALTER TABLE messages ADD COLUMN IF NOT EXISTS approval_expires_at TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reviewed_at         TIMESTAMPTZ;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS rejection_reason    TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited              BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS body_text           TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS body_html           TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS attachments_json    JSONB;

-- Partial index keeps the expiration-sweep query cheap regardless of total volume.
CREATE INDEX IF NOT EXISTS idx_messages_pending_approval
    ON messages (approval_expires_at)
    WHERE status = 'pending_approval';
