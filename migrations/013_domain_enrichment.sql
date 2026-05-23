-- 013_domain_enrichment.sql
--
-- Surface "is this the user's default domain?" and "when did we last
-- check DNS?" on the domains list. The redesign's Domains page renders
-- both as chips/timestamps; without them the UI shows static
-- "verified/pending" only.
--
-- - is_primary: at most one TRUE per user, enforced by a partial unique
--   index. Read paths don't change for non-primary callers — the column
--   is purely metadata until callers opt in.
-- - last_checked_at: updated whenever the verification probe runs (whether
--   it succeeded or not). NULL means "never probed since the column was
--   added" — distinct from "probed and failed", which is a separate state
--   captured by `verified=false` + a non-null `last_checked_at`.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS leaves existing schemas
-- untouched. Non-destructive on prod-sized tables — both columns are
-- nullable with default-NULL behavior, so the add is metadata-only.
--
-- The partial unique index has UNIQUE in its name to match the existing
-- naming convention for partial uniques in this schema. Created without
-- CONCURRENTLY because the domains table is small — a one-pass scan
-- under an exclusive lock is faster than the concurrent-index machinery
-- when row counts are in the hundreds.

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS last_checked_at TIMESTAMPTZ;

-- Enforce one primary per user. NULL user_ids (the seeded shared
-- domain) are excluded — they're not "owned" so the per-user
-- uniqueness invariant doesn't apply.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_domains_primary_per_user
    ON domains (user_id)
    WHERE is_primary = true AND user_id IS NOT NULL;
