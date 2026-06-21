-- 039_messages_storage_update_meter.sql
--
-- The account_usage storage trigger (migration 016) fired only on INSERT and
-- DELETE. Body content that changes via UPDATE went UNMETERED — most importantly
-- the HITL finalizers (ApproveAndSend / ExpireApproveAndSend), which now write
-- messages.raw_message (the retained "Sent folder" copy) via UPDATE while scrubbing
-- the draft body columns. Result: a HITL-approved send's body was stored off the
-- meter, so account_usage.storage_bytes drifted and the per-user storage cap could
-- not enforce held/sent bodies.
--
-- Fix: recompute the per-row body size on UPDATE and apply (NEW - OLD) to the
-- owner's storage_bytes, and fire the trigger on UPDATE as well. The INSERT/DELETE
-- branches are unchanged. Idempotent: CREATE OR REPLACE FUNCTION + DROP/CREATE
-- TRIGGER.

CREATE OR REPLACE FUNCTION e2a_messages_storage_delta() RETURNS TRIGGER AS $$
DECLARE
    uid       TEXT;
    new_bytes BIGINT;
    old_bytes BIGINT;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT user_id INTO uid FROM agent_identities WHERE id = NEW.agent_id;
        IF uid IS NULL THEN
            RETURN NEW;
        END IF;
        new_bytes := COALESCE(octet_length(NEW.raw_message), 0)
                   + COALESCE(octet_length(NEW.body_text), 0)
                   + COALESCE(octet_length(NEW.body_html), 0)
                   + COALESCE(octet_length(NEW.attachments_json::text), 0);
        INSERT INTO account_usage (user_id, storage_bytes)
        VALUES (uid, new_bytes)
        ON CONFLICT (user_id) DO UPDATE
            SET storage_bytes = account_usage.storage_bytes + EXCLUDED.storage_bytes,
                updated_at    = now();
        RETURN NEW;

    ELSIF TG_OP = 'UPDATE' THEN
        -- agent_id never changes on a message UPDATE, so NEW.agent_id is correct.
        SELECT user_id INTO uid FROM agent_identities WHERE id = NEW.agent_id;
        IF uid IS NULL THEN
            RETURN NEW;
        END IF;
        new_bytes := COALESCE(octet_length(NEW.raw_message), 0)
                   + COALESCE(octet_length(NEW.body_text), 0)
                   + COALESCE(octet_length(NEW.body_html), 0)
                   + COALESCE(octet_length(NEW.attachments_json::text), 0);
        old_bytes := COALESCE(octet_length(OLD.raw_message), 0)
                   + COALESCE(octet_length(OLD.body_text), 0)
                   + COALESCE(octet_length(OLD.body_html), 0)
                   + COALESCE(octet_length(OLD.attachments_json::text), 0);
        IF new_bytes <> old_bytes THEN
            UPDATE account_usage
               SET storage_bytes = GREATEST(storage_bytes + (new_bytes - old_bytes), 0),
                   updated_at    = now()
             WHERE user_id = uid;
        END IF;
        RETURN NEW;

    ELSIF TG_OP = 'DELETE' THEN
        SELECT user_id INTO uid FROM agent_identities WHERE id = OLD.agent_id;
        IF uid IS NULL THEN
            RETURN OLD;
        END IF;
        old_bytes := COALESCE(octet_length(OLD.raw_message), 0)
                   + COALESCE(octet_length(OLD.body_text), 0)
                   + COALESCE(octet_length(OLD.body_html), 0)
                   + COALESCE(octet_length(OLD.attachments_json::text), 0);
        UPDATE account_usage
           SET storage_bytes = GREATEST(storage_bytes - old_bytes, 0),
               updated_at    = now()
         WHERE user_id = uid;
        RETURN OLD;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS e2a_messages_storage_delta_trg ON messages;
CREATE TRIGGER e2a_messages_storage_delta_trg
    AFTER INSERT OR UPDATE OR DELETE ON messages
    FOR EACH ROW EXECUTE FUNCTION e2a_messages_storage_delta();
