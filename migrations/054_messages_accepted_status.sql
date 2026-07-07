-- 054_messages_accepted_status.sql
--
-- Async outbound pipeline (docs/design/async-message-pipeline.md): the accept-tx
-- writes messages.delivery_status='accepted', and the send worker writes 'sending'
-- then 'sent'/'failed'. `internal/delivery/status.go` (from #385) already defines
-- these values, but migration 031's CHECK constraints predate them and would REJECT
-- them. Widen both constraints to include 'accepted' and 'sending'.
--
-- Also add messages.send_job_id — the River outbound_send job id stamped by the
-- accept-tx, so the reconciler can find stranded rows ('accepted' with no live job),
-- mirroring webhook_subscriber_deliveries.job_id (migration 051).

ALTER TABLE messages ADD COLUMN IF NOT EXISTS send_job_id BIGINT;

-- Widen the CHECKs. The new set is a strict SUPERSET of the old, so every existing
-- row already conforms — ADD ... NOT VALID skips the validation scan (fast metadata
-- op, no ACCESS EXCLUSIVE table scan on prod-sized `messages`) while still enforcing
-- the widened set on all subsequent writes. DROP IF EXISTS first keeps it idempotent.
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_delivery_status_check;
ALTER TABLE messages ADD CONSTRAINT messages_delivery_status_check
    CHECK (delivery_status IS NULL OR delivery_status IN
        ('accepted','sending','queued','sent','delivered','bounced','complained','deferred','failed'))
    NOT VALID;

ALTER TABLE message_recipients DROP CONSTRAINT IF EXISTS message_recipients_status_check;
ALTER TABLE message_recipients ADD CONSTRAINT message_recipients_status_check
    CHECK (status IN
        ('accepted','sending','queued','sent','delivered','bounced','complained','deferred','failed'))
    NOT VALID;
