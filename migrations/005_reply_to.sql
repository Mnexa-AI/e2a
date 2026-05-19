-- Store the parsed Reply-To: header on inbound messages so SDK consumers
-- can identify the intended reply mailbox without re-parsing raw_message.
--
-- No DEFAULT clause: existing rows stay NULL (backfilling text[] across
-- a large table would lock for long enough to matter, and the SDK
-- normalization layer surfaces NULL as [] anyway). New inserts where
-- the Go layer passes a nil slice also store NULL. Consumers reading
-- raw SQL should COALESCE; the SDK contract — "always a list, never
-- None / null" — is enforced in Python by `list(data.get("reply_to")
-- or [])` and in TS by `detail.reply_to ?? []`.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to text[];
