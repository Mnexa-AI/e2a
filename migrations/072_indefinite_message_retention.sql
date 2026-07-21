-- 072_indefinite_message_retention.sql
-- Live inbound and outbound message data is retained indefinitely. NULL is the
-- canonical "never expires" value; trash purge remains governed by deleted_at.

ALTER TABLE messages
    ALTER COLUMN expires_at DROP NOT NULL,
    ALTER COLUMN expires_at DROP DEFAULT;

UPDATE messages
   SET expires_at = NULL
 WHERE expires_at IS NOT NULL;

DROP INDEX IF EXISTS idx_messages_expires;
