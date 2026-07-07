-- 055_messages_envelope_from.sql
--
-- Async outbound pipeline (docs/design/async-message-pipeline.md), slice C. The
-- accept-tx composes the message once and persists the exact wire bytes
-- (messages.raw_message) plus the SMTP envelope MAIL FROM so the River send
-- worker (internal/outboundsend) can submit the message WITHOUT re-composing
-- (which would drift the Message-ID/DKIM between attempts and the Sent-folder
-- copy). The synchronous path derives envelope_from at send time and never
-- persisted it; the async worker loads it from the row, so add the column.
--
-- Additive + idempotent + NULLable — every existing row (sync sends, inbound,
-- drafts) simply carries NULL; only async-accepted rows populate it. Safe on
-- prod-sized `messages` (ADD COLUMN with no default is a metadata-only op).

ALTER TABLE messages ADD COLUMN IF NOT EXISTS envelope_from TEXT;
