# Data Handling

What e2a stores, how long it lives, and what users + operators can do with it.

For vulnerability reporting and the security model, see [SECURITY.md](../SECURITY.md).

## What's stored

| Data | Where | Retention |
|---|---|---|
| Inbound + outbound message envelopes (sender, recipient, subject, conversation_id, timestamps) | Postgres `messages` | Default 10 days; `expires_at` per row, hourly cleanup worker. Chosen to exceed the 7-day HITL max TTL with a 3-day audit buffer. |
| Inbound message bodies (raw RFC822 in `raw_message`) | Postgres `messages` | Same 10-day default |
| Outbound message bodies (only while in `pending_approval`) | Postgres `messages.body_text` / `body_html` / `attachments_json` | **Scrubbed on terminal HITL transition** (approve/reject/expire) — only metadata persists after that |
| Attachments | Postgres rows (`attachments_json`, JSONB) | Same lifetime as the parent message — no S3/GCS |
| Agent + domain ownership records | Postgres `agent_identities`, `domains` | Until the user deletes the agent/domain or the account |
| API keys | Postgres `api_keys`, **hash only** (SHA over the plaintext) | Until revoked or the user is deleted; plaintext exists only in the create response and is never persisted |
| OAuth sessions | Postgres `user_sessions` | 30 days; cleanup worker removes expired rows hourly |
| Usage events / summaries (only when `E2A_USAGE_TRACKING=true`) | Postgres `usage_events`, `usage_summaries` | Indefinite by default — operator can purge or override |
| Per-user inbound-webhook signing secret | Postgres `webhook_signing_secrets`, **plaintext** (the relay needs the value to sign — hashing them like API keys would break HMAC). Auto-provisioned at signup; the relay falls back to the deployment-wide signer if a user has none. | Until the account is deleted. There is no per-user signing-secrets management API. |
| Per-webhook signing secret (`whsec_…`) | Postgres, **plaintext** | Until the webhook is deleted. Returned once at creation; rotate via `POST /v1/webhooks/{id}/rotate-secret` (the previous secret stays valid for a 24h grace window). |
| Deployment-wide HMAC signing secret (operator key / legacy fallback) | Operator's env (`E2A_HMAC_SECRET`); never written to DB | Lifetime of the deployment. The operator's deployment key; also the verify-side fallback when a user has no per-user secret. SDKs verify inbound deliveries with `E2A_WEBHOOK_SECRET`, not this. |

## What's logged

- The SMTP relay logs envelope metadata on every inbound message: sender address, recipient list, byte count, the SPF/DKIM verdict. This is standard MTA practice (Postfix and other relays log the same), but it does mean **PII (sender + recipient addresses) appears in application logs** and inherits whatever retention your log pipeline has. Operators in privacy-strict environments should plan for redaction in their log forwarder.
- HITL state transitions log message IDs and agent IDs but not bodies.
- Webhook delivery attempts log the destination URL and status code.

Application logs do **not** include message bodies, attachment contents, raw API keys, or HMAC secrets.

## User rights

The API exposes the two operations that GDPR Art. 15 / Art. 17 (and CCPA equivalents) require:

- **`GET /v1/account/export`** — returns a JSON dump of everything the authenticated account owns. Profile, agents, domains, API key metadata, all messages with bodies, usage events. Internal identifiers (Google subject, key hashes, session tokens) are excluded.
- **`DELETE /v1/account?confirm=DELETE`** — wipes the account and every related row in a single Postgres transaction (cascade through `agent_identities → messages → webhook_deliveries`, plus explicit deletion of `usage_events` which has `ON DELETE SET NULL` rather than CASCADE so it survives by default). Returns per-table row counts so the caller can audit what was removed.

Both are scoped to the authenticated user — there's no path to target someone else's data.

## Operator responsibilities

Things e2a doesn't (and can't) handle for you:

- **Database backups.** Take them, encrypt them, set retention policy. e2a doesn't ship a backup story; use whatever your Postgres provider gives you.
- **TLS termination** for the API and SMTP. Production mode enforces HTTPS for webhook delivery; the operator's reverse proxy / ingress terminates TLS for inbound API traffic and the SMTP relay's `tls_cert` / `tls_key` config covers `:2525`.
- **At-rest encryption.** Disk-level / volume-level encryption is the operator's responsibility (Postgres TDE, EBS encryption, GCP CMEK, …). e2a does not currently encrypt message bodies or attachments at the application layer; if your threat model includes a privileged DBA, you'll want to add column-level encryption.
- **Log redaction.** If your environment can't tolerate sender/recipient addresses in application logs, redact in your log forwarder or run with `--log-format=json` and filter the relevant fields downstream.
- **Compliance attestations** (SOC 2, HIPAA, ISO 27001) — those are deployment-level, not code-level.
