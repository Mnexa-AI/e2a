-- Preserve the exact observation that a worker could not atomically finalize.
-- Nullable, default-free columns avoid rewriting production-sized messages;
-- legacy rows continue to use reconciler-derived values.
ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS delivery_failure_occurred_at TIMESTAMPTZ;

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS delivery_failure_attempt INTEGER;

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS delivery_failure_blocked_recipients TEXT[];

COMMENT ON COLUMN messages.delivery_failure_occurred_at IS
    'Exact time of a terminal submission observation preserved for reconciliation.';

COMMENT ON COLUMN messages.delivery_failure_attempt IS
    'River attempt of a terminal submission observation preserved for reconciliation.';

COMMENT ON COLUMN messages.delivery_failure_blocked_recipients IS
    'Normalized recipients blocked by the preserved send-time suppression observation.';
