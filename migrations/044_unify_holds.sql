-- 044_unify_holds.sql
--
-- Unify message holds on the `review` vocabulary (design 2026-06-22). The outbound
-- HITL statuses (migration 003: pending_approval / rejected / expired_approved /
-- expired_rejected) are folded into the inbound review vocabulary (migration 040:
-- pending_review / review_rejected / review_expired_approved / review_expired_rejected)
-- so a held message is one direction-aware primitive. Outbound's "approved" terminal
-- stays `sent` (the approve triggers the send; there is no approved-but-unsent state).
--
-- Idempotent + non-destructive:
--   1. Backfill is scoped to the four retiring outbound statuses (a tiny, ≤10-day-TTL
--      slice) and is allowed under the existing 040 CHECK (which already permits the
--      review_* targets), so it runs before the constraint swap with no gap.
--   2. ADD CONSTRAINT takes ACCESS EXCLUSIVE on `messages` for the validation scan.
--      The migration runner wraps each file in one transaction, so a NOT VALID +
--      separate VALIDATE split would NOT reduce that (the lock is held across both in
--      one txn) — it is omitted here. The lock is bounded by the 10-day message TTL,
--      which keeps the table (hence the scan) small. If `messages` ever outgrows that
--      bound, split this into single-statement `-- e2a:no-transaction` migrations to
--      validate lock-free.

-- 1. Backfill the retiring outbound statuses onto the review vocabulary.
UPDATE messages SET status = CASE status
        WHEN 'pending_approval'  THEN 'pending_review'
        WHEN 'rejected'          THEN 'review_rejected'
        WHEN 'expired_approved'  THEN 'review_expired_approved'
        WHEN 'expired_rejected'  THEN 'review_expired_rejected'
    END
    WHERE status IN ('pending_approval', 'rejected', 'expired_approved', 'expired_rejected');

-- 2. Swap the CHECK to the unified set (drop the four retired outbound values).
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_status_check;
DO $$ BEGIN
    ALTER TABLE messages ADD CONSTRAINT messages_status_check
        CHECK (status IN (
            'sent',
            'pending_review', 'review_approved', 'review_rejected',
            'review_expired_approved', 'review_expired_rejected'
        ));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- 3. The outbound pending-sweep index is now covered by idx_messages_pending_review
--    (status='pending_review' spans both directions); drop the stale outbound index.
DROP INDEX IF EXISTS idx_messages_pending_approval;
