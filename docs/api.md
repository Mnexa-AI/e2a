# API Reference

All endpoints are under `/api/v1` unless noted. Auth is `Authorization: Bearer <api_key>` except where called out. Path parameters containing `@` (agent emails) must be URL-encoded.

For the machine-readable spec, see [`web/public/openapi.yaml`](../web/public/openapi.yaml).

## Domains

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/domains` | Register a custom domain. Returns required MX and TXT records. |
| `GET` | `/domains` | List domains owned by the authenticated user |
| `POST` | `/domains/{domain}/verify` | Verify ownership via TXT record |
| `DELETE` | `/domains/{domain}` | Delete (must delete all agents on the domain first) |

## Agents

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/agents` | Register an agent. Use `email` for a custom domain (must be verified) or `slug` for a shared-domain registration (only when the deployment has `shared_domain` configured) |
| `GET` | `/agents` | List agents owned by the authenticated user |
| `GET` | `/agents/{email}` | Get agent details |
| `PUT` | `/agents/{email}` | Update agent (webhook URL, mode, HITL settings) |
| `DELETE` | `/agents/{email}` | Delete an agent |
| `POST` | `/agents/{email}/test` | Send a test email through the agent |

## Messages — inbound (per-agent)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents/{email}/messages` | List inbound messages for the agent |
| `GET` | `/agents/{email}/messages/{id}` | Fetch a single inbound message (transitions `unread` → `read` for local-mode agents) |
| `POST` | `/agents/{email}/messages/{id}/reply` | Reply to an inbound message |

## Messages — outbound / HITL

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/send` | Send an email (held with `202 Accepted` if HITL enabled on the agent) |
| `GET` | `/messages` | List outbound messages owned by the user (filterable by status) |
| `GET` | `/messages/{id}` | Get a single outbound message |
| `POST` | `/messages/{id}/approve` | Approve a `pending_approval` message |
| `POST` | `/messages/{id}/reject` | Reject a `pending_approval` message |

## User (data rights)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/users/me/export` | Returns a JSON dump of the authenticated user's profile, agents, domains, API key metadata, messages, and usage events. Right-of-access export (GDPR Art. 15 / CCPA equivalent). |
| `DELETE` | `/users/me?confirm=DELETE` | Permanently deletes the authenticated user and all associated data in one Postgres transaction. Right-of-deletion (GDPR Art. 17 / CCPA "Do Not Sell or Share"). Requires `confirm=DELETE` query parameter as a guardrail; returns per-table row counts so the caller can audit the cascade. |

Both endpoints require a valid API key or session. The export omits internal identifiers (Google subject, API key hashes, session tokens) — see [data-handling.md](data-handling.md) for the full data model.

### Webhook signing secrets

Per-user HMAC secrets used to sign inbound webhook payloads. Up to 5 active per user. Plaintext is returned **once** at creation; subsequent reads see only the 12-char prefix.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/users/me/signing-secrets` | List the user's signing secrets (id, name, prefix, created_at, last_signed_at). |
| `POST` | `/users/me/signing-secrets` | Create a new signing secret. Returns the plaintext exactly once — store it immediately. Body: `{"name": "..."}`. |
| `DELETE` | `/users/me/signing-secrets/{id}` | Delete a signing secret. Cannot delete the last one (returns 409); rotate by creating a new one first, switching consumers over, then deleting the old one. |

The SDKs read the secret from `E2A_HMAC_SECRET` by default — `client.parse_webhook(body)` / `client.parseWebhook(body)` does parse + verify in one call. See [sdks/python/README.md](../sdks/python/README.md#quick-start) and [sdks/typescript/README.md](../sdks/typescript/README.md#webhook-cloud-agents).

## HITL magic links

These endpoints accept a signed `token` query parameter (from notification emails) instead of an API key, so reviewers can approve from any mail client without auth.

| Method | Path | Description |
|--------|------|-------------|
| `GET`/`POST` | `/approve?token=…` | Approve a pending message via signed token |
| `GET`/`POST` | `/reject?token=…` | Reject a pending message via signed token |

## Real-time delivery

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents/{email}/ws?token={api_key}` | WebSocket for local-mode agents. Auth via query param (WebSocket clients can't set headers during upgrade). |

The server pushes lightweight JSON notifications (metadata only):

```json
{
  "message_id": "msg_abc123",
  "conversation_id": "conv_xyz",
  "from": "alice@example.com",
  "recipient": "bot@your-domain.com",
  "subject": "Meeting tomorrow",
  "received_at": "2026-04-24T10:00:00Z"
}
```

Fetch full content via `GET /agents/{email}/messages/{id}`. The full payload includes the parsed `to` (list) and `cc` (list) headers from the original message; the lightweight notification omits them since the agent fetches the body anyway. On connect, all unread messages are drained as notifications automatically.

## Other

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/health` | none | Health check |
| `GET` | `/api/v1/info` | none | Deployment discovery — returns `shared_domain`, `slug_registration_enabled`, and `public_url`. CLIs/SDKs hit this to self-configure from a single base URL. |
| `POST` | `/api/feedback` | none | Submit feedback (rate-limited per-IP) |
