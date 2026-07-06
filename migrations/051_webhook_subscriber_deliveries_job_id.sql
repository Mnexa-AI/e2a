-- 051_webhook_subscriber_deliveries_job_id.sql
--
-- Webhook delivery → River migration (docs/design/webhook-delivery-river-migration.md).
-- webhook_subscriber_deliveries flips from BEING the execution queue (SKIP LOCKED
-- lease + hand-rolled retry) to being pure Layer-2 delivery STATE, written by a
-- River DeliverWorker. This column links a delivery row to its River job:
--   - observability (which river_job drives this delivery), and
--   - the one-shot cutover discriminator: the legacy worker is retired and every
--     pre-cutover `pending` row with job_id IS NULL gets exactly one River job
--     enqueued (the NULL guard makes the one-shot migration idempotent).
--
-- Nullable, additive, no backfill. Idempotent + non-destructive (ADD COLUMN IF
-- NOT EXISTS; metadata-only, no table rewrite).

ALTER TABLE webhook_subscriber_deliveries
    ADD COLUMN IF NOT EXISTS job_id BIGINT;
