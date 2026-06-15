-- 030_domain_sending_identity.sql
--
-- Custom-domain sender identity (decision 4 / Slice 4). A domain has two
-- INDEPENDENT statuses: `verified` (inbound ownership, DNS TXT — already
-- here) and the new `sending_status` (the async SES sending identity that
-- lets outbound mail use the agent's OWN address as the From header).
--
-- sending_status lifecycle:
--   none     — no sending identity registered (default; self-host / SES
--              not configured stays here forever → relay From, fail-closed)
--   pending  — SES identity registered via BYODKIM, awaiting verification
--   verified — SES confirmed; own-address From is now used for this domain
--   failed   — verification failed or the pending TTL elapsed; relay From
--
-- The own-address From is used ONLY when sending_status = 'verified'
-- (decision 4 fail-closed gate); every other value falls back to the relay
-- From, so this migration is behavior-neutral until the SES provider flips a
-- domain to 'verified'.
--
-- Companion columns:
--   sending_error           — actionable failure reason when failed
--   sending_dns_records     — JSONB the dashboard shows (BYODKIM reuses the
--                             per-domain DKIM record already published, so
--                             this is usually empty; a future custom MAIL
--                             FROM subdomain would add records here)
--   sending_last_checked_at — when the reconciler last polled SES
--
-- Idempotent + non-destructive: ADD COLUMN IF NOT EXISTS only; the CHECK is
-- added defensively under a guard so a re-run is a no-op. Safe on prod-sized
-- tables (metadata-only, no rewrite).

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS sending_status TEXT NOT NULL DEFAULT 'none';

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS sending_error TEXT;

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS sending_dns_records JSONB;

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS sending_last_checked_at TIMESTAMPTZ;

DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_sending_status_check
        CHECK (sending_status IN ('none', 'pending', 'verified', 'failed'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- The reconciler scans for domains stuck in 'pending'. Partial index keeps
-- that sweep cheap as the table grows.
CREATE INDEX IF NOT EXISTS idx_domains_sending_pending
    ON domains (sending_last_checked_at)
    WHERE sending_status = 'pending';
