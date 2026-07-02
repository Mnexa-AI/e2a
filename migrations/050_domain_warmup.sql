-- 050_domain_warmup.sql
--
-- Per-domain sending warmup (email-warmup-support).
--
-- Warmup gradually raises a newly sending-verified domain's daily outbound
-- allowance over a ramp window so a cold domain builds ISP reputation instead
-- of blasting full volume on day one (the same automatic ramp SES/Postmark/
-- SendGrid apply to new senders). It is scoped PER DOMAIN because mailbox
-- providers track reputation per sending domain, not per agent or account.
--
-- warmup_status lifecycle:
--   inactive — default; no warmup in effect (self-host / SES not configured, or
--              a domain that never reached sending-verified). The enforcer
--              no-ops, so every send is allowed. This keeps the migration
--              behavior-neutral until a domain first becomes sending-verified.
--   active   — the ramp is running: outbound for this domain is capped at the
--              schedule's per-day allowance (a function of warmup_started_at).
--              Set exactly once, when sending_status first flips to 'verified'
--              (see identity.Store.SetSendingStatus), and never re-armed on a
--              later re-verify — reputation, once built, is not rebuilt.
--   paused   — an operator/user has suspended the ramp; the enforcer no-ops
--              (sends flow at full volume) until resumed to 'active'.
--
-- "Completed" is NOT a stored state: once now() - warmup_started_at exceeds the
-- configured ramp window the schedule returns the full target as the daily cap,
-- so an 'active' domain past its ramp is effectively unthrottled without a
-- status write. Callers that want to render "completed" derive it from the
-- schedule (warmup.Schedule.DailyCap's done return).
--
-- warmup_started_at is the ramp anchor (NULL until the first verified flip).
--
-- Idempotent + non-destructive: ADD COLUMN IF NOT EXISTS only; the CHECK is
-- added defensively under a guard so a re-run is a no-op. Safe on prod-sized
-- tables (metadata-only, no rewrite).

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS warmup_status TEXT NOT NULL DEFAULT 'inactive';

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS warmup_started_at TIMESTAMPTZ;

DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_warmup_status_check
        CHECK (warmup_status IN ('inactive', 'active', 'paused'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
