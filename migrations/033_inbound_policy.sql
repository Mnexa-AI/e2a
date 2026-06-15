-- 033_inbound_policy.sql
--
-- Inbound trust policy (decision 10 / Slice 7a — ingestion gate). A per-agent
-- inbound_policy decides, on arrival, whether a message is trusted or FLAGGED.
-- Flagged messages are still delivered (never dropped) and emit email.flagged.
--
--   open (default) — accept all
--   allowlist      — accept only senders on inbound_allowlist (exact address)
--   domain         — accept only senders whose domain is on inbound_allowlist
--   verified_only  — accept only DMARC-aligned mail (anti-spoofing)
--
-- The hitl ACTION gate is a separate axis (the existing hitl_enabled, formalized
-- in Slice 7b) — the two compose. Idempotent + non-destructive.
ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS inbound_policy TEXT NOT NULL DEFAULT 'open';
ALTER TABLE agent_identities
    ADD COLUMN IF NOT EXISTS inbound_allowlist TEXT[];

DO $$ BEGIN
    ALTER TABLE agent_identities ADD CONSTRAINT agent_identities_inbound_policy_check
        CHECK (inbound_policy IN ('open', 'allowlist', 'domain', 'verified_only'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Per-message ingestion result: flagged by the policy at arrival (with a
-- reason), surfaced on the message + via email.flagged. Inbound-only in
-- practice; default false.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS flagged BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS flag_reason TEXT;
