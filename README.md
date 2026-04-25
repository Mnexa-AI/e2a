# e2a — Email for AI agents

[![Tests](https://github.com/Mnexa-AI/e2a/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/Mnexa-AI/e2a/actions/workflows/test.yml)
[![Build image](https://github.com/Mnexa-AI/e2a/actions/workflows/build-image.yml/badge.svg?branch=main)](https://github.com/Mnexa-AI/e2a/actions/workflows/build-image.yml)
[![License](https://img.shields.io/github/license/Mnexa-AI/e2a)](LICENSE)
[![npm @e2a/sdk](https://img.shields.io/npm/v/%40e2a%2Fsdk?label=%40e2a%2Fsdk)](https://www.npmjs.com/package/@e2a/sdk)
[![PyPI e2a](https://img.shields.io/pypi/v/e2a)](https://pypi.org/project/e2a/)

Authenticated email gateway for AI agents. Receive emails as webhooks or via WebSocket, send emails through an HTTP API, and verify the identity of every sender — humans and other agents alike.

- **Authenticated transport** — SPF/DKIM verified on inbound; HMAC-signed `X-E2A-Auth-*` headers on every delivery
- **Two delivery modes** — webhook (cloud agents) or WebSocket (local agents, no public URL needed)
- **Outbound API** — agents send to other agents (SMTP relay) or humans (upstream SMTP, e.g. SES, Resend)
- **Human in the loop** — opt-in approval gate that holds outbound mail until a reviewer approves via dashboard, magic-link email, or CLI
- **CLI + SDKs** — TypeScript and Python SDKs, plus a `e2a` CLI for everyday agent ops

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
curl -X POST http://localhost:8080/api/v1/agents \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"slug":"my-bot","agent_mode":"local"}'

curl -H "Authorization: Bearer $KEY" http://localhost:8080/api/v1/agents
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

### Agent modes

Agents operate in one of two modes, set via `agent_mode` at registration:

| Mode | Delivery | Public URL needed? |
|------|----------|---------------------|
| `cloud` (default) | HTTPS webhook POST to `webhook_url` | Yes |
| `local` | WebSocket notification + REST fetch | No |

Local-mode agents accumulate "unread" messages while disconnected; on reconnect, the server drains them as WebSocket notifications. Both modes can also poll messages via the REST API.

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

The `X-E2A-Auth-Verified` field is the *server's claim* — anyone who can reach your webhook URL can set it. To make a security decision, **verify the signature** with the shared HMAC secret:

```python
from e2a.v1 import E2AClient
client = E2AClient()
email = client.parse(request_body)
if not email.verify_signature(my_hmac_secret):
    return 401  # untrusted, reject
# now safe to act on email.sender, email.is_verified, etc.
```

```typescript
import { E2AClient } from "@e2a/sdk";
const email = await client.parse(req.body);
if (!email.verifySignature(myHmacSecret)) {
  return res.status(401).end();
}
```

Both SDKs check, in order: body_hash matches the raw message bytes, HMAC matches the canonical, and timestamp is within a 5-minute replay window. Returns `true` only if all three hold. Treat `false` as untrusted regardless of the `is_verified` claim.

### Conversation threading

Both `send` and `reply` accept an opaque `conversation_id`. e2a propagates it to the recipient on delivery via `payload.conversation_id`, surfaced in this priority order:

1. **`X-E2A-Conversation-Id` header** — authoritative for e2a-to-e2a traffic. Only honored when the SMTP envelope `MAIL FROM` originates from this relay, so external senders cannot forge it.
2. **`In-Reply-To` / `References` lookup** — standard RFC 5322 threading, scoped to the recipient agent's own messages. Covers humans replying from Gmail/Outlook.

First contact from a human arrives with `conversation_id: null` — the agent should assign a new id before replying.

### Human in the loop (HITL)

When an agent has HITL enabled, outbound `send` and `reply` calls do **not** dispatch immediately. The message is stored with status `pending_approval` and the API returns HTTP `202 Accepted`. A reviewer must approve it before delivery; otherwise, after a configurable TTL, the message expires into `expired_approved` (auto-sent) or `expired_rejected` (discarded), depending on the agent's `hitl_expiration_action`.

Reviewers can approve or reject via:

- **Dashboard / API** — `POST /api/v1/messages/{id}/approve` or `/reject`
- **Magic-link email** — sent automatically when HITL fires; one-click `GET /api/v1/approve?token=…` and `/reject?token=…` URLs (requires `E2A_PUBLIC_URL` and outbound SMTP configured)
- **CLI** — `e2a pending` lists held messages

Enable HITL on an agent via `PUT /api/v1/agents/{email}` with `hitl_enabled: true` and an optional `hitl_expiration_action` and TTL.

## API

All endpoints are under `/api/v1` unless noted. Auth is `Authorization: Bearer <api_key>` except where called out. Path parameters containing `@` (agent emails) must be URL-encoded.

### Domains

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/domains` | Register a custom domain. Returns required MX and TXT records. |
| `GET` | `/domains` | List domains owned by the authenticated user |
| `POST` | `/domains/{domain}/verify` | Verify ownership via TXT record |
| `DELETE` | `/domains/{domain}` | Delete (must delete all agents on the domain first) |

### Agents

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/agents` | Register an agent. Use `email` for a custom domain (must be verified) or `slug` for a shared-domain registration (only when the deployment has `shared_domain` configured) |
| `GET` | `/agents` | List agents owned by the authenticated user |
| `GET` | `/agents/{email}` | Get agent details |
| `PUT` | `/agents/{email}` | Update agent (webhook URL, mode, HITL settings) |
| `DELETE` | `/agents/{email}` | Delete an agent |
| `POST` | `/agents/{email}/test` | Send a test email through the agent |

### Messages — inbound (per-agent)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents/{email}/messages` | List inbound messages for the agent |
| `GET` | `/agents/{email}/messages/{id}` | Fetch a single inbound message (transitions `unread` → `read` for local-mode agents) |
| `POST` | `/agents/{email}/messages/{id}/reply` | Reply to an inbound message |

### Messages — outbound / HITL

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/send` | Send an email (held with `202 Accepted` if HITL enabled on the agent) |
| `GET` | `/messages` | List outbound messages owned by the user (filterable by status) |
| `GET` | `/messages/{id}` | Get a single outbound message |
| `POST` | `/messages/{id}/approve` | Approve a `pending_approval` message |
| `POST` | `/messages/{id}/reject` | Reject a `pending_approval` message |

### HITL magic links

These endpoints accept a signed `token` query parameter (from notification emails) instead of an API key, so reviewers can approve from any mail client without auth.

| Method | Path | Description |
|--------|------|-------------|
| `GET`/`POST` | `/approve?token=…` | Approve a pending message via signed token |
| `GET`/`POST` | `/reject?token=…` | Reject a pending message via signed token |

### Real-time delivery

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents/{email}/ws?token={api_key}` | WebSocket for local-mode agents. Auth via query param (WebSocket clients can't set headers during upgrade). |

The server pushes lightweight JSON notifications (metadata only):

```json
{
  "message_id": "msg_abc123",
  "conversation_id": "conv_xyz",
  "from": "alice@example.com",
  "to": "bot@your-domain.com",
  "subject": "Meeting tomorrow",
  "received_at": "2026-04-24T10:00:00Z"
}
```

Fetch full content via `GET /agents/{email}/messages/{id}`. On connect, all unread messages are drained as notifications automatically.

### Other

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/health` | none | Health check |
| `GET` | `/api/v1/info` | none | Deployment discovery — returns `shared_domain`, `slug_registration_enabled`, and `public_url`. CLIs/SDKs hit this to self-configure from a single base URL. |
| `POST` | `/api/feedback` | none | Submit feedback (rate-limited per-IP) |

## CLI

```bash
npm install -g @e2a/cli
e2a login
```

| Command | Description |
|---------|-------------|
| `e2a agents register <slug>` | Register `<slug>@<shared-domain>`. The deployment's shared domain is auto-discovered after `e2a login` and cached in `~/.e2a/config.json`. |
| `e2a agents list` | List your agents |
| `e2a agents update <email>` | Update an agent (webhook URL, mode, HITL) |
| `e2a agents delete <email>` | Delete an agent |
| `e2a listen` | Listen for emails over WebSocket (real-time) |
| `e2a listen --json` | Output one full message JSON per line |
| `e2a listen --forward <url>` | Forward each message as HTTP POST to a local URL |
| `e2a inbox` | List recent messages |
| `e2a read <id>` | Read a message |
| `e2a reply <id> --body …` | Reply to a message |
| `e2a send --to … --subject … --body …` | Send an email |
| `e2a pending` | List HITL messages awaiting approval |
| `e2a config` | View or update CLI config |

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
from e2a.v1 import E2AClient

client = E2AClient()                # reads E2A_API_KEY
email = client.parse(request_body)  # validate + decode webhook payload
print(email.sender, email.subject)
email.reply("Got it!", conversation_id="conv_123")
```

WebSocket (local agents):

```python
from e2a.v1 import AsyncE2AClient

async with AsyncE2AClient(api_key="e2a_…") as client:
    async for email in client.listen("bot@your-domain.com"):
        print(email.sender, email.subject)
        await email.reply("Got it!")
```

See [sdks/python/README.md](sdks/python/README.md).

### TypeScript

```bash
npm install @e2a/sdk
```

See [sdks/typescript/README.md](sdks/typescript/README.md).

## Deployment

There are three audiences who configure something — and confusing them is the main UX pothole of self-hosted projects. The split:

| Audience | What they configure | Where |
|---|---|---|
| **Server operator** — runs the Go backend | DB, signing key, SMTP, OAuth, optional shared domain | `config.yaml` + `E2A_*` env |
| **CLI / SDK user** — calls the API from their machine | Just the deployment URL (and login) | `E2A_URL` + `e2a login` |
| **Web dashboard deployer** — hosts the Next.js dashboard | Public site URL + branding | `NEXT_PUBLIC_*` build-time env |

### Server operator

Copy `config.example.yaml` to `config.yaml` and fill in values, or set the environment variables below (env wins over file). All secrets should be set via env, never the file.

| Variable | Required | Description |
|----------|----------|-------------|
| `E2A_DATABASE_URL` | yes | Postgres connection string |
| `E2A_HMAC_SECRET` | yes | HMAC signing secret for `X-E2A-Auth-*` headers |
| `E2A_PUBLIC_URL` | for HITL emails | Externally visible base URL (e.g. `https://e2a.example.com`); required to render absolute magic-link URLs |
| `E2A_SHARED_DOMAIN` | optional | Mail domain backing slug-based agent registration (e.g. `agents.example.com`). When set, users can register agents with just a slug; when empty, every agent must use a custom domain that the user verifies. The shared domain itself becomes reserved (cannot be claimed as a custom domain). |
| `E2A_GOOGLE_CLIENT_ID` | for OAuth login | Google OAuth client ID for dashboard sign-in |
| `E2A_GOOGLE_CLIENT_SECRET` | for OAuth login | Google OAuth client secret |
| `E2A_OUTBOUND_SMTP_HOST` | for outbound | Upstream SMTP host (e.g. `email-smtp.us-east-1.amazonaws.com`) |
| `E2A_OUTBOUND_SMTP_PORT` | for outbound | Upstream SMTP port (typically `587`) |
| `E2A_OUTBOUND_SMTP_USERNAME` | for outbound | Upstream SMTP username |
| `E2A_OUTBOUND_SMTP_PASSWORD` | for outbound | Upstream SMTP password |
| `E2A_OUTBOUND_SMTP_FROM_DOMAIN` | for outbound | Domain used in `From:` of outbound mail |
| `E2A_USAGE_TRACKING` | no (default `false`) | Set to `true` to write per-message rows into `usage_events` / `usage_summaries`. The hosted deployment uses these for billing reconciliation; self-hosters typically don't need them. |

`env: production` in [config.example.yaml](config.example.yaml) enforces TLS for SMTP and HTTPS for webhook URLs. Leave it as `development` for local work.

### CLI / SDK user

End-users only need to know the deployment URL — the rest is auto-discovered.

```bash
export E2A_URL=https://e2a.example.com   # default: https://e2a.dev
e2a login                                # browser flow; saves api key + auto-discovers shared domain
```

The CLI hits `GET /api/v1/info` on login and caches `shared_domain` to `~/.e2a/config.json`, so commands like `e2a agents update my-bot` resolve to the right address on any deployment without further config. Escape hatches if you need to override or skip the discovery step:

| Variable | Description |
|---|---|
| `E2A_URL` | API base URL (default `https://e2a.dev`) |
| `E2A_API_KEY` | Bypass `e2a login` — useful in CI |
| `E2A_SHARED_DOMAIN` | Force the shared domain instead of auto-discovering it |

The TypeScript and Python SDKs follow the same pattern: pass `baseUrl` (or `base_url`) once and call `E2AApi.fetchInfo()` if you need the deployment's shared domain in your own code.

### Web dashboard deployer

The Next.js dashboard ships as a static export, so its config is inlined at build time via `NEXT_PUBLIC_*` env vars. Copy [`web/.env.example`](web/.env.example) to `web/.env.local` and adjust:

| Variable | Description |
|---|---|
| `NEXT_PUBLIC_SITE_URL` | Externally visible base URL of the dashboard. Used for SEO metadata, sitemap, and canonical URLs. Default: `http://localhost:3000`. |
| `NEXT_PUBLIC_SITE_NAME` | Display name in titles, OpenGraph, and structured data. Default: `e2a`. |
| `NEXT_PUBLIC_AGENTS_DOMAIN` | Shared mail domain shown in landing-page code samples (e.g. `agents.example.com`). When empty, samples fall back to `your-domain.com`. |
| `NEXT_PUBLIC_FEEDBACK_EMAIL` | Address shown on the feedback form. Empty hides the "or email us at …" line. |
| `NEXT_PUBLIC_GOOGLE_SITE_VERIFICATION` | Google Search Console token. Only emitted into `<head>` when set, so forks don't inherit upstream's property. |

### Scaling and limitations

**Most state is already DB-coordinated.** The HITL expiration worker, the webhook retry worker, and the periodic cleanup worker all use Postgres `SELECT … FOR UPDATE SKIP LOCKED` (or rely on `DELETE` idempotency for cleanup), so running multiple replicas concurrently is safe — only one worker claims a given pending message at a time, no duplicate sends. User sessions live in Postgres and the OAuth nonce travels in a cookie + the OAuth state parameter, so dashboard sign-in survives load-balancer rebalancing.

That leaves two real horizontal-scaling caveats:

1. **WebSocket fan-out is per-replica.** The hub is an in-memory `map[agentID]*conn` ([internal/ws/hub.go](internal/ws/hub.go)). An agent connected to replica A won't receive real-time notifications for events that happen on replica B — an inbound mail arriving at B's SMTP relay, a HITL approval firing on B's API, etc. Messages aren't lost: they stay `unread` in Postgres and the agent drains them on the next reconnect or REST fetch. They're just not pushed in real-time. Fix: a shared pub/sub (Redis, NATS) for cross-replica notification fan-out, or sticky sessions plus a per-replica routing layer.
2. **Rate limits multiply with replica count.** Limiters are in-process (per-IP, per-agent, per-user — see `ratelimit.New(...)` calls in [internal/agent/api.go](internal/agent/api.go)). With two replicas the effective caps are 2× looser, not stricter. Operators who need exact global limits would move the limiters to a shared store (Redis, or a Postgres-backed token bucket).

**Vertical scaling is fine.** The API, the SMTP relay, and all three background workers run safely on multiple replicas today — the only paths that need attention before you do are the two above.

**Dashboard auth is Google OAuth only.** [`internal/auth/auth.go`](internal/auth/auth.go) imports `golang.org/x/oauth2/google` directly and the config exposes `google_client_id` / `google_client_secret`. Teams running GitHub OAuth, Microsoft Entra, Okta, or generic OIDC need to add a provider in that package. The CLI and SDKs authenticate with API keys, which are provider-agnostic.

**Otherwise infra-agnostic.** The Go binary runs on any container host (Docker, Podman, k8s, ECS, Fly, Cloud Run, …). Storage is plain Postgres 14+ — managed (RDS, Cloud SQL, Neon, Supabase) or self-managed. Email goes out via standard SMTP, not a vendor SDK. Attachments live in Postgres rows, so there's no S3/GCS dependency. No queue, no Redis, no separate worker process. Secrets are read from env vars, so any secret manager that injects env at start time works.

## Security

- **Identity** — agent registration requires DNS TXT verification of domain ownership (custom domains)
- **Domain auth** — SPF and DKIM checked on every inbound message
- **Header signatures** — HMAC-SHA256 over canonical auth-header string; reject if timestamp older than 5 minutes
- **SSRF protection** — webhook URLs must be HTTPS (in production), resolve to public IPs, use domain names (no raw IPs, no private/loopback ranges)
- **OAuth CSRF** — single-use, time-limited nonce in the `state` parameter
- **Production mode** (`E2A_ENV=production`) enforces the above where development mode is more permissive

Report security issues privately — see [SECURITY.md](SECURITY.md) for the disclosure process and what's in scope. **Do not file public GitHub issues for vulnerabilities.**

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
