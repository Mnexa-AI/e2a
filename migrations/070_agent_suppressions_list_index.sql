-- Match the beta agent-suppression keyset list order, including its stable
-- address tie-breaker. Kept as a forward migration so installations that
-- already applied 068 receive the index on upgrade.
CREATE INDEX IF NOT EXISTS agent_suppressions_list_idx
    ON agent_suppressions (user_id, agent_id, created_at DESC, address DESC);
