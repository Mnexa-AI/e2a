# API Reference

All endpoints are under `/v1` unless noted. Auth is `Authorization: Bearer <api_key>` except where called out. Path parameters containing `@` (agent emails) must be URL-encoded.

> **Note:** the canonical, authoritative `/v1` surface is the generated OpenAPI spec at [`api/openapi.yaml`](../api/openapi.yaml). This hand-written overview predates the v1 redesign and still lists some pre-redesign flat paths (e.g. `/send`, `/messages/{id}`) that are now agent-scoped (`/v1/agents/{address}/messages…`); a full rewrite is tracked separately. Use the OpenAPI spec when in doubt.

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
| `GET` | `/agents/{email}/messages/{id}` | Fetch a single inbound message. Side effect: any `unread` row flips to `read` on read, regardless of agent mode. |
| `POST` | `/agents/{email}/messages/{id}/reply` | Reply to an inbound message |

## Messages — outbound / HITL

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/send` | Send an email (held with `202 Accepted` if HITL enabled on the agent) |
| `GET` | `/messages` | List outbound messages owned by the user. Only `status=pending_approval` is accepted (the default); any other value returns `400`. |
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

A per-user HMAC secret signs inbound webhook payloads; one is auto-provisioned at
signup, and the relay falls back to the deployment-wide signer if a user has none.
The standalone per-user management API (`/users/me/signing-secrets`) was retired in
the v1 cutover — webhooks-as-a-resource now carry their own per-webhook secret,
rotatable via `POST /v1/webhooks/{id}/rotate-secret`.

The SDKs read the secret from `E2A_WEBHOOK_SECRET` by default (with `E2A_HMAC_SECRET` accepted as a deprecated alias for SDK 2.0 users) — `client.parse_webhook(body)` / `client.parseWebhook(body)` does parse + verify in one call. See [sdks/python/README.md](../sdks/python/README.md#quick-start) and [sdks/typescript/README.md](../sdks/typescript/README.md#webhook-cloud-agents).

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

Fetch full content via `GET /agents/{email}/messages/{id}`. The full payload includes the parsed `to` (list), `cc` (list), and `reply_to` (list) headers from the original message; the lightweight notification omits them since the agent fetches the body anyway. `reply_to` is the addresses the sender wants replies sent to — useful when `from` is a no-reply notifications mailbox; empty list when the header was absent (the server never silently falls back to `from`). On connect, all unread messages are drained as notifications automatically.

## Other

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/health` | none | Health check |
| `GET` | `/v1/info` | none | Deployment discovery — returns `shared_domain`, `slug_registration_enabled`, and `public_url`. CLIs/SDKs hit this to self-configure from a single base URL. |
| `POST` | `/api/feedback` | none | Submit feedback (rate-limited per-IP) |
