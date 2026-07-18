-- agent_identities stores the explicitly registered domain identity that
-- authorizes an address. For inherited subdomain agents this differs from the
-- exact domain in id/email, so name the relationship for what it represents.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'agent_identities'
          AND column_name = 'domain'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'agent_identities'
          AND column_name = 'registered_domain'
    ) THEN
        ALTER TABLE agent_identities RENAME COLUMN domain TO registered_domain;
    END IF;
END $$;

ALTER INDEX IF EXISTS idx_agents_domain
    RENAME TO idx_agents_registered_domain;
