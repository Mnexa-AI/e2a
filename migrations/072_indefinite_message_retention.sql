-- 072_indefinite_message_retention.sql
-- Live inbound and outbound message data is retained indefinitely. New rows
-- use NULL as the canonical "never expires" value. Existing timestamps remain
-- untouched to avoid rewriting the production-sized messages table; runtime
-- retention no longer depends on expires_at. Trash purge is governed by
-- deleted_at.

ALTER TABLE messages
    ALTER COLUMN expires_at DROP NOT NULL,
    ALTER COLUMN expires_at DROP DEFAULT;

DROP INDEX IF EXISTS idx_messages_expires;
