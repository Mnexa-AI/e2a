-- 012_hitl_reviewer.sql
--
-- Attribute HITL approval / rejection actions to the human reviewer.
-- The redesigned pending detail panel surfaces "approved by Jamie 14m ago";
-- without this column, only the timestamp survives review.
--
-- ON DELETE SET NULL preserves the message audit trail even if the
-- reviewer's user row is later deleted (right-of-deletion request) —
-- losing the message provenance because a user wound down their account
-- would be the wrong tradeoff for a security-adjacent feature.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS leaves existing schemas
-- untouched. Non-destructive on prod-sized tables — adding a nullable
-- column is a metadata-only operation in Postgres.

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS reviewed_by_user_id TEXT
        REFERENCES users(id) ON DELETE SET NULL;
