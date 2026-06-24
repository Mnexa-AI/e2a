-- 048_drop_webhook_signing_secrets.sql
--
-- Removes the legacy per-USER signing-secret layer. The deployment HMAC
-- secret (cfg.Signing.HMACSecret / E2A_HMAC_SECRET) is now the sole signer
-- for (a) the relay's X-E2A-Auth-* headers and (b) HITL approval /
-- magic-link tokens. The per-user management API was already removed in the
-- v1 cutover, so nothing tenant-side verifies these rows; webhook delivery
-- authenticity is carried separately by the per-WEBHOOK whsec_ secret
-- (webhooks.signing_secret, migration 023), which this does NOT touch.
--
-- Created in migration 004; this is its end-of-life. The table is tiny
-- (<=5 rows per user), so a DROP is a metadata-only catalog change with no
-- prod-table-rewrite risk (precedent: migrations 029, 043). 004 is left in
-- place as history. Idempotent via IF EXISTS.

DROP TABLE IF EXISTS webhook_signing_secrets;
