-- 022_conversation_index.sql
-- e2a:no-transaction
--
-- Composite index supporting the Conversations API. Both endpoints
-- (list + detail) filter by agent_id and group/sort by conversation_id
-- + created_at; without this they'd seq-scan an agent's entire message
-- partition.
--
-- The leading agent_id column makes this also serve queries that
-- filter on agent_id alone (Postgres can use a prefix), so it doesn't
-- compete with the existing idx_messages_agent_created on (agent_id,
-- created_at DESC) — the two index different access patterns and the
-- planner picks whichever fits the predicate set.
--
-- created_at DESC so the per-conversation message fetch returns rows
-- in newest-first order without a sort.
--
-- CREATE INDEX CONCURRENTLY required by the e2a:no-transaction
-- directive (see internal/identity/migrate.go) so prod deploys don't
-- block writes on the multi-million-row messages table. Single
-- statement per the runner's contract.
--
-- Idempotent via IF NOT EXISTS.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_messages_agent_conv_created
    ON messages (agent_id, conversation_id, created_at DESC)
    WHERE conversation_id <> '';
