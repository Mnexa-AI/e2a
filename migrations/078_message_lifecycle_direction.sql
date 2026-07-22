-- Reject lifecycle observations whose direction contradicts their owning
-- message. A trigger is used instead of a validated relational constraint so
-- deployment does not scan or rewrite production-sized tables. Historical rows
-- remain untouched; every new insert or direction/message reassignment is
-- checked by indexed messages(id) lookup.

CREATE OR REPLACE FUNCTION enforce_message_lifecycle_direction()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    owning_direction TEXT;
BEGIN
    SELECT direction
      INTO owning_direction
      FROM messages
     WHERE id = NEW.message_id;

    IF FOUND AND NEW.direction IS DISTINCT FROM owning_direction THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            CONSTRAINT = 'message_lifecycle_direction_matches_message',
            MESSAGE = 'message lifecycle direction does not match owning message';
    END IF;
    RETURN NEW;
END;
$$;

-- Direction remains mutable for messages that have no lifecycle observations.
-- Once a child exists, changing the parent would rewrite the meaning of the
-- append-only ledger, so reject it with an indexed existence check.
CREATE OR REPLACE FUNCTION enforce_message_direction_with_lifecycle()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.direction IS DISTINCT FROM OLD.direction
       AND EXISTS (
           SELECT 1
             FROM message_lifecycle_transitions
            WHERE message_id = OLD.id
       ) THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            CONSTRAINT = 'message_direction_matches_lifecycle',
            MESSAGE = 'message direction cannot change after lifecycle observations exist';
    END IF;
    RETURN NEW;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_trigger
         WHERE tgrelid = 'message_lifecycle_transitions'::regclass
           AND tgname = 'message_lifecycle_direction_matches_message'
           AND NOT tgisinternal
    ) THEN
        CREATE TRIGGER message_lifecycle_direction_matches_message
        BEFORE INSERT OR UPDATE OF message_id, direction
        ON message_lifecycle_transitions
        FOR EACH ROW
        EXECUTE FUNCTION enforce_message_lifecycle_direction();
    END IF;
END;
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_trigger
         WHERE tgrelid = 'messages'::regclass
           AND tgname = 'message_direction_matches_lifecycle'
           AND NOT tgisinternal
    ) THEN
        CREATE TRIGGER message_direction_matches_lifecycle
        BEFORE UPDATE OF direction
        ON messages
        FOR EACH ROW
        EXECUTE FUNCTION enforce_message_direction_with_lifecycle();
    END IF;
END;
$$;
