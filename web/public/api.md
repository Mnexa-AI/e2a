# api.md

The e2a REST API, for agents (and humans) calling it directly. If you're
connected over **MCP**, you don't need this — call `tools/list` for the exact
tool surface. This doc is for **non-MCP** clients hitting HTTP, and for
understanding the shapes the SDKs wrap.

The **authoritative, exhaustive contract** is the OpenAPI 3.1 document at
https://e2a.dev/openapi.yaml — every endpoint, field, enum, and error is there,
generated from the live handlers. This page is the orientation: base URL, auth,
conventions, and the resource map. When a signature here and the spec disagree,
the spec wins.

## Base URL & versioning

```
https://e2a.dev/v1/...
```

All resource endpoints live under `/v1`. Responses are JSON.

## Auth

Send a bearer token on every request:

```
Authorization: Bearer <token>
```

A token is either an **API key** (`e2a_acct_…` account scope, or `e2a_agt_…`
agent scope) or an **OAuth 2.1 access token**. Account scope can manage agents,
domains, keys, and the review queue; agent scope is pinned to one inbox and is
barred from account-wide operations. For self-registering agents (OAuth + Dynamic
Client Registration, no human-supplied secret), see https://e2a.dev/auth.md.

## Conventions

- **Pagination** — list endpoints take `?cursor=` and return `next_cursor`
  (null when exhausted). Walk it until null.
- **Errors** — non-2xx responses carry `{"error": {"code", "message",
  "request_id"}}`. Branch on the machine `code`, not the message.
- **Idempotency** — unsafe writes that send mail (`send`, `reply`, `forward`,
  approve) accept an `Idempotency-Key` header; a retried call with the same key
  replays the original result instead of double-sending.
- **IDs** — `{type}_{random}` (e.g. `msg_abc123`). An agent's id *is* its email
  address.

## Resource map

Exact request/response bodies are in [openapi.yaml](https://e2a.dev/openapi.yaml).

- **Inboxes** — `GET/POST /v1/agents`, `GET/PATCH/DELETE /v1/agents/{email}`,
  `POST /v1/agents/{email}/test`. Per-agent screening/HITL posture:
  `GET/PUT /v1/agents/{email}/protection`.
- **Messages** (per inbox, inbound + outbound) —
  `GET /v1/agents/{email}/messages` (filters: `direction`, `status`, cursor),
  `GET …/messages/{id}`, `GET …/messages/{id}/attachments/{index}`,
  `PATCH …/messages/{id}` (labels / read), and the send family:
  `POST …/messages` (send), `POST …/messages/{id}/reply`,
  `POST …/messages/{id}/forward`.
- **Reviews (HITL)** — the account-scoped hold queue:
  `GET /v1/reviews`, `GET /v1/reviews/{id}`,
  `POST /v1/reviews/{id}/approve`, `POST /v1/reviews/{id}/reject`. Holds are both
  outbound drafts awaiting send and inbound messages held by a screening gate.
- **Domains** — `GET/POST /v1/domains`, `GET/DELETE /v1/domains/{domain}`,
  `POST /v1/domains/{domain}/verify`. Carries `verified` (inbound ownership) and
  `sending_status` (outbound SES identity) independently.
- **Webhooks** — `GET/POST /v1/webhooks`, `GET/PATCH/DELETE /v1/webhooks/{id}`,
  `POST /v1/webhooks/{id}/rotate-secret`, `GET /v1/webhooks/{id}/deliveries`.
  Each webhook has its own `whsec_…` signing secret (returned once at create).
- **Events** — `GET /v1/events`, `GET /v1/events/{id}`,
  `POST /v1/events/{id}/redeliver`. The durable, queryable log.
- **Account** — `GET /v1/account` (whoami), `/v1/account/api-keys`,
  `/v1/account/suppressions`, `/v1/account/export`.

## Key flows

- **Send** — `POST /v1/agents/{email}/messages` with `{to, subject, body}`.
  Response is `sent` *or* `pending_review` if a HITL gate held it — that's not an
  error; it resolves when a human approves it.
- **Receive** — subscribe a webhook to `email.received`; the delivery's
  `data.message_id` + `data.recipient` let you `GET …/messages/{id}` for the
  parsed body. (Or poll `GET …/messages?direction=inbound&status=unread`.)
- **Reply (threaded)** — `POST …/messages/{id}/reply`; e2a keeps the
  `conversation_id` so the recipient sees a normal threaded reply.

## Webhook payloads

Every webhook delivery is `{type, id, created_at, data}` — the same shape as a
`GET /v1/events/{id}` object. Verify the `X-E2A-Signature` header against the
webhook's `whsec_` secret (the SDKs' `construct_event` / `constructEvent` do
parse + verify in one call). The inbound `email.received` `data` carries
`message_id`, `from`, `to`, `subject`, and the raw message (base64).

## SDKs

Prefer a typed client over raw HTTP — see https://e2a.dev/sdk.md
(TypeScript + Python).
