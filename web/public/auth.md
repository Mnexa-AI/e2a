# auth.md

You are an agent that wants to use e2a — the authenticated email gateway for AI agents. This file describes how to obtain credentials today, how to handle them safely, and where the protocol is going.

Two hosts are relevant:

- **API** — `https://api.e2a.dev` — the resource server you will call (`/v1/...`, MCP at `/mcp`).
- **Dashboard & docs** — `https://e2a.dev` — where the user manages agents, domains, API keys, and billing, and where these `.md` docs live.

## Current state

e2a already implements the OAuth 2.1 surface that MCP clients depend on: RFC 8414 authorization-server metadata at [`/.well-known/oauth-authorization-server`](https://api.e2a.dev/.well-known/oauth-authorization-server), RFC 7591 Dynamic Client Registration at `/oauth2/register` (rate-limited per IP), `authorization_code` + `refresh_token` grants with PKCE S256, RFC 7009 revocation, and RFC 6750 Bearer challenges on 401s. MCP clients can register and onboard without any human-supplied secret — the user only sees a browser consent screen.

What e2a does **not** yet implement is the WorkOS [auth.md](https://github.com/workos/auth.md) flow specifically: there is no `/agent/auth` endpoint, no `agent_auth` block in the AS metadata, no RFC 9728 protected-resource metadata document, no ID-JAG verification, and no email-OTP claim ceremony. See [Agent identity](#agent-identity) for where we're heading — because every e2a agent has an end-to-end-verified email address, e2a is positioned to act as an identity provider in this protocol, and that's the direction we're building.

## Use the existing tooling first

Before you do anything credential-shaped, check whether the user has already wired e2a into your environment. Most of the time they have, and you should not be handling credentials (API keys or OAuth tokens) yourself.

1. **e2a MCP server** — if you are an MCP client (Claude, Cursor, Codex, ChatGPT, etc.), e2a ships an official MCP server (`@e2a/mcp-server`) that exposes inbox, send, reply, agents, domains, and HITL approval as tools. The user configures it once with their key; you call the tools without ever seeing the key. Package: `https://www.npmjs.com/package/@e2a/mcp-server`. Repository: `https://github.com/Mnexa-AI/e2a/tree/main/mcp`.
2. **e2a agent skill** — a guidance document covering inbox patterns, idempotency, HITL approval, attachment handling, and webhook verification. Load it before calling the API directly so you don't relearn the gotchas on the user's dime.
3. **e2a CLI** — if you are running shell commands, prefer `e2a` over hand-rolled `curl`. It handles auth resolution, retries, and non-interactive flags. Install: `npm i -g @e2a/cli`. Repository: `https://github.com/Mnexa-AI/e2a/tree/main/cli`.

If any of these is already configured, use it and stop. Do not ask the user for an API key you do not need.

## Credentials

e2a accepts two credential shapes at every `/v1/...` endpoint, dispatched by token prefix:

- **OAuth access token** (`ate2a_…`, refresh `rte2a_…`) — issued by the e2a OAuth server to a registered client after the user consents in a browser. Use this if you are an MCP client.
- **API key** (`e2a_acct_…` for account scope, `e2a_agt_…` for a single-agent scope) — issued by the user directly from the dashboard, supplied to your environment out of band. Use this if you are a CLI, script, server-side integration, or direct API consumer.

Both are presented as `Authorization: Bearer <credential>`.

### Path A — MCP client via OAuth DCR

If you are an MCP client, you do not need an API key. Run the standard discovery + DCR + authorization-code flow:

1. **Discover** — `GET https://api.e2a.dev/.well-known/oauth-authorization-server`. Read `registration_endpoint`, `authorization_endpoint`, `token_endpoint`. Scope to request: `mcp`.
2. **Register** — `POST` your client metadata to `registration_endpoint` (RFC 7591). You'll receive a `client_id`. Token endpoint auth method is `none` — you are a public client.
3. **Authorize** — redirect the user to `authorization_endpoint` with `response_type=code`, your `client_id`, `redirect_uri`, `scope=mcp`, and PKCE S256 (`code_challenge`, `code_challenge_method=S256`). The user logs in to e2a and consents.
4. **Token exchange** — `POST` `code` + `code_verifier` to `token_endpoint`. You receive `access_token` (prefix `ate2a_…`) and `refresh_token`.
5. **Use** — present the access token as a bearer; refresh with the refresh token before `expires_in`.

Access tokens carry the user identity that consented to your client; every `/v1/...` call is scoped to that user.

### Path B — Direct API consumer via API key

The user issues an API key from the e2a dashboard and supplies it to you through a secure channel — never by pasting it into chat.

#### How to pick the key up

Look for it in this order. Stop at the first one that exists:

1. `E2A_API_KEY` in your process environment.
2. A project `.env` file the user has told you to read.
3. The user's CLI config at `~/.e2a/config.json` (populated via `e2a login`; used automatically when you invoke the `e2a` CLI).

If you're invoking the e2a MCP server, you don't pick the key up at all — the server reads `E2A_API_KEY` from its own environment (set in the MCP client's `env` block) and you call tools through it.

If none of the above is set and you genuinely need a key, **do not ask the user to paste it into the conversation**. Instead, tell them to:

- Create one in the e2a dashboard.
- Put it in `E2A_API_KEY` in their shell, `.env`, MCP client config, or run `e2a login` to populate `~/.e2a/config.json` — whichever matches how they invoke you.
- Resume the task once it is set.

This keeps the key out of your transcript, out of any logs the user shares, and out of the model provider's training data.

API keys do not expire on their own. Treat a `401` on a previously-working key as revocation: drop it from memory and ask the user to refresh whichever source you read it from.

### How to use the credential

Whether `access_token` or `api_key`, present it as a bearer token. The message surface is agent-scoped — the sending agent is in the path (URL-encode the `@`), so there is no `from` field in the body. Example send:

```http
POST /v1/agents/bot%40agents.e2a.dev/messages HTTP/1.1
Host: api.e2a.dev
Authorization: Bearer $CREDENTIAL
Content-Type: application/json
Idempotency-Key: <UUIDv4>

{
  "to": ["alice@example.com"],
  "subject": "Hello from your agent",
  "body": "Plain-text body. Required.",
  "html_body": "<p>Optional HTML alternative.</p>"
}
```

The plain-text body field is `body` (required). The HTML alternative is `html_body` (optional). There is no `text` field.

Read the credential from the environment at the moment of the call. Do not copy it into variables you log, do not echo it back to the user, do not include it in commit messages, PR descriptions, error reports, or screenshots. If you are running a shell command, never interpolate the credential inline — reference the environment variable so it does not appear in command history.

Set an `Idempotency-Key` (UUIDv4 recommended) per logical operation on side-effectful calls (sends, replies, HITL approve). Reuse the **same** key on transport retries (network failures, timeouts) — the server replays the original response. Same key with a different body returns `422`; a genuinely new operation needs a fresh key.

### HITL: handling 202 pending_review

If the agent's protection config holds outbound mail for review, a send (and `reply`/`forward`) will return **`202 Accepted`** with `status: "pending_review"` instead of dispatching the message:

```json
{
  "message_id": "msg_abc123",
  "status": "pending_review",
  "approval_expires_at": "2026-05-28T13:00:00Z"
}
```

The message is held until a human approves it via the dashboard, CLI, or magic link, or until `approval_expires_at` fires the configured expiration action. Do not retry the send — that would queue a duplicate. To learn the outcome, poll `GET /v1/agents/{address}/messages/{id}`: an approved outbound send becomes `sent`; a rejection becomes `review_rejected`; on TTL expiry it becomes `review_expired_approved` (auto-sent) or `review_expired_rejected` (discarded), per the hold's `on_expiry` action. Or surface the situation to the calling user and stop.

### Errors

| Status | Where | Meaning | What to do |
| --- | --- | --- | --- |
| `400` | send, reply | Missing `subject` or `body`; malformed recipient; CRLF in subject. | Fix the payload before retrying. |
| `401` first use | any | Credential missing, malformed, revoked, or for a different environment. | Ask the user to confirm the value in their `E2A_API_KEY` / config is current and active in the dashboard. MCP clients should restart at discovery. |
| `401` on previously-working credential | any | Revoked or rotated. | Drop the cached value. API-key consumers re-read from the same source you loaded it from. MCP clients refresh, then re-run the authorization-code flow if refresh fails. |
| `403` | send, reply | Agent's sending domain is not verified. | Ask the user to register and verify the domain in the dashboard (`POST /v1/domains` then `POST /v1/domains/{domain}/verify`). |
| `409` | send, reply, approve | An in-flight request with this `Idempotency-Key` is still being processed, or the message is no longer in the expected state. | Wait and re-poll `GET /v1/agents/{address}/messages/{id}`. |
| `422` | send, reply | `Idempotency-Key` reused with a different body. | Mint a fresh key for the new payload. |
| `429` | any | Rate limited (60 sends/agent/minute; 200 agent registrations/IP/hour on `/v1/agents`). | Back off; honor `Retry-After` (delay-seconds form). |

The `WWW-Authenticate` header on 401 responses tells you whether the failing credential was an OAuth token (carries RFC 6750 §3.1 `error="invalid_token"` params) or an API key (bare `Bearer realm="e2a"`). MCP clients should branch on this.

## Agent identity

This section describes e2a's bet on where agent auth is heading. It does not describe shipped surface — only direction. If you are implementing today, use the credential paths above; come back here when you are building for the protocol's next phase.

Every e2a agent has a stable, verified email address. The owner proved control of the domain (via DNS records and a verification token), and e2a enforces SPF and DKIM on every inbound message routed to that agent. Equivalently for agents on the shared `agents.e2a.dev` domain, e2a is the authoritative issuer. **The agent's email is not a label — it's an identity claim e2a stands behind.**

We are building two pieces on top of this:

### e2a as an identity provider (planned)

e2a already operates as an OAuth issuer at `https://api.e2a.dev` (see AS metadata above). The remaining work is publishing a JSON Web Key Set at `https://api.e2a.dev/.well-known/jwks.json` and issuing audience-bound [ID-JAG](https://datatracker.ietf.org/doc/draft-ietf-oauth-identity-assertion-authz-grant/) assertions (`urn:ietf:params:oauth:token-type:id-jag`) with:

- `iss` = `https://api.e2a.dev`
- `sub` = the agent's verified email
- `email` / `email_verified: true`
- `aud` = the third-party service the agent is registering with
- Short `exp` (≤5 minutes), fresh `jti`

Any auth.md-implementing service that adds e2a to its trust list will be able to onboard an e2a agent without an OTP ceremony — the agent's e2a identity vouches for it. e2a's contribution is an identity rooted in a verifiable email address, stable across agent runtimes.

If you operate an agent service and want to accept e2a-issued assertions, watch for `iss: https://api.e2a.dev` to land in the WorkOS reference trust list, or open an issue at `https://github.com/Mnexa-AI/e2a/issues` to pre-register.

### Email-loop claim completion (proposed)

The WorkOS auth.md OTP ceremony assumes a human reads a 6-digit code back to the agent. For agents that have an e2a inbox, we are prototyping an inbox-driven completion:

1. The third-party service sends a single-click approval mail to the user.
2. The user clicks "confirm".
3. The service emails a confirmation to the agent's e2a inbox.
4. The agent receives the confirmation via WebSocket or webhook and posts to `/agent/auth/claim/complete`.

No code reading, no copy-paste, no transcript leakage. This is uniquely possible for e2a because the agent's mailbox is part of the product. We will publish a flow extension when the prototype is stable; if you are building an auth.md service and want to support this from day one, open an issue at `https://github.com/Mnexa-AI/e2a/issues`.

## Discovery

What e2a publishes today:

- **RFC 8414 authorization-server metadata** at [`https://api.e2a.dev/.well-known/oauth-authorization-server`](https://api.e2a.dev/.well-known/oauth-authorization-server) — advertises `authorization_endpoint`, `token_endpoint`, `registration_endpoint`, `revocation_endpoint`, supported grants (`authorization_code`, `refresh_token`), PKCE (`S256`), and the RFC 9207 `iss` parameter. Request the `mcp` scope.
- **RFC 6750 Bearer challenges** on every 401 from `/v1/...` — `WWW-Authenticate: Bearer realm="e2a"` for unknown/missing credentials, plus RFC 6750 §3.1 error params for OAuth-bearer failures.

What's missing for full auth.md compliance:

- An `agent_auth` block in the AS metadata describing `register_uri`, `claim_uri`, `revocation_uri`, `identity_types_supported`, etc.
- An RFC 9728 protected-resource metadata document at `/.well-known/oauth-protected-resource`, and `resource_metadata="..."` parameter on the WWW-Authenticate challenge so agents can auto-discover it.
- The `/agent/auth` endpoint itself, with `anonymous`, `identity_assertion + verified_email`, and `identity_assertion + id-jag` flows.
- A JSON Web Key Set at `/.well-known/jwks.json` for verifying e2a-issued ID-JAGs (see [Agent identity](#agent-identity)).

These will land together. When they do, this document will be updated and the AS metadata will carry the canonical machine-readable description.

## Revocation

The user revokes API keys in the e2a dashboard. OAuth access tokens are revoked via `POST /oauth2/revoke` (RFC 7009) or by the user disconnecting the client in the dashboard. Either way, you will discover revocation as a `401` on a previously-working credential — drop it and re-acquire from the same source you loaded it from. Once e2a issues ID-JAGs, providers will be able to POST logout tokens to a `revocation_uri` advertised in AS metadata; that is not in scope for the current credential paths.

## References

- WorkOS [auth.md protocol](https://workos.com/auth-md) — the open spec this document follows
- [github.com/workos/auth.md](https://github.com/workos/auth.md) — reference implementation
- e2a [OpenAPI contract](https://e2a.dev/openapi.yaml) — full reference for the endpoints above
