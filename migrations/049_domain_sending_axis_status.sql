-- 049_domain_sending_axis_status.sql
--
-- Per-axis SES sending status (companion to 030_domain_sending_identity.sql).
--
-- SES verifies a sending identity along two INDEPENDENT axes: DKIM
-- (DkimAttributes.Status) and the custom MAIL FROM subdomain
-- (MailFromAttributes.MailFromDomainStatus). Migration 030's sending_status is
-- the all-or-nothing rollup over both (mapSESStatus), so a domain with good
-- DKIM but a broken MAIL FROM (or vice versa) reads as `failed` on EVERY
-- sending record and the user can't tell which one to fix.
--
-- These two columns persist the per-axis breakdown the reconcile worker
-- extracts from SES so the API's per-record DNSRecord.status can reflect each
-- record's OWN axis: the dkim record follows sending_dkim_status; the
-- mail_from_mx + mail_from_spf records follow sending_mail_from_status. The
-- rollup sending_status is unchanged (still the all-or-nothing summary).
--
-- Lifecycle: NULL until the reconciler first records an axis (or for terminal
-- failures with no per-axis signal — no key material, identity gone, timed
-- out). The read path falls back to the sending_status rollup when an axis is
-- NULL, so pre-migration / pre-provision rows behave exactly as before. This is
-- behavior-neutral for the send path (own-address From still gates only on
-- sending_status = 'verified').
--
-- Idempotent + non-destructive: ADD COLUMN IF NOT EXISTS only; the CHECKs are
-- added defensively under a guard so a re-run is a no-op. Safe on prod-sized
-- tables (metadata-only, no rewrite).

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS sending_dkim_status TEXT;

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS sending_mail_from_status TEXT;

DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_sending_dkim_status_check
        CHECK (sending_dkim_status IS NULL OR sending_dkim_status IN ('none', 'pending', 'verified', 'failed'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_sending_mail_from_status_check
        CHECK (sending_mail_from_status IS NULL OR sending_mail_from_status IN ('none', 'pending', 'verified', 'failed'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
