-- 027_retry_envelope_extension.sql
--
-- Slice 5 of the Stripe-tier webhooks design: extend the retry envelope
-- from ~4h (5 attempts) to ~72h (8 attempts). Customer-visible:
-- subscribers down for an extended outage (deploys, weekend incidents)
-- now have a 72-hour window to come back online before delivery
-- transitions to status='failed'.
--
-- ALTER COLUMN ... SET DEFAULT is metadata-only on PostgreSQL 11+ — it
-- only changes the system catalog, no row rewrite. Safe on
-- prod-sized webhook_subscriber_deliveries. Existing rows keep their
-- per-row max_attempts value (typically 5), so they terminate at the
-- old cap. New rows inherit the new defaults.
--
-- expires_at TTL also extends: 30 → 90 days, to give the 72h retry
-- envelope a healthy margin before janitor cleanup.
--
-- Idempotent — ALTER ... SET DEFAULT runs unchanged on a table that
-- already has the new default.

ALTER TABLE webhook_subscriber_deliveries
    ALTER COLUMN max_attempts SET DEFAULT 8;

ALTER TABLE webhook_subscriber_deliveries
    ALTER COLUMN expires_at SET DEFAULT now() + interval '90 days';
