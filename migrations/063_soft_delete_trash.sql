-- 063_soft_delete_trash.sql
--
-- Trash / soft delete for agent inboxes and messages (docs/design/
-- trash-soft-delete.md). deleted_at IS NULL = live; non-NULL = in trash since
-- that instant. Trashed rows are hidden from every agent-facing read path,
-- restorable from the dashboard trash, and purged by the janitor after the
-- retention window (identity.TrashRetention, default 30 days).
--
-- ADD COLUMN (nullable, no default rewrite) is safe on the prod-sized
-- messages table. The indexes are partial — trash is a tiny fraction of each
-- table — so they cost near-zero on the hot (live) write path.

ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE messages         ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

-- Trash listing: newest-first per agent (mirrors idx_messages_agent_created).
CREATE INDEX IF NOT EXISTS idx_messages_trash_agent_created
    ON messages(agent_id, created_at DESC)
    WHERE deleted_at IS NOT NULL;

-- Janitor purge sweeps: rows past deleted_at + retention.
CREATE INDEX IF NOT EXISTS idx_messages_trash_deleted_at
    ON messages(deleted_at)
    WHERE deleted_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agents_trash_deleted_at
    ON agent_identities(deleted_at)
    WHERE deleted_at IS NOT NULL;
