-- 028_webhook_subscriber_deliveries_event_id.sql
--
-- Slice 2 prerequisite: links webhook_subscriber_deliveries rows back to
-- the originating webhook_events row, and adds the partial unique index
-- that prevents multi-replica duplicate fan-out (design §4.4).
--
-- event_id: logical link to webhook_events.id. No FK because the parent
--   has a single-column PK but FK semantics differ from what we want
--   (we don't want CASCADE on parent expire; we want app-layer 410
--   handling). The replay handler checks the event exists at API time.
--
-- replay_id: null for first-delivery rows, whd_<…> for replays. Lets
--   the partial unique index distinguish "first delivery for this
--   (event,webhook)" (NULL replay_id, constrained) from "explicit
--   replay" (NON-NULL replay_id, not constrained).
--
-- idx_wsd_event_webhook_uniq: enforces ONE first-delivery row per
--   (event_id, webhook_id) pair. This is the partial unique index from
--   §4.4 that makes the outbox worker's per-row ON CONFLICT DO NOTHING
--   safe under multi-replica race: two workers both fanning out the
--   same event each try to insert; the first wins, the second no-ops.
--
-- idx_wsd_event_id: supporting index for the /redeliver-since handler
--   which needs to skip events that already have a pending delivery
--   for a given webhook.
--
-- MIGRATION SAFETY: this index build runs against a populated table.
-- It is *safe* in this deploy because event_id is being added in the
-- SAME migration with no DEFAULT (all existing rows have NULL). The
-- partial predicate excludes NULL event_ids → the index covers zero
-- existing rows → build is a fast heap scan. If re-applied later on a
-- populated table that already has event_ids, a CONCURRENTLY rebuild
-- would be required — but the IF NOT EXISTS guard makes that
-- unreachable.

ALTER TABLE webhook_subscriber_deliveries
    ADD COLUMN IF NOT EXISTS event_id TEXT;

ALTER TABLE webhook_subscriber_deliveries
    ADD COLUMN IF NOT EXISTS replay_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_wsd_event_webhook_uniq
    ON webhook_subscriber_deliveries (event_id, webhook_id)
    WHERE event_id IS NOT NULL AND replay_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_wsd_event_id
    ON webhook_subscriber_deliveries (event_id)
    WHERE event_id IS NOT NULL;
