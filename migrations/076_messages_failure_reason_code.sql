-- Durable canonical attribution for an outbound terminal failure whose full
-- state+lifecycle+event transaction must be retried by the reconciler.
-- Nullable with no default so PostgreSQL records only catalog metadata and
-- does not rewrite the production-sized messages table. Application catalog
-- validation owns the closed vocabulary; no validating FK/CHECK is added here.
ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS delivery_failure_reason_code TEXT;

COMMENT ON COLUMN messages.delivery_failure_reason_code IS
    'Canonical lifecycle reason for a durable outbound failure observation; validated by the application catalog.';
