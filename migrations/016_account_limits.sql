-- 016_account_limits.sql
--
-- Per-user resource caps + matching usage rollup. Two tables, one purpose:
-- the OSS server gains a generic limits primitive (no Stripe, no plan
-- names, no $) that any operator can drive however they like.
--
-- account_limits — the caps. One row per user; missing row falls back to
-- operator-configured defaults (config.yaml `limits:` block). `plan_code`
-- and `upgrade_url` are opaque to the OSS server: they are written by
-- whatever provisions the row (hosted-service sidecar, admin tool, manual
-- SQL) and echoed back to the dashboard verbatim. The OSS server only
-- enforces the integer caps.
--
-- account_usage — current stock counters that pair with the stock caps
-- (storage today; future stock metrics fit here). Flow caps like
-- max_messages_month are tracked in usage_summaries instead, since that
-- table already aggregates per-day per-user message counts.
--
-- Storage is maintained by a trigger on the messages table: every INSERT
-- adds the sum of the size-bearing columns (raw_message, body_text,
-- body_html, attachments_json) to the user's storage_bytes; every DELETE
-- subtracts. The trigger guarantees consistency across all delete paths
-- (the retention sweep, ON DELETE CASCADE from agent_identities deletion,
-- user-initiated message delete) without requiring every call site to
-- remember to update the counter.
--
-- The trigger does NOT handle UPDATE. The only known column-update path
-- today is HITL approve-with-edits replacing body_text/body_html; the
-- resulting drift is bounded (a few KB per edited approval) and the
-- counter self-corrects on the retention sweep when the message expires.
-- Add UPDATE handling here if a larger update path emerges.
--
-- One-time backfill seeds account_usage from existing messages so the
-- counter is accurate the first time the trigger fires. ON CONFLICT DO
-- NOTHING makes the backfill idempotent — re-running the migration is a
-- no-op once rows exist.
--
-- Idempotent: CREATE TABLE/TRIGGER/FUNCTION use IF NOT EXISTS or
-- OR REPLACE. Additive only — no destructive ALTERs. Safe to rerun on
-- prod-sized data; the backfill INSERT scales linearly with messages but
-- runs once.

CREATE TABLE IF NOT EXISTS account_limits (
    user_id            TEXT        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    plan_code          TEXT        NOT NULL DEFAULT 'default',
    max_agents         INTEGER     NOT NULL,
    max_domains        INTEGER     NOT NULL,
    max_messages_month INTEGER     NOT NULL,
    max_storage_bytes  BIGINT      NOT NULL,
    upgrade_url        TEXT        NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_account_limits_updated_at ON account_limits(updated_at);

CREATE TABLE IF NOT EXISTS account_usage (
    user_id       TEXT        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    storage_bytes BIGINT      NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Storage delta trigger. The size of a message is the sum of its
-- size-bearing columns; nullable columns are coerced to 0 so the math
-- works on rows that only populate a subset (e.g. inbound rows have
-- raw_message but no body_*; HITL pending outbound rows have body_*
-- but no raw_message).
CREATE OR REPLACE FUNCTION e2a_messages_storage_delta() RETURNS TRIGGER AS $$
DECLARE
    uid   TEXT;
    delta BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT user_id INTO uid FROM agent_identities WHERE id = NEW.agent_id;
        IF uid IS NULL THEN
            RETURN NEW;
        END IF;
        delta := COALESCE(octet_length(NEW.raw_message), 0)
               + COALESCE(octet_length(NEW.body_text), 0)
               + COALESCE(octet_length(NEW.body_html), 0)
               + COALESCE(octet_length(NEW.attachments_json::text), 0);
        INSERT INTO account_usage (user_id, storage_bytes)
        VALUES (uid, delta)
        ON CONFLICT (user_id) DO UPDATE
            SET storage_bytes = account_usage.storage_bytes + EXCLUDED.storage_bytes,
                updated_at    = now();
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        SELECT user_id INTO uid FROM agent_identities WHERE id = OLD.agent_id;
        IF uid IS NULL THEN
            RETURN OLD;
        END IF;
        delta := COALESCE(octet_length(OLD.raw_message), 0)
               + COALESCE(octet_length(OLD.body_text), 0)
               + COALESCE(octet_length(OLD.body_html), 0)
               + COALESCE(octet_length(OLD.attachments_json::text), 0);
        UPDATE account_usage
           SET storage_bytes = GREATEST(storage_bytes - delta, 0),
               updated_at    = now()
         WHERE user_id = uid;
        RETURN OLD;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS e2a_messages_storage_delta_trg ON messages;
CREATE TRIGGER e2a_messages_storage_delta_trg
AFTER INSERT OR DELETE ON messages
FOR EACH ROW EXECUTE FUNCTION e2a_messages_storage_delta();

-- One-time backfill of storage from existing messages. Idempotent via
-- ON CONFLICT DO NOTHING: the second time the migration runs, every user
-- with messages already has a row, so the INSERT is a no-op. Users with
-- zero messages get no row; the limits enforcer treats a missing row as
-- storage_bytes=0, which is correct.
INSERT INTO account_usage (user_id, storage_bytes)
SELECT a.user_id,
       COALESCE(SUM(
           COALESCE(octet_length(m.raw_message), 0)
         + COALESCE(octet_length(m.body_text), 0)
         + COALESCE(octet_length(m.body_html), 0)
         + COALESCE(octet_length(m.attachments_json::text), 0)
       ), 0)
  FROM messages m
  JOIN agent_identities a ON a.id = m.agent_id
 GROUP BY a.user_id
ON CONFLICT (user_id) DO NOTHING;
