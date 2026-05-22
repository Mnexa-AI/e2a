-- 010_usage_events_created_idx.sql
-- e2a:no-transaction
--
-- Index supporting global time-range scans of usage_events. Powers
-- the hosted v_top_agents_7d view (filters by created_at, then groups
-- by agent_id) and any "events in the last N days" query that isn't
-- already served by usage_summaries' (user_id, bucket_date) PK.
--
-- Why a separate index from 009: that one's leading column is user_id,
-- so it can't serve a query that filters created_at alone — Postgres
-- would full-scan. A dedicated (created_at) index closes that gap.
--
-- CREATE INDEX CONCURRENTLY per the same reasoning as 009. Split into
-- its own file because the runner requires single-statement migrations
-- under the e2a:no-transaction directive.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_events_created
    ON usage_events (created_at);
