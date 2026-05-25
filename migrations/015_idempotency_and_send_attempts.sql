-- 015_idempotency_and_send_attempts.sql
--
-- Two related tables that together give the outbound send path
-- exactly-once semantics under retry:
--
--   1) idempotency_keys (slice 1) — caller-facing dedup. When a request
--      sets `Idempotency-Key: <string>` on POST /api/v1/send or POST
--      /api/v1/agents/{email}/messages/{id}/reply, the server claims a
--      row here, does the work once, and replays the cached response
--      verbatim on any subsequent request with the same key.
--
--      Scope is (user_id, key) rather than (api_key_id, key). Stripe
--      uses account-level scoping for the same reason: API keys are
--      credentials, not identities, and a user rotating their key
--      should not silently reset their idempotency window. UUIDv4
--      collisions across a single user's keys are mathematically
--      negligible, and the body-hash check below catches the
--      pathological-collision case explicitly with a 422.
--
--      response_body holds the exact bytes the server returned on the
--      first call (Content-Type included separately so the replay
--      reproduces the original response wire-faithfully). status_code
--      is the HTTP status to replay (typically 200 for immediate send
--      or 202 for HITL-held).
--
--      status='in_progress' is the claim marker between INSERT and the
--      UPDATE that records the response. A row that sits in_progress
--      longer than the in-code stale window (5min) is treated as the
--      remnant of a crashed handler and re-claimable by the next
--      caller; see internal/idempotency for the takeover rule.
--
--   2) send_attempts (slice 2) — internal exactly-once gate for the
--      HITL approval path. Closes the documented crash window in
--      internal/identity/store.go: ApproveAndSend used to call the
--      SES-bound send() callback inside its approval transaction, so
--      a successful SES handoff followed by a DB commit failure would
--      leave the message row pending — and a retry would re-send to
--      SES.
--
--      The fix: ApproveAndSend now writes send_attempts rows in two
--      small autonomous transactions that bracket the SES call, so
--      the outcome of the upstream send survives any failure of the
--      surrounding approval transaction. On retry, ApproveAndSend
--      consults send_attempts FIRST: status='sent' means the previous
--      attempt succeeded at SES — reuse the recorded provider id,
--      skip the SES call. status='attempting' that is recent (<10min)
--      means another worker is mid-send — return 409 and let the
--      caller retry. status='failed' or no row means proceed.
--
-- Both tables are referenced in code under their bare names; see
-- internal/idempotency/ (slice 1) and internal/identity/store.go's
-- ApproveAndSend (slice 2).
--
-- Idempotent: CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT EXISTS,
-- no DROP, no rewrites. Safe to rerun on prod-sized data.

CREATE TABLE IF NOT EXISTS idempotency_keys (
    user_id               TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key                   TEXT        NOT NULL,
    request_path          TEXT        NOT NULL,
    request_body_hash     TEXT        NOT NULL,
    response_status       INTEGER     NOT NULL DEFAULT 0,
    response_content_type TEXT        NOT NULL DEFAULT '',
    response_body         BYTEA       NOT NULL DEFAULT ''::bytea,
    status                TEXT        NOT NULL CHECK (status IN ('in_progress', 'completed')),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at          TIMESTAMPTZ,
    PRIMARY KEY (user_id, key)
);

CREATE INDEX IF NOT EXISTS idx_idempotency_keys_created_at ON idempotency_keys(created_at);

CREATE TABLE IF NOT EXISTS send_attempts (
    message_id         TEXT        PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    attempted_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at       TIMESTAMPTZ,
    status             TEXT        NOT NULL CHECK (status IN ('attempting', 'sent', 'failed')),
    provider_message_id TEXT       NOT NULL DEFAULT '',
    method             TEXT        NOT NULL DEFAULT '',
    to_recipients      TEXT[]      NOT NULL DEFAULT '{}',
    cc_recipients      TEXT[]      NOT NULL DEFAULT '{}',
    bcc_recipients     TEXT[]      NOT NULL DEFAULT '{}',
    error              TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_send_attempts_attempted_at ON send_attempts(attempted_at);
