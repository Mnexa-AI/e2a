-- 037_account_class.sql
--
-- Account classification (prober-selftest design, slice 1). A per-account
-- `account_class` is the single source of truth for "what kind of account this
-- is", ORTHOGONAL to the paid plan/tier (which decides quota size). The metering
-- gate (usage.PolicyFor) resolves billability from this class at the one point
-- usage is recorded, so non-standard accounts never write usage_events /
-- usage_summaries and never accrue quota.
--
--   standard (default) — a real customer; metered, billed, in analytics
--   internal           — internal dogfooding; not metered/billed/analytics
--   system             — synthetic-monitoring probe traffic; not metered/billed/analytics
--   demo               — demo accounts; not metered/billed/analytics
--
-- Lives on users (usage/quota are keyed by user_id). Idempotent + non-destructive:
-- ADD COLUMN ... DEFAULT on the small users table does not rewrite messages /
-- usage_events. account_class is server-side only — it is never surfaced in any
-- /v1 response (see docs/design/prober-selftest.md "API surface impact").
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS account_class TEXT NOT NULL DEFAULT 'standard';

DO $$ BEGIN
    ALTER TABLE users ADD CONSTRAINT users_account_class_check
        CHECK (account_class IN ('standard', 'internal', 'system', 'demo'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
