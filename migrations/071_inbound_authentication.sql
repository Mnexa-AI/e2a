-- Canonical inbound identities and RFC 9989 authentication evidence.
-- envelope_from already exists from migration 055.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS header_from TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS authentication JSONB;
