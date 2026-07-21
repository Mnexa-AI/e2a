-- Canonical inbound identities and RFC 9989 authentication evidence.
-- envelope_from already exists from migration 055.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS header_from TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS authentication JSONB;

-- Retain the SMTP greeting across the queue-first intake boundary so a null
-- reverse path can use postmaster@HELO as required by RFC 7208 section 2.4.
ALTER TABLE inbound_intake ADD COLUMN IF NOT EXISTS helo_domain TEXT NOT NULL DEFAULT '';
