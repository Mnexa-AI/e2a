-- 038_scan_config.sql
--
-- Slice 3 of the agent email screening feature
-- (docs/design/2026-06-20-agent-screening-hitl.md §4.1).
--
-- Adds the per-agent content-screening config surface: the producer-policy actions
-- (inbound_policy_action / outbound_policy_action), the outbound recipient gate
-- (outbound_policy + outbound_allowlist — the egress firewall / trust ramp), and the
-- inbound/outbound content scans (on/off + review/block threshold ladder).
--
-- This migration is ADDITIVE only. The retirement of hitl_enabled/hitl_mode (whose
-- decision role the new outbound_policy/outbound_scan fields take over) lands with
-- the outbound wiring slice, once the replacement logic actually drives the hold —
-- you can't drop the column that currently makes the decision before its replacement
-- is wired.
--
-- Defaults preserve today's behavior: gate actions default to 'flag' (the existing
-- inbound_policy gate is flag-and-deliver; defaulting to review would silently start
-- quarantining mail for every existing agent), scans default 'off'.

-- Producer-policy actions (flag | review | block).
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS inbound_policy_action  TEXT NOT NULL DEFAULT 'flag';
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_policy_action TEXT NOT NULL DEFAULT 'flag';

-- Outbound recipient gate (egress firewall). verified_only is inbound-only (DMARC),
-- so the outbound gate is open | allowlist | domain.
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_policy    TEXT NOT NULL DEFAULT 'open';
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_allowlist TEXT[];

-- Content scans: on/off + the per-direction review/block threshold ladder.
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS inbound_scan                   TEXT NOT NULL DEFAULT 'off';
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS inbound_scan_review_threshold  REAL NOT NULL DEFAULT 0.5;
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS inbound_scan_block_threshold   REAL NOT NULL DEFAULT 0.9;
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_scan                  TEXT NOT NULL DEFAULT 'off';
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_scan_review_threshold REAL NOT NULL DEFAULT 0.5;
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_scan_block_threshold  REAL NOT NULL DEFAULT 0.9;

-- CHECK constraints (idempotent via duplicate_object guard, matching migration 036).
DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_inbound_policy_action_check
        CHECK (inbound_policy_action IN ('flag', 'review', 'block'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_outbound_policy_action_check
        CHECK (outbound_policy_action IN ('flag', 'review', 'block'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_outbound_policy_check
        CHECK (outbound_policy IN ('open', 'allowlist', 'domain'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_inbound_scan_check
        CHECK (inbound_scan IN ('off', 'on'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_outbound_scan_check
        CHECK (outbound_scan IN ('off', 'on'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_scan_thresholds_check
        CHECK (
            inbound_scan_review_threshold  >= 0 AND inbound_scan_review_threshold  <= 1 AND
            inbound_scan_block_threshold   >= 0 AND inbound_scan_block_threshold   <= 1 AND
            outbound_scan_review_threshold >= 0 AND outbound_scan_review_threshold <= 1 AND
            outbound_scan_block_threshold  >= 0 AND outbound_scan_block_threshold  <= 1
        );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
