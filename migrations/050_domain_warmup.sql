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
--   inactive — default; no warmup in effect (self-host / SES not configured,
--              warmup disabled in config, or a domain that never reached
--              sending-verified). The enforcer no-ops, so every send is
--              allowed. This keeps the migration behavior-neutral until a
--              domain first becomes sending-verified with warmup enabled.
--   active   — the ramp is running: outbound for this domain is capped at the
--              schedule's per-day allowance (a function of warmup_started_at).
--              Set exactly once, when sending_status first flips to 'verified'
--              while warmup is enabled (see identity.Store.SetSendingStatus),
--              and never re-armed later — reputation, once built, is not
--              rebuilt.
--   paused   — an operator has suspended the ramp (manual SQL for now; no API
--              writes this yet); the enforcer no-ops until resumed to 'active'.
--
-- "Completed" is NOT a stored state: once the ramp window elapses the enforcer
-- short-circuits on the schedule's done flag, so an 'active' domain past its
-- ramp is unthrottled (and uncounted) without a status write. Callers that
-- want to render "completed" derive it from warmup.Schedule.DailyCap's done
-- return.
--
-- warmup_started_at is the ramp anchor. It is stamped on the FIRST transition
-- to sending-verified regardless of whether warmup is enabled (see the
-- backfill note below for why), and never overwritten.
--
-- domain_send_counters is the warmup numerator: one row per (domain, UTC day),
-- incremented atomically at actual wire-send time by
-- usage.Store.ReserveDomainSend with the day's cap as a guard, so concurrent
-- sends serialize on the row and can never jointly overshoot the ramp. It is
-- deliberately independent of the messages table: message rows are cascade-
-- deleted with their agent, and held-for-review drafts are created days before
-- they hit the wire — neither may perturb the count of what actually left for
-- the ISPs. Row volume is tiny (only actively-warming domains write here, one
-- row per day, and ramp-completed domains stop writing), so no retention job
-- is needed.
--
-- Idempotent + non-destructive: ADD COLUMN / CREATE TABLE IF NOT EXISTS only;
-- the CHECK is added defensively under a guard so a re-run is a no-op; the
-- backfill UPDATE matches no rows on re-run. Safe on prod-sized tables
-- (metadata-only on domains, no rewrite).

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS warmup_status TEXT NOT NULL DEFAULT 'inactive';

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS warmup_started_at TIMESTAMPTZ;

DO $$ BEGIN
    ALTER TABLE domains ADD CONSTRAINT domains_warmup_status_check
        CHECK (warmup_status IN ('inactive', 'active', 'paused'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Backfill: domains that were already sending-verified before this feature
-- shipped have built their reputation at full volume. Stamp their anchor now
-- (status stays 'inactive') so the arm-once CASE in SetSendingStatus — which
-- keys "first verification" on warmup_started_at IS NULL — can never arm a
-- day-0 ramp on an established sender via a later re-verify or status flap.
UPDATE domains
   SET warmup_started_at = now()
 WHERE sending_status = 'verified'
   AND warmup_started_at IS NULL;

CREATE TABLE IF NOT EXISTS domain_send_counters (
    domain TEXT NOT NULL,
    day    DATE NOT NULL,
    count  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (domain, day)
);
