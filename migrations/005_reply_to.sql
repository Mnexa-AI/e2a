-- Store the parsed Reply-To: header on inbound messages so SDK consumers
-- can identify the intended reply mailbox without re-parsing raw_message.
-- Empty array (not NULL) when the header was absent — the SDK contract
-- promises an empty list, never a fallback to From:.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to text[];
