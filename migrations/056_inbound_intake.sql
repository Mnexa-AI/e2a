-- 056_inbound_intake.sql
--
-- Queue-first inbound pipeline (docs/design/inbound-message-pipeline-river.md).
-- inbound_intake is Layer 1: the durable landing pad for a received message. The
-- SMTP session writes the raw MIME + envelope + connecting IP here (the ONLY work
-- that gates 250) and enqueues a River job (QueueInbound) referencing the row in the
-- same transaction; the internal/inboundprocess worker then parses, screens,
-- persists the messages row, and delivers — off the SMTP critical path.
--
-- This is NOT a hand-rolled claim queue (no SKIP-LOCKED lease here): River owns
-- claim/retry/rescue via the job. The table is just the durable record the job reads
-- (raw is up to 10MB — too large for a job arg) plus the dedup key.
--
-- remote_ip is captured because SPF (RFC 7208) needs the connecting IP, available
-- only in-session — the async worker cannot recompute it.
--
-- Additive + idempotent. inbound_intake is a fresh table (no prod-size rewrite risk).

CREATE TABLE IF NOT EXISTS inbound_intake (
    id             TEXT PRIMARY KEY,                         -- intk_<rand>
    recipient      TEXT NOT NULL,                            -- RCPT TO (the agent)
    envelope_from  TEXT NOT NULL DEFAULT '',                 -- MAIL FROM (for SPF/DMARC)
    remote_ip      TEXT NOT NULL DEFAULT '',                 -- connecting IP (for SPF)
    raw_message    BYTEA NOT NULL,                           -- the raw MIME
    message_id     TEXT NOT NULL DEFAULT '',                 -- sender's RFC 5322 Message-ID (dedup)
    content_hash   TEXT NOT NULL,                            -- sha256 of raw (dedup)
    status         TEXT NOT NULL DEFAULT 'accepted'
        CHECK (status IN ('accepted', 'processed', 'failed')),
    process_job_id BIGINT,                                   -- River QueueInbound job id
    message_fk     TEXT,                                     -- resulting messages.id once processed
    detail         TEXT,                                     -- failure detail (status='failed')
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),       -- accept time
    processed_at   TIMESTAMPTZ                               -- terminal (processed/failed) time
);

-- Dedup: an MTA retry after a lost 250 re-sends the same (recipient, Message-ID,
-- body). ON CONFLICT DO NOTHING against this index makes the accept idempotent — a
-- duplicate re-uses the row and enqueues no second job. When Message-ID is absent
-- ('') the key degrades to (recipient, content_hash), still collapsing identical
-- resends.
CREATE UNIQUE INDEX IF NOT EXISTS idx_inbound_intake_dedup
    ON inbound_intake (recipient, message_id, content_hash);

-- Startup/periodic reconciler: accepted rows that never got a job (crash between
-- insert and enqueue, or the mode-flip moment). Partial index keeps it cheap.
CREATE INDEX IF NOT EXISTS idx_inbound_intake_unenqueued
    ON inbound_intake (created_at)
    WHERE status = 'accepted' AND process_job_id IS NULL;

-- Retention sweep: prune processed rows older than the window (raw also lives in
-- messages.raw_message once processed).
CREATE INDEX IF NOT EXISTS idx_inbound_intake_processed_at
    ON inbound_intake (processed_at)
    WHERE status = 'processed';
