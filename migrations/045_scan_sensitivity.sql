-- 045_scan_sensitivity.sql
--
-- Agent protection config (design 2026-06-22-agent-protection-config.md, O4).
--
-- The public protection API models the content scan as a single semantic
-- sensitivity level (off|low|medium|high) instead of the raw review/block float
-- thresholds. This adds the per-direction sensitivity column as the API
-- source-of-truth for read-back; the handler still derives and writes the
-- existing inbound_scan/*_threshold columns so the piguard engine is untouched
-- (it keeps reading the float thresholds). Retaining the float columns also
-- leaves room for a future raw-threshold power-user override.
--
-- ADDITIVE only and idempotent. Default 'off' preserves today's behavior
-- (scans default off); no existing agent's posture changes.

ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS inbound_scan_sensitivity  TEXT NOT NULL DEFAULT 'off';
ALTER TABLE agent_identities ADD COLUMN IF NOT EXISTS outbound_scan_sensitivity TEXT NOT NULL DEFAULT 'off';

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_inbound_scan_sensitivity_check
        CHECK (inbound_scan_sensitivity IN ('off', 'low', 'medium', 'high'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_outbound_scan_sensitivity_check
        CHECK (outbound_scan_sensitivity IN ('off', 'low', 'medium', 'high'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Backfill: a pre-045 agent that enabled its scan via the old API has scan='on'
-- but the new sensitivity column just defaulted to 'off'. Left as-is, GET
-- /protection would misreport (scan on, sensitivity off), and a read-modify-write
-- PUT would write scan='off' from that 'off' level — silently disabling a live
-- scan. Derive the level from the existing review threshold (nearest band:
-- high≈0.30, medium≈0.50, low≈0.70) so the default 0.5/0.9 pair reads back as
-- 'medium'. Idempotent + safe: only touches scan='on' rows still showing 'off',
-- a state UpdateAgentProtection never writes (off⇒scan off, level⇒scan on).
UPDATE agent_identities SET inbound_scan_sensitivity = CASE
        WHEN inbound_scan_review_threshold <= 0.40 THEN 'high'
        WHEN inbound_scan_review_threshold <= 0.60 THEN 'medium'
        ELSE 'low' END
  WHERE inbound_scan = 'on' AND inbound_scan_sensitivity = 'off';

UPDATE agent_identities SET outbound_scan_sensitivity = CASE
        WHEN outbound_scan_review_threshold <= 0.40 THEN 'high'
        WHEN outbound_scan_review_threshold <= 0.60 THEN 'medium'
        ELSE 'low' END
  WHERE outbound_scan = 'on' AND outbound_scan_sensitivity = 'off';
