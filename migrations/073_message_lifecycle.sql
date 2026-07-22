-- Canonical append-only Message Trust Ledger.
-- Both tables are new and empty on deployment; no existing message rows are
-- rewritten or scanned beyond ordinary foreign-key metadata validation.

CREATE TABLE IF NOT EXISTS message_lifecycle_reason_codes (
    code      TEXT PRIMARY KEY,
    stage     TEXT NOT NULL CHECK (stage IN (
        'accepted', 'authentication', 'review', 'suppression',
        'queued', 'submission', 'delivery', 'complaint'
    )),
    outcome   TEXT NOT NULL CHECK (outcome IN (
        'accepted', 'passed', 'failed', 'indeterminate', 'pending',
        'approved', 'rejected', 'blocked', 'applied', 'enqueued',
        'deferred', 'delivered', 'bounced', 'reported'
    )),
    retryable BOOLEAN NOT NULL,
    UNIQUE (code, stage, outcome, retryable)
);

INSERT INTO message_lifecycle_reason_codes (code, stage, outcome, retryable)
VALUES
    ('acceptance.inbound_smtp',                 'accepted',       'accepted',      false),
    ('acceptance.outbound_api',                 'accepted',       'accepted',      false),
    ('acceptance.local_loopback',               'accepted',       'accepted',      false),
    ('authentication.dmarc_pass',               'authentication', 'passed',        false),
    ('authentication.dmarc_fail',               'authentication', 'failed',        false),
    ('authentication.dmarc_none',               'authentication', 'indeterminate', false),
    ('authentication.dmarc_temporary_error',    'authentication', 'indeterminate', true),
    ('authentication.dmarc_permanent_error',    'authentication', 'indeterminate', false),
    ('review.hold_created',                      'review',         'pending',       false),
    ('review.approved',                          'review',         'approved',      false),
    ('review.rejected',                          'review',         'rejected',      false),
    ('review.expired_approved',                  'review',         'approved',      false),
    ('review.expired_rejected',                  'review',         'rejected',      false),
    ('suppression.recipient_blocked',            'suppression',    'blocked',       false),
    ('suppression.hard_bounce_applied',          'suppression',    'applied',       false),
    ('suppression.complaint_applied',            'suppression',    'applied',       false),
    ('queue.inbound_processing',                 'queued',         'enqueued',      false),
    ('queue.outbound_submission',                'queued',         'enqueued',      false),
    ('submission.upstream_accepted',             'submission',     'accepted',      false),
    ('submission.local_loopback_accepted',       'submission',     'accepted',      false),
    ('submission.temporary_failure',             'submission',     'deferred',      true),
    ('submission.provider_rejected',             'submission',     'failed',        false),
    ('submission.local_retries_exhausted',       'submission',     'failed',        true),
    ('submission.cancelled',                     'submission',     'failed',        false),
    ('delivery.recipient_server_accepted',       'delivery',       'delivered',     false),
    ('delivery.temporary_delay',                  'delivery',       'deferred',      true),
    ('delivery.permanent_bounce',                 'delivery',       'bounced',       false),
    ('delivery.transient_bounce',                 'delivery',       'bounced',       true),
    ('delivery.undetermined_bounce',              'delivery',       'bounced',       false),
    ('complaint.recipient_reported',              'complaint',      'reported',      false)
ON CONFLICT (code) DO NOTHING;

-- Catalog meanings are immutable. ON CONFLICT deliberately never overwrites
-- an existing code, while this assertion fails deployment if that code already
-- has a different tuple. Error text identifies only the stable code.
DO $$
DECLARE
    drifted_code TEXT;
BEGIN
    SELECT expected.code
    INTO drifted_code
    FROM (VALUES
        ('acceptance.inbound_smtp',                 'accepted',       'accepted',      false),
        ('acceptance.outbound_api',                 'accepted',       'accepted',      false),
        ('acceptance.local_loopback',               'accepted',       'accepted',      false),
        ('authentication.dmarc_pass',               'authentication', 'passed',        false),
        ('authentication.dmarc_fail',               'authentication', 'failed',        false),
        ('authentication.dmarc_none',               'authentication', 'indeterminate', false),
        ('authentication.dmarc_temporary_error',    'authentication', 'indeterminate', true),
        ('authentication.dmarc_permanent_error',    'authentication', 'indeterminate', false),
        ('review.hold_created',                      'review',         'pending',       false),
        ('review.approved',                          'review',         'approved',      false),
        ('review.rejected',                          'review',         'rejected',      false),
        ('review.expired_approved',                  'review',         'approved',      false),
        ('review.expired_rejected',                  'review',         'rejected',      false),
        ('suppression.recipient_blocked',            'suppression',    'blocked',       false),
        ('suppression.hard_bounce_applied',          'suppression',    'applied',       false),
        ('suppression.complaint_applied',            'suppression',    'applied',       false),
        ('queue.inbound_processing',                 'queued',         'enqueued',      false),
        ('queue.outbound_submission',                'queued',         'enqueued',      false),
        ('submission.upstream_accepted',             'submission',     'accepted',      false),
        ('submission.local_loopback_accepted',       'submission',     'accepted',      false),
        ('submission.temporary_failure',             'submission',     'deferred',      true),
        ('submission.provider_rejected',             'submission',     'failed',        false),
        ('submission.local_retries_exhausted',       'submission',     'failed',        true),
        ('submission.cancelled',                     'submission',     'failed',        false),
        ('delivery.recipient_server_accepted',       'delivery',       'delivered',     false),
        ('delivery.temporary_delay',                  'delivery',       'deferred',      true),
        ('delivery.permanent_bounce',                 'delivery',       'bounced',       false),
        ('delivery.transient_bounce',                 'delivery',       'bounced',       true),
        ('delivery.undetermined_bounce',              'delivery',       'bounced',       false),
        ('complaint.recipient_reported',              'complaint',      'reported',      false)
    ) AS expected(code, stage, outcome, retryable)
    LEFT JOIN message_lifecycle_reason_codes AS actual
      ON actual.code = expected.code
     AND actual.stage = expected.stage
     AND actual.outcome = expected.outcome
     AND actual.retryable = expected.retryable
    WHERE actual.code IS NULL
    ORDER BY expected.code
    LIMIT 1;

    IF drifted_code IS NOT NULL THEN
        RAISE EXCEPTION 'message lifecycle catalog mismatch for code %', drifted_code;
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS message_lifecycle_transitions (
    id              TEXT PRIMARY KEY,
    message_id      TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    dedupe_key      TEXT NOT NULL,
    direction       TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    recipient       TEXT,
    stage           TEXT NOT NULL,
    outcome         TEXT NOT NULL,
    reason_code     TEXT NOT NULL,
    retryable       BOOLEAN NOT NULL,
    evidence        JSONB NOT NULL DEFAULT '{}',
    correlation_ids JSONB NOT NULL DEFAULT '{}',
    occurred_at     TIMESTAMPTZ NOT NULL,
    reconstructed   BOOLEAN NOT NULL DEFAULT false,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, dedupe_key),
    FOREIGN KEY (reason_code, stage, outcome, retryable)
        REFERENCES message_lifecycle_reason_codes (code, stage, outcome, retryable)
);

CREATE INDEX IF NOT EXISTS message_lifecycle_message_order_idx
    ON message_lifecycle_transitions (message_id, occurred_at, id);
