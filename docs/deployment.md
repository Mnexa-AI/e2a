# Deployment

There are three audiences who configure something — and confusing them is the main UX pothole of self-hosted projects. The split:

| Audience | What they configure | Where |
|---|---|---|
| **Server operator** — runs the Go backend | DB, signing key, SMTP, OAuth, optional shared domain | `config.yaml` + `E2A_*` env |
| **CLI / SDK user** — calls the API from their machine | Just the deployment URL (and login) | `E2A_URL` + `e2a login` |
| **Web dashboard deployer** — hosts the Next.js dashboard | Public site URL + branding | `NEXT_PUBLIC_*` build-time env |

## Server operator

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

`env: production` in [config.example.yaml](../config.example.yaml) enforces TLS for SMTP and HTTPS for webhook URLs. Leave it as `development` for local work.

### Shared-domain setup

If you set `E2A_SHARED_DOMAIN` (or `shared_domain` in `config.yaml`) so users can register agents with just a slug — `alice@agents.yourcompany.com` — there are two parts to it: DNS you set up once, and a database row the server takes care of for you.

**You do (once, externally):**

1. Pick the subdomain (e.g. `agents.yourcompany.com`).
2. Add an `MX` record pointing it at the host running the e2a SMTP relay.
3. Add `A`/`AAAA` records for that host.
4. Open inbound port 25 (the SMTP listener defaults to `:2525` — either change `smtp.listen_addr` to `:25` or NAT 25→2525).
5. Provision a TLS cert for the SMTP domain and set `smtp.tls_cert` / `smtp.tls_key`.
6. Add SPF/DKIM TXT records on the subdomain so outbound mail from your relay isn't rejected by recipient mail servers.

**The server does (automatically, at startup):**

The shared domain needs a row in the `domains` table — it's the FK target for every agent registered against it. The server seeds this row idempotently every time it boots: `INSERT … ON CONFLICT DO NOTHING` against the configured `shared_domain`, with `user_id = NULL` and `verified = true` (system-owned, pre-verified). You don't run a migration, you don't `psql` anything by hand. Change the configured domain later? Restart and the new row appears; the old one stays as a harmless orphan because the API layer reads `cfg.SharedDomain` to decide what's reserved, not the table.

If you leave `shared_domain` empty, slug registration is disabled and every agent must use a custom domain the user verifies — no DNS setup required from you.

## CLI / SDK user

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

## Web dashboard deployer

The Next.js dashboard ships as a static export, so its config is inlined at build time via `NEXT_PUBLIC_*` env vars. Copy [`web/.env.example`](../web/.env.example) to `web/.env.local` and adjust:

| Variable | Description |
|---|---|
| `NEXT_PUBLIC_SITE_URL` | Externally visible base URL of the dashboard. Used for SEO metadata, sitemap, and canonical URLs. Default: `http://localhost:3000`. |
| `NEXT_PUBLIC_SITE_NAME` | Display name in titles, OpenGraph, and structured data. Default: `e2a`. |
| `NEXT_PUBLIC_AGENTS_DOMAIN` | Shared mail domain shown in landing-page code samples (e.g. `agents.example.com`). When empty, samples fall back to `your-domain.com`. |
| `NEXT_PUBLIC_FEEDBACK_EMAIL` | Address shown on the feedback form. Empty hides the "or email us at …" line. |
| `NEXT_PUBLIC_GOOGLE_SITE_VERIFICATION` | Google Search Console token. Only emitted into `<head>` when set, so forks don't inherit upstream's property. |

## Scaling and limitations

**Most state is already DB-coordinated.** The HITL expiration worker, the webhook retry worker, and the periodic cleanup worker all use Postgres `SELECT … FOR UPDATE SKIP LOCKED` (or rely on `DELETE` idempotency for cleanup), so running multiple replicas concurrently is safe — only one worker claims a given pending message at a time, no duplicate sends. User sessions live in Postgres and the OAuth nonce travels in a cookie + the OAuth state parameter, so dashboard sign-in survives load-balancer rebalancing.

That leaves two real horizontal-scaling caveats:

1. **WebSocket fan-out is per-replica.** The hub is an in-memory `map[agentID]*conn` ([internal/ws/hub.go](../internal/ws/hub.go)). An agent connected to replica A won't receive real-time notifications for events that happen on replica B — an inbound mail arriving at B's SMTP relay, a HITL approval firing on B's API, etc. Messages aren't lost: they stay `unread` in Postgres and the agent drains them on the next reconnect or REST fetch. They're just not pushed in real-time. Fix: a shared pub/sub (Redis, NATS) for cross-replica notification fan-out, or sticky sessions plus a per-replica routing layer.
2. **Rate limits multiply with replica count.** Limiters are in-process (per-IP, per-agent, per-user — see `ratelimit.New(...)` calls in [internal/agent/api.go](../internal/agent/api.go)). With two replicas the effective caps are 2× looser, not stricter. Operators who need exact global limits would move the limiters to a shared store (Redis, or a Postgres-backed token bucket).

**Vertical scaling is fine.** The API, the SMTP relay, and all three background workers run safely on multiple replicas today — the only paths that need attention before you do are the two above.

**Dashboard auth is Google OAuth only.** [`internal/auth/auth.go`](../internal/auth/auth.go) imports `golang.org/x/oauth2/google` directly and the config exposes `google_client_id` / `google_client_secret`. Teams running GitHub OAuth, Microsoft Entra, Okta, or generic OIDC need to add a provider in that package. The CLI and SDKs authenticate with API keys, which are provider-agnostic.

**Otherwise infra-agnostic.** The Go binary runs on any container host (Docker, Podman, k8s, ECS, Fly, Cloud Run, …). Storage is plain Postgres 14+ — managed (RDS, Cloud SQL, Neon, Supabase) or self-managed. Email goes out via standard SMTP, not a vendor SDK. Attachments live in Postgres rows, so there's no S3/GCS dependency. No queue, no Redis, no separate worker process. Secrets are read from env vars, so any secret manager that injects env at start time works.
