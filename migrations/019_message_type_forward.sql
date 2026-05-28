-- 019_message_type_forward.sql
--
-- Allow the value 'forward' in messages.message_type.
--
-- The /api/v1/agents/{email}/messages/{id}/forward endpoint records its
-- outbound row with message_type='forward' so audit queries can
-- distinguish a forward (new thread, no In-Reply-To) from a reply
-- (same thread). Mirrors the pattern used by 008_loopback_method.sql
-- to expand the messages.method CHECK constraint.
--
-- Idempotent: drops the prior check by canonical name and re-adds it
-- with the expanded value set. The CHECK is reasserted by name so a
-- replay against an already-migrated DB is a no-op.

ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_message_type_check;
ALTER TABLE messages ADD CONSTRAINT messages_message_type_check
    CHECK (message_type IN ('send', 'reply', 'test', 'forward'));
