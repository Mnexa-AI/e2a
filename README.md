# e2a — Email for AI agents

[![Tests](https://github.com/Mnexa-AI/e2a/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/Mnexa-AI/e2a/actions/workflows/test.yml)
[![Build image](https://github.com/Mnexa-AI/e2a/actions/workflows/build-image.yml/badge.svg?branch=main)](https://github.com/Mnexa-AI/e2a/actions/workflows/build-image.yml)
[![License](https://img.shields.io/github/license/Mnexa-AI/e2a)](LICENSE)
[![npm @e2a/sdk](https://img.shields.io/npm/v/%40e2a%2Fsdk?label=%40e2a%2Fsdk)](https://www.npmjs.com/package/@e2a/sdk)
[![PyPI e2a](https://img.shields.io/pypi/v/e2a)](https://pypi.org/project/e2a/)

<a href="https://www.producthunt.com/products/e2a-open-source-email-api-for-agents?embed=true&utm_source=badge-featured&utm_medium=badge&utm_campaign=badge-e2a-open-source-email-api-for-agents" target="_blank" rel="noopener noreferrer"><img alt="e2a – open-source email API for agents - Give your AI agents a real, authenticated email address. | Product Hunt" width="250" height="54" src="https://api.producthunt.com/widgets/embed-image/v1/featured.svg?post_id=1145559&theme=light&t=1778615217650"></a>

Authenticated email gateway for AI agents. Receive emails as webhooks or via WebSocket, send emails through an HTTP API, and verify the identity of every sender — humans and other agents alike.

- **Authenticated transport** — SPF/DKIM verified on inbound; HMAC-signed `X-E2A-Auth-*` headers on every delivery
- **Two delivery channels** — webhook (cloud agents) or WebSocket (local agents, no public URL needed)
- **Outbound API** — agents send to other agents (SMTP relay) or humans (upstream SMTP, e.g. SES, Resend)
- **Human in the loop** — opt-in approval gate that holds outbound mail until a reviewer approves via dashboard, magic-link email, the MCP tools, or the API
- **CLI + SDKs** — TypeScript and Python SDKs, plus a `e2a` CLI for everyday agent ops

<video src="https://github.com/user-attachments/assets/b55a8f18-6470-44e3-a053-97dfb787228c" controls autoplay muted loop width="800"></video>

## Use it

You can either use the hosted instance or self-host.

- **Hosted** — sign up at [e2a.dev](https://e2a.dev). Includes the shared `agents.e2a.dev` domain for instant slug-based onboarding (no DNS setup), a dashboard, and managed deliverability.
- **Self-host** — see [Quickstart](#quickstart) and [Deployment](#deployment). Every feature works the same; the shared-domain slug shortcut just needs you to point a mail domain at your relay and set `shared_domain` in `config.yaml`.

## How it works

```
Human (Gmail/Outlook)
    │
    ▼ SMTP
┌──────────────┐
│   e2a relay   │  ← MX record for your agent domain points here
│              │
│  1. Verify   │  ← SPF/DKIM check on the inbound message
│  2. Sign     │  ← HMAC-signed X-E2A-Auth-* headers
│  3. Deliver  │
└──────────────┘
    │
    ├──▶ Cloud-mode agent: HTTPS webhook POST
    │
    └──▶ Local-mode agent: store + WebSocket notification
              │
              ▼
         e2a listen (CLI) or client.listen() (SDK)
```

Inbound flow: SMTP → SPF/DKIM check → agent lookup → HMAC-sign auth headers → webhook or WebSocket delivery.

Outbound flow: API call → optional HITL hold → SMTP relay (agent-to-agent) or upstream SMTP (agent-to-human).

## Quickstart

Requires Docker.

```bash
git clone https://github.com/Mnexa-AI/e2a.git
cd e2a
docker compose up -d
```

Postgres comes up first (migrations run automatically), then the API server, then the dashboard. Three host ports:

- `:8080` — HTTP API
- `:2525` — SMTP relay
- `:3000` — Dashboard (Caddy + Next.js, proxies `/api/*` to the API server)

Health check:

```bash
curl http://localhost:8080/api/health
# {"status":"ok"}
```

Open `http://localhost:3000` in a browser to view the dashboard. Sign-in requires Google OAuth credentials configured in `config.yaml`; for an API-only smoke test you can skip the dashboard and use the bootstrap flow below.

Create your first user and API key (no OAuth required):

```bash
docker compose exec e2a e2a -config /etc/e2a/config.yaml -bootstrap-email you@example.com
# User:    you@example.com (id=...)
# API key: e2a_...
```

Save the key — it's only shown once. Register an agent and confirm it works:

```bash
KEY=e2a_...
curl -X POST http://localhost:8080/v1/agents \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"email":"my-bot@agents.e2a.dev"}'   # an email on the deployment shared domain (or a domain you've verified)

curl -H "Authorization: Bearer $KEY" http://localhost:8080/v1/agents
```

To receive real inbound mail, point a domain's MX record at your relay host:

- **A**: `your-domain.com` → server IP
- **MX**: `your-domain.com` → `your-domain.com` (priority 10)

Then register and verify the domain through the API (see [Domains](#domains)). Without DNS, the API still works for testing — but external email won't reach your relay.

> **Upgrades and migrations.** The compose file mounts `migrations/` into Postgres' init directory, which only runs on first start (when the data volume is empty). When you upgrade e2a and pull a new schema migration, you must apply it manually:
> ```bash
> docker compose exec postgres sh -c \
>   'for f in /docker-entrypoint-initdb.d/*.sql; do psql -U e2a -d e2a -f "$f" -v ON_ERROR_STOP=1; done'
> ```
> The migration files are idempotent (`CREATE TABLE IF NOT EXISTS`, `ALTER TABLE … ADD COLUMN IF NOT EXISTS`) so re-running them is safe.

## Concepts

### Delivery channels

Inbound mail reaches you two complementary ways — chosen per integration, not set on the agent:

| Channel | How | Public URL needed? |
|---------|-----|---------------------|
| **Webhooks** | Account-level subscriptions (`POST /v1/webhooks`) — HTTPS POST per event, filterable by agent / conversation / event type | Yes |
| **WebSocket** | Per-agent real-time notification stream (`/v1/agents/{address}/ws`) + REST fetch | No |

A disconnected WebSocket client accumulates "unread" messages; on reconnect, the server drains them as notifications. Either channel can also poll messages via the REST API. Webhooks are their own resource (`/v1/webhooks`), chosen per integration rather than set on the agent.

### Auth headers

Every email delivered through e2a (webhook or WebSocket-fetched) carries signed headers:

| Header | Description |
|--------|-------------|
| `X-E2A-Auth-Verified` | `true` if domain-level auth (SPF or DKIM) passed |
| `X-E2A-Auth-Sender` | Verified sender email or agent domain |
| `X-E2A-Auth-Entity-Type` | `human` or `agent` |
| `X-E2A-Auth-Domain-Check` | SPF/DKIM result string (e.g. `spf=pass; dkim=none`) |
| `X-E2A-Auth-Delegation` | `agent={id};human={id}` if an active delegation binding exists |
| `X-E2A-Auth-Timestamp` | RFC3339 timestamp |
| `X-E2A-Auth-Message-Id` | Internal e2a message ID this delivery is for |
| `X-E2A-Auth-Body-Hash` | Hex SHA-256 of the raw message bytes |
| `X-E2A-Auth-Signature` | HMAC-SHA256 over a canonical string of the above |

The signature covers:

```
verified \n sender \n entity_type \n domain_check \n delegation \n timestamp \n message_id \n body_hash
```

The MAC binds to **both** `message_id` and a SHA-256 of the raw message body. Substituting either invalidates the signature, so an attacker who captures one delivery cannot replay the auth claim on a different message or under a modified body.

#### Verifying the signature

The `X-E2A-Auth-Verified` field is the *server's claim* — anyone who can reach your webhook URL can set it. To make a security decision, **verify the signature** with your webhook's signing secret. The secret (a `whsec_…` value) is returned **once** when you create the subscription (`POST /v1/webhooks`); store it then. Rotate via `POST /v1/webhooks/{id}/rotate-secret` (24h grace window where the old secret still verifies).

The one-call shortcut parses **and** verifies a delivery, returning a typed event — use it instead of trusting any field on an unverified payload:

```python
from e2a.v1 import construct_event, E2AWebhookSignatureError

# raw request body + the X-E2A-Signature header + your whsec_… secret
event = construct_event(request_body, signature_header, webhook_secret)  # raises on bad signature
if event.type == "email.received":
    msg = event.data   # the message payload
```

```typescript
import { constructEvent, E2AWebhookSignatureError } from "@e2a/sdk/v1";

const event = constructEvent(req.body, req.header("X-E2A-Signature")!, webhookSecret); // throws on bad signature
if (event.type === "email.received") {
  const msg = event.data; // the message payload
}
```

`construct_event` / `constructEvent` checks that the HMAC matches the canonical signing string and the timestamp is within a 5-minute replay window. Pass an array of secrets to accept either during a rotation: `constructEvent(body, header, [oldSecret, newSecret])`.

Messages fetched over an authenticated channel — `client.messages.get(address, id)` or the `client.listen(...)` stream — are already trusted (the bearer token authenticated the call), so no verify step is needed there.

### Conversation threading

Both `send` and `reply` accept an opaque `conversation_id`. e2a propagates it to the recipient on delivery via `payload.conversation_id`, surfaced in this priority order:

1. **`X-E2A-Conversation-Id` header** — authoritative for e2a-to-e2a traffic. Only honored when the SMTP envelope `MAIL FROM` originates from this relay, so external senders cannot forge it.
2. **`In-Reply-To` / `References` lookup** — standard RFC 5322 threading, scoped to the recipient agent's own messages. Covers humans replying from Gmail/Outlook.

First contact from a human arrives with `conversation_id: null` — the agent should assign a new id before replying.

### Human in the loop (HITL)

When an agent's protection config holds an outbound message for review, `send` and `reply` calls do **not** dispatch immediately. The message is stored with status `pending_review` and the API returns HTTP `202 Accepted`. A reviewer must approve it before delivery; otherwise, after a configurable TTL, the protection config's `holds.on_expiry` decides the terminal: `approve` (the message just goes out, terminal status `sent` — for outbound, approving *is* sending) or `reject` (discard, `review_expired_rejected`). (Inbound messages can be held for review too — there, the auto-approve terminal is `review_expired_approved`, releasing the message to the inbox.)

Reviewers can approve or reject via:

- **Dashboard / API** — the account-scoped review queue `POST /v1/reviews/{id}/approve` or `/reject` (id-addressed, no inbox email needed; lists held items across all the account's inboxes via `GET /v1/reviews`). This is the primary path. The agent-path `POST /v1/agents/{address}/messages/{id}/approve|reject` is **deprecated** but still works identically for back-compat.
- **Magic-link email** — sent automatically when a hold fires; one-click `GET /v1/approve?t=…` and `/v1/reject?t=…` URLs (requires `E2A_PUBLIC_URL` and outbound SMTP configured)

Enable review holds on an agent via `PUT /v1/agents/{address}/protection`: set the outbound gate action to `review` (or turn on the content scan), plus the hold TTL (`holds.ttl_seconds`) and its expiry behavior (`holds.on_expiry` = `approve` or `reject`). Posture lives entirely on the protection sub-resource.

## API

All endpoints are under `/v1` unless noted. Auth is `Authorization: Bearer <api_key>` except for `/api/health`, `/v1/info`, `/api/feedback`, and the HITL magic-link routes. Path parameters containing `@` (agent emails) must be URL-encoded.

The surface covers domain registration + verification, agent CRUD, inbound/outbound messages, HITL approve/reject (API key or signed magic-link token), GDPR-style export and deletion, and a WebSocket channel for real-time inbound delivery.

See [docs/api.md](docs/api.md) for the full endpoint reference, or [`api/openapi.yaml`](api/openapi.yaml) for the machine-readable spec.

## CLI

```bash
npm install -g @e2a/cli
e2a login
```

The CLI is a thin developer convenience — it covers only what the other surfaces
don't do ergonomically. Drive agents (read/send/reply/list/labels) over the **MCP
tools** or the **SDKs**; manage domains/agents/webhooks/keys/HITL in the **web
dashboard**.

| Command | Description |
|---------|-------------|
| `e2a login` | Open a browser login and save your API key + default agent to `~/.e2a/config.json` |
| `e2a listen --agent <email>` | Stream inbound email for an agent over WebSocket (real-time; `--json` for raw, `--forward <url>` to bridge to a local HTTP handler) |
| `e2a config [list\|get\|set]` | View or update the local config |

The `listen --forward` mode also supports OpenAI Responses API forwarding via `--forward-token`, which formats each inbound email as a Responses payload and auto-replies with the model's output:

```bash
e2a listen --forward http://localhost:18789/v1/responses --forward-token <token>
```

See [cli/README.md](cli/README.md) for full reference.

## SDKs

### Python

```bash
pip install e2a            # webhook mode
pip install 'e2a[ws]'      # adds WebSocket support
```

```python
from e2a.v1 import E2AClient, construct_event

client = E2AClient()                                       # reads E2A_API_KEY
event = construct_event(request_body, signature_header, webhook_secret)  # parse + HMAC-verify
if event.type == "email.received":
    msg = event.data
    await client.messages.reply(msg["recipient"], msg["message_id"],
                                {"body": "Got it!", "conversation_id": "conv_123"})
```

WebSocket (local agents):

```python
from e2a.v1 import E2AClient

async with E2AClient(api_key="e2a_…") as client:
    async for notif in client.listen("bot@your-domain.com"):  # falls back to E2A_AGENT_EMAIL
        # notif is lightweight metadata — fetch the body when you want it
        email = await client.messages.get(notif.recipient, notif.message_id)
        await client.messages.reply(notif.recipient, notif.message_id, {"body": "Got it!"})
```

See [sdks/python/README.md](sdks/python/README.md).

### TypeScript

```bash
npm install @e2a/sdk
```

See [sdks/typescript/README.md](sdks/typescript/README.md).

## Deployment

Three audiences each configure a different surface:

| Audience | What they configure | Where |
|---|---|---|
| **Server operator** — runs the Go backend | DB, signing key, SMTP, OAuth, optional shared domain | `config.yaml` + `E2A_*` env |
| **CLI / SDK user** — calls the API from their machine | Just the deployment URL (and login) | `E2A_URL` + `e2a login` |
| **Web dashboard deployer** — hosts the Next.js dashboard | Public site URL + branding | `NEXT_PUBLIC_*` build-time env |

The Go binary runs on any container host; storage is plain Postgres 14+; outbound mail goes through standard SMTP. Most workers coordinate via `SELECT … FOR UPDATE SKIP LOCKED`, so multi-replica is safe — the two real horizontal-scaling caveats are in-memory WebSocket fan-out and per-process rate limits.

See [docs/deployment.md](docs/deployment.md) for the full env-var reference, shared-domain DNS setup, and scaling/limitation notes.

## Security

- **Identity** — agent registration requires DNS TXT verification of domain ownership (custom domains)
- **Domain auth** — SPF and DKIM checked on every inbound message
- **Header signatures** — HMAC-SHA256 over canonical auth-header string; reject if timestamp older than 5 minutes
- **SSRF protection** — webhook URLs must be HTTPS (in production), resolve to public IPs, use domain names (no raw IPs, no private/loopback ranges)
- **OAuth CSRF** — single-use, time-limited nonce in the `state` parameter
- **Production mode** (`E2A_ENV=production`) enforces the above where development mode is more permissive

Report security issues privately — see [SECURITY.md](SECURITY.md) for the disclosure process and what's in scope. **Do not file public GitHub issues for vulnerabilities.**

## Data handling

Message envelopes and inbound bodies live in Postgres for 10 days by default; outbound bodies are scrubbed at terminal HITL transition; API keys are stored as hashes; attachments go in JSONB rows (no S3/GCS). Application logs include sender/recipient addresses (standard MTA practice) but never bodies, attachments, raw keys, or HMAC secrets. Users can self-export (`GET /v1/account/export`) and self-delete (`DELETE /v1/account?confirm=DELETE`) for GDPR Art. 15 / Art. 17 / CCPA.

See [docs/data-handling.md](docs/data-handling.md) for the full retention table, log fields, user-rights endpoints, and the operator-side responsibilities (backups, TLS, at-rest encryption, log redaction, compliance).

## FAQ

### Why not just use SendGrid / Resend / Postmark for sending and their inbound parsing for receiving?

Four things that aren't possible to bolt on without significant rework:

1. **Local-mode agents with no public URL.** Agents authenticate with their API key, open a WebSocket to `/v1/agents/{address}/ws`, and inbound mail arrives as JSON over that connection — no webhook URL, no ngrok, no port forward. Useful for agents on developer laptops, edge devices, or behind corporate firewalls. SendGrid/Resend are webhook-only by design. A polling REST API is available as fallback.

2. **Conversation threading on every reply.** Whether a human replies from Gmail or another e2a agent replies via the API, the inbound message arrives at the agent with a stable `conversation_id` already mapped to the original thread. For human senders, the relay does standard `In-Reply-To` / `References` lookup scoped to the recipient agent's own messages. For agent-to-agent where both sides are on e2a, it also trusts an `X-E2A-Conversation-Id` header it controls (envelope-from is its own domain), which survives clients that rewrite threading headers. SendGrid/Resend never see inbound mail — they aren't receivers — so neither path is available without you building both yourself.

3. **Slug provisioning on a shared domain.** Operators set `shared_domain: agents.e2a.dev` and users `POST {"slug": "my-agent"}` to immediately get `my-agent@agents.e2a.dev` with no DNS configuration. Possible because e2a *is* the SMTP relay claiming the domain — Resend / SendGrid are providers, not platforms, and can't multi-tenant a shared address space without you running the relay yourself.

4. **Built-in review hold + auto-expiration.** A per-agent protection policy (outbound gate action `review`, or the content scan) holds mail in `pending_review` state. Reviewers approve via dashboard, magic-link email, the MCP tools, or the API; a background worker auto-acts on expired holds based on the `holds.on_expiry` config. Magic-link tokens are HMAC-encoded — stateless, no session backend. With Resend / SendGrid you'd hold the message in your own DB, build the timer, the approval UI, and the stateless review tokens.

You can absolutely use SES / Resend / SendGrid as e2a's *outbound* SMTP for delivery to humans — that's what `outbound_smtp` in `config.yaml` is for. They complement e2a; they don't replace the inbound receiver, agent abstraction, or any of the layers above transport.

### Why email at all? Why not webhooks, gRPC, or MCP between agents?

Email is the only protocol where every human already has an address and a working client. Webhooks / gRPC / MCP are great inside systems you control, but they don't reach Gmail or Outlook. If you want an agent that talks to humans (or to *other organizations'* agents) without forcing everyone to install a new client, email is the universal substrate.

e2a doesn't replace webhooks — agents *receive* email via webhooks. It bridges email's universal addressability to the structured-data world the agent code already lives in.

### What stops an attacker from spoofing the `X-E2A-Auth-*` headers?

The relay strips any incoming `X-E2A-Auth-*` from inbound messages and re-signs with HMAC-SHA256 against `signing.hmac_secret`. The signed canonical binds `Sender + Verified + Body-Hash + Message-Id` together — replay attempts, body swaps, and sender-only forgery all fail validation. Each delivery is bound to *that specific message body*, not just the sender claim, so a captured `(headers, signature)` tuple can't be lifted onto a different message.

Receivers verify with the SDK — `construct_event(body, header, secret)` / `constructEvent(body, header, secret)` does parse + HMAC verify in one call (or `verify_webhook_signature(...)` / `verifyWebhookSignature(...)` if you only need the boolean check). No API call back to e2a needed. If a signing secret leaks, rotate it via the dashboard and old signatures stop verifying. If it's *stolen from the relay*, the attacker has bigger access than headers anyway.

### Isn't this just SMTP with extra steps?

Yes — and the extra steps are the point. Concretely:

- SPF/DKIM verdict normalization so receivers don't reimplement domain auth
- HMAC-signed delivery contract binding sender, body hash, message ID, and verification status
- WebSocket transport for agents without public URLs
- HITL approval flow with auto-expiration and stateless magic-link review
- Conversation-Id threading that survives the email ↔ structured-data boundary
- Slug-based agent provisioning on a shared domain
- Per-agent webhook routing, rate limits, and HITL config

Building those on top of bare Postfix is a real project. e2a is that project, open source.

### How does this compare to running Postfix or Postal myself?

If you want a full MTA, run an MTA — Postfix and Postal are great. e2a isn't trying to replace them at the SMTP transport level (it uses `go-smtp` for receiving and dial-out for sending). The value is the layer above transport: the auth model, agent abstraction, signed delivery contract, retry policy for webhook failures, HITL approval flow, SDKs and CLI. If you're comfortable operating an MTA and only need email plumbing, e2a may be more than you want. If you want the agent abstraction and signed identity layer prebuilt, that's what this is.

### Why open source if there's a hosted version?

Two reasons:

1. **Auditability.** Identity infrastructure for your agents should be readable code, not a vendor black box. You can verify the cosign signature on `ghcr.io/mnexa-ai/e2a`, reproduce the build, and confirm what's actually running.
2. **Self-host as a real option.** The hosted instance at e2a.dev runs the same `ghcr.io/mnexa-ai/e2a` image you can pull right now. Convenience features on the hosted side (the shared `agents.e2a.dev` domain, managed deliverability) are config + DNS, not closed-source extras.

Pricing for the hosted version isn't enabled yet. When it lands, it'll be opt-in via env var and the OSS code path stays unchanged.

## Development

```bash
make build               # go build -o bin/e2a ./cmd/e2a
make run                 # build + run (cp config.example.yaml config.yaml first)
make test                # all Go tests (needs Postgres on :5433)
make test-unit           # Go unit tests only (no DB)
make test-integration    # integration tests (needs Postgres)
make test-e2e            # e2e tests (needs Postgres)
make docker-up           # start local Postgres via docker compose
make migrate             # apply SQL migrations to local DB
```

See [CLAUDE.md](CLAUDE.md) for the full developer guide (architecture, tests, code generation, conventions).

## Contributing

By submitting a pull request, you certify the [Developer Certificate of Origin](https://developercertificate.org/) for your contribution. Sign your commits with `git commit -s`.

## License

Apache 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
