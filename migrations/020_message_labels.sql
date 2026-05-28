-- 020_message_labels.sql
--
-- Add per-message string labels. labels[] is the canonical store —
-- bare strings, not a join table, no separate `labels` resource.
-- Matches the agent-shaped model used by AgentMail (every label is just
-- a tag an agent emits); contrasts with Gmail's label-as-resource model
-- which needs a CRUD surface for color/visibility metadata we don't
-- want.
--
-- Default '{}'::text[] is a constant: Postgres 11+ records the new
-- column metadata-only and rewrites no rows. Safe on a multi-million-
-- row messages table.
--
-- GIN index is the standard array containment index — supports the
-- AND-match filter shape `WHERE labels @> ARRAY['urgent','follow-up']`
-- exposed by GET /messages?labels=urgent&labels=follow-up.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS, CREATE INDEX IF NOT EXISTS.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS labels TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_messages_labels_gin ON messages USING GIN (labels);
