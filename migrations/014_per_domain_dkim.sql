-- 014_per_domain_dkim.sql
--
-- Per-domain DKIM keypair storage (BACKEND_TODO #5).
--
-- Today the outbound relay signs every message with a single
-- deployment-level DKIM key. Strict receivers (Gmail, Microsoft 365)
-- treat that as a DMARC alignment failure when the From header is on a
-- user-owned custom domain — the message often ends up in spam or is
-- rejected outright. The fix: generate one RSA-2048 keypair per
-- registered domain and sign outbound mail with the key matching the
-- From header's domain.
--
-- Columns:
--   - dkim_selector: short identifier (e.g. "e2a202605") that names this
--     keypair in DNS. Selector + domain form the lookup name
--     "{selector}._domainkey.{domain}". Stored so we can rotate selectors
--     without breaking signature verification on in-flight mail.
--   - dkim_public_key: base64-encoded SubjectPublicKeyInfo, header/newlines
--     stripped. This is the literal "p=" value users paste into their
--     DNS TXT record at "{selector}._domainkey.{domain}".
--   - dkim_private_key: PKCS#1 DER-encoded RSA private key. BYTEA rather
--     than TEXT to keep it opaque — the column is never returned by the
--     JSON API, only read by the outbound signer.
--
-- All three columns are nullable: existing domains (and the seeded
-- shared-domain row) don't have keys until the next backfill pass. The
-- outbound signer treats a missing key as "skip DKIM" so partial
-- adoption is non-fatal.
--
-- Idempotent: ADD COLUMN IF NOT EXISTS, no DROP, no rewrites. Safe to
-- rerun on prod-sized tables.

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS dkim_selector TEXT;

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS dkim_public_key TEXT;

ALTER TABLE domains
    ADD COLUMN IF NOT EXISTS dkim_private_key BYTEA;
