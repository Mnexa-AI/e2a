-- 057_messages_notify_job.sql
--
-- Durable HITL approval-notification on River (docs/design/hitl-notify-river.md).
--
-- The approve/reject notification email that fires when an outbound message enters
-- pending_review used to be a detached, best-effort goroutine — lost on a crash or
-- an SMTP outage between the 202 response and the send. It now rides a River job
-- (QueueNotify) enqueued in the SAME transaction as the pending_review row, mirroring
-- the outbound accept-tx (send_job_id, migration 054).
--
-- Two additive columns on messages:
--   notify_job_id — the River QueueNotify job id, stamped in the accept-tx so the
--                   startup reconciler can find rows stranded without a job
--                   (pending_review AND notify_job_id IS NULL AND notified_at IS NULL).
--   notified_at   — set by the worker AFTER a successful send; the send-dedup marker
--                   that makes a crash-after-send re-drive a no-op. Loss is
--                   impossible (only set post-send); a rare duplicate is benign.
--
-- Additive + idempotent. Nullable ADD COLUMN with no default is a metadata-only op
-- on Postgres — safe on the prod-sized messages table (same as 054/055).
--
-- The reconciler's partial index is built CONCURRENTLY in migration 058 (a plain
-- CREATE INDEX here would take a write lock on the whole messages table at deploy).

ALTER TABLE messages ADD COLUMN IF NOT EXISTS notify_job_id BIGINT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS notified_at   TIMESTAMPTZ;

-- Cutover: every message already in pending_review at the moment this ships was
-- created under the old code path, which ALREADY sent its notification (the detached
-- goroutine). Mark those rows notified so the startup reconciler does not re-notify
-- their owners a second time. (New holds get notify_job_id atomically and never
-- match the reconciler; a hold created on the no-notifier plain path keeps
-- notified_at NULL and is correctly picked up.) Touches only the tiny pending_review
-- set (bounded by TTL, served by idx_messages_pending_review) — cheap + idempotent.
UPDATE messages SET notified_at = now()
    WHERE status = 'pending_review' AND notified_at IS NULL;
