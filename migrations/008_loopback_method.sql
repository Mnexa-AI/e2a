-- 008_loopback_method.sql
--
-- Allow the value 'loopback' in messages.method.
--
-- Self-send (an agent emailing its own address) short-circuits the
-- SMTP path: instead of round-tripping through SES + MX + the local
-- SMTP receiver, the /api/v1/send handler writes the outbound and
-- inbound rows synchronously. The persisted method on the outbound
-- row must distinguish this case so operators can filter audit
-- queries — 'smtp' would be misleading because no SMTP traffic
-- actually occurred.
--
-- Idempotent: drops the prior check by canonical name (created by
-- the original messages table migration) and re-adds it with the
-- expanded value set. The CHECK is reasserted by name so a replay
-- against an already-migrated DB is a no-op.

ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_method_check;
ALTER TABLE messages ADD CONSTRAINT messages_method_check
    CHECK (method = ANY (ARRAY['smtp'::text, 'webhook'::text, 'loopback'::text]));
