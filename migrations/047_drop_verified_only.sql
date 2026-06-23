-- 047_drop_verified_only.sql
--
-- Drop the `verified_only` inbound-policy posture (the DMARC-alignment gate). It
-- was unsettable via the API after the protection sub-resource landed (the gate
-- only accepts open|allowlist|domain), and is not supported for GA. It may return
-- later as a composable, additive policy.
--
-- Idempotent + non-destructive: migrate any straggler off verified_only first
-- (there should be none — it's been unsettable), then tighten the CHECK.

-- 1. Migrate any agent still on verified_only to the default open gate.
UPDATE agent_identities SET inbound_policy = 'open' WHERE inbound_policy = 'verified_only';

-- 2. Replace the CHECK constraint with one that no longer permits verified_only.
DO $$ BEGIN
    ALTER TABLE agent_identities DROP CONSTRAINT IF EXISTS agent_identities_inbound_policy_check;
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_inbound_policy_check
        CHECK (inbound_policy IN ('open', 'allowlist', 'domain'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
