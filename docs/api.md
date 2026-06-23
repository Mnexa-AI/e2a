# API Reference

All endpoints are under `/v1` unless noted. Auth is `Authorization: Bearer <api_key>` except where called out. Path parameters containing `@` (agent emails) must be URL-encoded.

> **Note:** the canonical, authoritative `/v1` surface is the generated OpenAPI spec at [`api/openapi.yaml`](../api/openapi.yaml). This hand-written overview is a convenience summary; if anything here disagrees with the spec, the spec wins.

For the machine-readable spec, see [`api/openapi.yaml`](../api/openapi.yaml).

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
| `POST` | `/agents` | Register an agent. Body `{email, name?}`. The `email` must be on a domain you've verified, or on the deployment's configured `shared_domain` (see `/v1/info` → `slug_registration_enabled`). |
| `GET` | `/agents` | List agents owned by the authenticated user |
| `GET` | `/agents/{address}` | Get agent details |
| `PATCH` | `/agents/{address}` | Update an agent's display name. Screening/protection config (trust gate, scan, holds) lives on `/agents/{address}/protection`. |
| `DELETE` | `/agents/{address}` | Delete an agent |
| `POST` | `/agents/{address}/test` | Send a test email to the agent's own address |

## Messages (per-agent)

The message surface is agent-scoped: every message endpoint hangs off `/agents/{address}/messages`. Sending is `POST` to the collection (the agent in the path is the sender — there is no `from` field); `reply`, `forward`, `approve`, and `reject` are sub-resources of a single message.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents/{address}/messages` | List the agent's messages. Filters: `direction` (`inbound` default \| `outbound` \| `all`), `read_status` (inbound only — `unread`\|`read`\|`all`), `sort`, `from`, `subject_contains`, `conversation_id`, `labels`, `since`, `until`, `cursor`, `limit` (max 100, default 50). Returns `{items, next_cursor}`. Held outbound drafts appear as `status=pending_review`. |
| `POST` | `/agents/{address}/messages` | Send a new email from the agent (a new thread). Body `{to, subject, body, html_body?, cc?, bcc?}` — no `from`. Held with `202 Accepted` + `status=pending_review` when the agent's outbound policy or content scan holds it for review. |
| `GET` | `/agents/{address}/messages/{id}` | Fetch a single message (inbound or outbound). Side effect: any `unread` inbound row flips to `read` on read. |
| `POST` | `/agents/{address}/messages/{id}/reply` | Reply to an inbound message |
| `POST` | `/agents/{address}/messages/{id}/forward` | Forward a message to new recipients |
| `POST` | `/agents/{address}/messages/{id}/approve` | Approve a `pending_review` message. Branches on direction: outbound → sent; inbound → released to the inbox. Account scope only. |
| `POST` | `/agents/{address}/messages/{id}/reject` | Reject a `pending_review` message (outbound → discarded; inbound → dropped). Account scope only. |

## Account (data rights)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/account/export` | Returns a JSON dump of the authenticated account's profile, agents, domains, API key metadata, messages, suppressions, protection events (screening audit log), and usage events. Right-of-access export (GDPR Art. 15 / CCPA equivalent). |
| `DELETE` | `/account?confirm=DELETE` | Permanently deletes the authenticated account and all associated data in one Postgres transaction. Right-of-deletion (GDPR Art. 17 / CCPA "Do Not Sell or Share"). Requires `confirm=DELETE` query parameter as a guardrail; returns per-table row counts so the caller can audit the cascade. |

Both endpoints require a valid API key or session. The export omits internal identifiers (Google subject, API key hashes, session tokens) — see [data-handling.md](data-handling.md) for the full data model.

### Webhook signing secrets

A per-user HMAC secret signs inbound webhook payloads; one is auto-provisioned at
signup, and the relay falls back to the deployment-wide signer if a user has none.
The standalone per-user management API (`/users/me/signing-secrets`) was retired in
the v1 cutover — webhooks-as-a-resource now carry their own per-webhook secret,
rotatable via `POST /v1/webhooks/{id}/rotate-secret`.

Pass your webhook's signing secret to the SDK helper — `construct_event(body, header, secret)` / `constructEvent(body, header, secret)` does parse + verify in one call. See [sdks/python/README.md](../sdks/python/README.md#quick-start) and [sdks/typescript/README.md](../sdks/typescript/README.md#webhook-cloud-agents).

## HITL magic links

These endpoints accept a signed `t` query parameter (from notification emails) instead of an API key, so reviewers can approve from any mail client without auth.

| Method | Path | Description |
|--------|------|-------------|
| `GET`/`POST` | `/v1/approve?t=…` | Approve a pending message via signed token |
| `GET`/`POST` | `/v1/reject?t=…` | Reject a pending message via signed token |

These moved under the `/v1` prefix in the cutover (previously root-level `/approve`·`/reject`); the paths are shown in full here because they are the literal links embedded in notification emails.

## Real-time delivery

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents/{address}/ws` | WebSocket for real-time inbound. Auth via the `Authorization: Bearer <api_key>` handshake header (the credential never appears in the URL). |

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

Fetch full content via `GET /agents/{address}/messages/{id}`. The full payload includes the parsed `to` (list), `cc` (list), and `reply_to` (list) headers from the original message; the lightweight notification omits them since the agent fetches the body anyway. `reply_to` is the addresses the sender wants replies sent to — useful when `from` is a no-reply notifications mailbox; empty list when the header was absent (the server never silently falls back to `from`). On connect, all unread messages are drained as notifications automatically.

## Other

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/health` | none | Health check |
| `GET` | `/v1/info` | none | Deployment discovery — returns `shared_domain`, `slug_registration_enabled`, and `public_url`. CLIs/SDKs hit this to self-configure from a single base URL. |
| `POST` | `/api/feedback` | none | Submit feedback (rate-limited per-IP) |
