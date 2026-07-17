-- Add per-agent HITL approval notification suppression to agent identities.
ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS suppress_notifications boolean NOT NULL DEFAULT false;
