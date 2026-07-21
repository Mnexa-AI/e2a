# AGENTS.md

Guidance for AI coding agents working in this repository. Assumes no prior
knowledge of the project. For deeper prose, see `README.md` (product),
`CONTRIBUTING.md` (contributor workflow), `CLAUDE.md` (architecture notes),
`docs/` (API reference, deployment, design docs), and `SECURITY.md`.

## Project overview

e2a is an **authenticated email gateway for AI agents**. It gives an agent a
real email address: inbound mail is received over SMTP, sender-verified
(SPF/DKIM/DMARC), and delivered to the agent as structured authentication
evidence — via webhook, WebSocket, REST polling, or MCP tools.
Outbound mail goes out through an HTTP API (SMTP relay agent-to-agent,
upstream SMTP such as SES agent-to-human), with an optional human-in-the-loop
(HITL) approval gate and opt-in prompt-injection content screening (piguard).

Polyglot monorepo, Apache-2.0, GitHub: `tokencanopy/e2a`. The `/v1` API and
SDKs are release candidates; stable compatibility guarantees start at the
announced GA tag.

| Surface | Path | Stack | Package |
|---|---|---|---|
| Backend server | `cmd/e2a/` + `internal/` | Go (module `github.com/tokencanopy/e2a`) | `bin/e2a` binary |
| CLI | `cli/` | TypeScript, Node ≥18 | `@e2a/cli` (npm) |
| TypeScript SDK | `sdks/typescript/` | TypeScript | `@e2a/sdk` (npm) |
| Python SDK | `sdks/python/` | Python ≥3.9, hatchling | `e2a` (PyPI) |
| MCP server | `mcp/` | TypeScript, Express | `@e2a/mcp-server` (hosted-only; npm publish retired) |
| Web dashboard | `web/` | Next.js 16 App Router, React 19, Tailwind CSS 4, static export | private |
| Design system | `design-system/` | React + tsup + Storybook | `@e2a/ui` ("Loft", consumed via `file:` dep) |
| Agent plugin | `plugins/e2a/` | Markdown skills + manifests | Claude / Codex / Cursor marketplaces |

Toolchain versions: `go.mod` declares Go 1.25; CI and the Dockerfiles build
with Go 1.26. Node: engines `>=18`, CI runs on 22. Python: `requires-python
>=3.9`, CI runs on 3.12.

## Repository layout

- `cmd/e2a/` — main server binary entry point (SMTP relay + HTTP API in one
  process). Other binaries: `cmd/e2a-contract-server` (test instance for SDK
  contract tests), `cmd/e2a-prober` (critical-path self-test runner),
  `cmd/e2a-openapi-normalize`, `cmd/e2a-openapi-sdk-check`,
  `cmd/e2a-openapi-security-check` (compat-gate helpers), `cmd/piguard-eval`.
- `internal/` — all backend packages (see "Backend architecture").
- `api/openapi.yaml` — committed OpenAPI 3.1 document, golden-tested against
  the live Huma handlers. `api/testdata/oasdiff/` holds fixtures for the
  compatibility-policy tests.
- `migrations/` — sequential `0NN_*.sql` files, embedded into the binary via
  `migrations/embed.go` and auto-applied at startup.
- `tests/contract/` — Go contract tests driven by `scenarios.yaml`;
  `tests/e2e-prod/` — production smoke-test harness (TypeScript).
- `docs/` — `api.md`, `deployment.md`, `events.md`, `templates.md`,
  `data-handling.md`, `api-compatibility-gate.md`, plus `design/` (design
  docs), `runbooks/` (ops procedures).
- `scripts/` — CI guardrails (OpenAPI compat check, plugin validator, SDK
  version-sync check, repo text-integrity check).
- `examples/adk-cloud-webhook/` — Python webhook example.
- Root config files: `go.mod`/`go.sum` (Go), `package.json` +
  `package-lock.json` (npm workspaces: `cli`, `sdks/typescript`, `mcp`,
  `design-system` — note `web/` and `sdks/python/` are NOT workspaces),
  `Makefile` (all Go-side workflows), `config.example.yaml` (annotated server
  config template), `docker-compose.yaml` (local dev), `Dockerfile` (server
  image), `.testcoverage.yml` (per-package coverage floors), `VERSION`.

## Build and test commands

### Go backend (via Makefile)

```bash
make build              # go build -o bin/e2a ./cmd/e2a
make run                # build + run with config.yaml (copy from config.example.yaml first)
make test               # ALL Go tests, -tags integration -p 1 (needs Postgres on :5433)
make test-unit          # unit tests only, no DB needed — fast
make test-integration   # integration tests (needs Postgres on :5433)
make test-e2e           # discovers every package with //go:build integration tests
make cover              # coverage profile cover.out across internal/... (needs Postgres)
make cover-check        # cover + enforce per-package floors in .testcoverage.yml
make docker-up          # local Postgres (:5433) + Mailpit (SMTP :1025, UI :8025)
make migrate            # manually apply migrations/*.sql to the local DB
make spec               # regenerate api/openapi.yaml from the live Huma handlers
make spec-check         # drift gate: committed spec must byte-equal handler output
make generate-sdk       # regenerate TS + Python SDK bases from the spec (Docker, OAG v7.16.0)
make generate           # spec + generate-sdk
make generate-sdk-check # regenerate + fail on git diff (CI freshness gate)
make openapi-compat-check  # oasdiff backward-compat gate vs origin/main:api/openapi.yaml
```

DB-backed tests need
`E2A_TEST_DATABASE_URL="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable"`.
The test harness (`internal/testutil/db.go`) truncates tables between tests
and auto-applies all migrations on connect. **Never share one test database
between concurrent sessions/agents/worktrees** — create a separate database
per runner and point `E2A_TEST_DATABASE_URL` at it. Always run DB-backed
packages with `-p 1`.

### TypeScript (npm workspaces — always use `--workspace` and `npm ci`)

```bash
npm ci                                     # deterministic install of workspace deps
npm run build --workspace @e2a/sdk         # SDK (must build before CLI and MCP)
npm test --workspace @e2a/sdk              # typecheck + vitest unit + type tests
npm run test:contract --workspace @e2a/sdk # contract tests (needs live server)
npm test --workspace @e2a/cli              # CLI tests (vitest)
npm run build --workspace @e2a/cli
npm test --workspace @e2a/mcp-server
npm run build --workspace @e2a/mcp-server
npm run build --workspace @e2a/ui          # design system; dist/ is committed
```

### Python SDK (not a workspace; has its own venv/pip flow)

```bash
cd sdks/python
pip install -e ".[dev]"
pytest tests/ -v                     # unit tests
pytest tests/test_contract.py -v     # contract tests (needs live server)
mypy                                 # type gate (CI runs this too)
```

### Web dashboard (not a workspace)

```bash
cd web
npm ci
npm run dev     # dev server :3000, proxies /api/* to localhost:8080
npm test        # Jest
npm run lint    # ESLint
npm run build   # Next.js static export
```

### First run (local backend)

```bash
cp config.example.yaml config.yaml
make docker-up     # Postgres :5433 + Mailpit :1025/:8025
make run           # API on :8080, SMTP relay on :2525; auto-applies migrations
./bin/e2a -config config.yaml -bootstrap-email you@example.com  # create user + API key
```

Point `outbound_smtp` in `config.yaml` at Mailpit (`host: localhost`,
`port: 1025`, `from_domain: e2a.localhost`) to exercise outbound flows without
real SMTP credentials; captured mail appears at http://localhost:8025.

## Backend architecture (`internal/`)

Single Go process (`cmd/e2a/main.go`) running an SMTP relay, the HTTP API,
and background workers. Inbound flow: SMTP → `emailauth` (SPF/DKIM/DMARC) → agent
lookup → canonical authentication persistence → webhook fan-out or WebSocket
push. Outbound is **always queue-first**: the accept transaction atomically
persists the message and enqueues a River job; a worker submits to the relay
and records the terminal outcome.

Key packages:

- `relay` — SMTP server, inbound mail intake. `emailauth` + `dkim` — SPF/DKIM/
  DMARC evaluation and alignment. `mailparse` — raw RFC 5322 → parsed view.
- `httpapi` — the typed `/v1` Huma handlers (source of the OpenAPI document);
  `apiserver` — assembles the process HTTP handler.
- `agent` — agent CRUD, HITL, REST API. `agentauth` — agent-identity token
  layer (JWKS/JWT).
- `identity` / `senderidentity` — domain ownership verification/storage,
  sender-identity resolution (incl. SES BYODKIM provisioning).
- `ws` — WebSocket hub for real-time message push. `loopback` — agent-to-self
  delivery without touching the network.
- `outbound` / `outboundsend` — compose + send via upstream SMTP; the
  queue-first River send worker.
- `inboundprocess` / `inboundpolicy` — async inbound processing worker and
  screening/policy decisions (allow / review / block).
- `piguard` — prompt-injection / phishing content screening: dependency-free
  heuristics detector, plus a Gemini LLM-as-detector layer enabled by
  `GEMINI_API_KEY` (kill switch `E2A_GEMINI_DETECTOR_ENABLED=false`).
- `hitlworker` / `hitlnotify` — human-in-the-loop review holds
  (`pending_review`) and approval notifications.
- `webhook` / `webhookdelivery` / `webhookpub` — webhook subscriptions,
  durable fan-out, HTTP POST delivery with retry (SSRF-guarded).
  `eventpayload` — canonical typed `data` payloads for webhook events.
- `jobs` — River-backed durable job runtime (Postgres mandatory).
  `janitor` — periodic TTL sweeps (trash expiry, expired holds).
- `delivery` — SES delivery/bounce/complaint feedback via SNS
  (`POST /webhooks/ses`, signature-verified, fail-closed).
- `idempotency` — `Idempotency-Key` storage + replay.
- `usage` / `limits` — usage metering and plan/account entitlement limits.
- `sendramp` — per-domain recipient-volume ramping for new sender domains.
- `auth` — API key authentication. `oauth` — fosite-based authorization
  server (MCP OAuth). Google OAuth login + optional generic OIDC login.
- `ratelimit`, `telemetry` (metrics interface), `emailtemplate` +
  `startertemplates` (server-side `{{variable}}` templates + starter catalog),
  `mailfrom` (custom MAIL FROM convention), `selftest` (critical-path
  self-test used by `cmd/e2a-prober`), `openapicompat` (normalization for the
  compat gate), `config` (YAML + `E2A_*` env overrides), `testutil` (test
  harness), `e2e` (end-to-end suites).

Async-migration feature flags: inbound async processing is opt-in
(`E2A_INBOUND_MODE=async`, default `sync`) and webhook fan-out on River is
opt-in (`E2A_WEBHOOK_FANOUT_MODE=river`, default `legacy`); unknown values
fall back to the historical in-process path. Outbound has no such flag — the
legacy `E2A_OUTBOUND_MODE` switch was removed (guarded by
`TestOutboundModeConfigurationRemoved`).

## OpenAPI spec and SDK generation (contract pipeline)

The `/v1` surface (`internal/httpapi`, Huma v2 on chi) emits OpenAPI 3.1 from
the typed handlers — the handlers are the single source of truth.

- `make spec` regenerates the committed `api/openapi.yaml`;
  `TestSpecGoldenNoDrift` (runs in unit tests and CI) fails on drift.
- `make generate-sdk` runs OpenAPI Generator (pinned image
  `openapitools/openapi-generator-cli:v7.16.0`, via Docker) to regenerate the
  TS base in `sdks/typescript/src/v1/generated/` and the Python base in
  `sdks/python/src/e2a/v1/generated/`. Hand-written ergonomic layers
  (`client.ts` / `client.py`, etc.) wrap the generated bases; package
  scaffolding is suppressed via `.openapi-generator-ignore`.
- **After any `/v1` handler change: run `make generate` and commit
  `api/openapi.yaml` plus both `generated/` trees.** CI gates this with
  `spec-check` and `generate-sdk-check`.
- `make openapi-compat-check` rejects breaking `/v1` changes on PRs (oasdiff
  policy; see `docs/api-compatibility-gate.md`).
- The old swag-annotation pipeline is fully retired — do not reintroduce it.
  The dashboard renders the API reference live from `/v1/openapi.yaml`.

Every API change is expected to maintain parity across **eight client
surfaces**: Go handler, OpenAPI spec, TS SDK (raw + high-level), Python SDK
sync (raw + high-level), Python SDK async (raw + high-level), CLI, MCP server,
web dashboard. The PR template checklists them.

## Client surfaces

- **TS SDK** (`sdks/typescript/`): layered — generated types → `E2AApi` (raw
  HTTP) → `E2AClient` (high-level `.parse()`, `.reply()`); WebSocket in
  `v1/ws.ts`; webhook signature verification in `v1/webhook-signature.ts`.
- **Python SDK** (`sdks/python/`): src layout, async-native (httpx), sync +
  async high-level clients over the generated base; PEP 561 `py.typed`.
- **CLI** (`cli/`): commands login, whoami, agents, keys, protection, send,
  reply, messages, listen, config; config in `~/.e2a/config.json`; `listen
  --forward` proxies WebSocket messages to a local HTTP endpoint. **Exit codes
  (`cli/src/exit.ts`) are a frozen contract** — 0 ok, 1 transient, 2 usage,
  3 held-for-review, 4 auth, 5 permanent request error, 6 timeout,
  7 send-outcome. Add new codes, never renumber.
- **MCP server** (`mcp/`): inbox tools over the REST API; hosted HTTP
  transport (image `ghcr.io/tokencanopy/e2a-mcp-http`). **npm publishing is
  retired** (`@e2a/mcp-server` frozen at 0.4.0) — do not configure a trusted
  publisher.
- **Web** (`web/`): Next.js 16 static export; dev rewrites `/api/*` to
  :8080; consumes `@e2a/ui` via a `file:` dep, so `design-system/dist/` is
  committed and CI fails if it drifts (rebuild with
  `npm run build --workspace @e2a/ui` and commit).

## Testing strategy

- **Go tiers**: `test-unit` (no DB), `test-integration` (Postgres),
  `test-e2e` (auto-discovers `//go:build integration` packages); `make test`
  runs everything with `-tags integration -p 1`. CI additionally race-checks
  `./internal/sendramp` and `./internal/outboundsend` with `-race`.
- **Coverage ratchet**: `.testcoverage.yml` sets per-package floors (currently
  webhook, webhookpub, httpapi, outboundsend, sendramp, inboundprocess).
  Ratchet floors UP, never down. `make cover-check` runs the same gate CI
  runs (`vladopajic/go-test-coverage`).
- **Contract tests**: TS and Python SDK contract tests run against
  `cmd/e2a-contract-server`, a real e2a instance backed by Postgres (CI builds
  and launches it automatically; locally you need it running for
  `test:contract` / `test_contract.py`).
- **Schema-change rule**: when changing a table shape, add/update DB-backed
  tests in every package that writes direct SQL against that table — the
  idempotent migration runner will not catch drifted runtime SQL.
- TS uses vitest (+ `tsc` type tests), web uses Jest, Python uses pytest +
  mypy. CI workflow: `.github/workflows/test.yml` (jobs: Go, coverage gate,
  Go e2e, web, ts-sdk, ts-contract, cli, mcp, spec gates, python-sdk,
  python-contract, generated-code freshness, design-system dist freshness,
  SDK-version-sync, plugin manifests, repo text integrity).
- `tests/e2e-prod/` is a production smoke harness — not part of local dev.

## Conventions

- **npm**: root `package.json` declares the workspaces; always `npm ci` and
  `--workspace <name>`; commit intentional lockfile updates. In-repo consumers
  must declare an `@e2a/sdk` range the workspace SDK satisfies (CI guardrail:
  `scripts/check-sdk-version-sync.mjs`).
- **Migrations** (`migrations/0NN_*.sql`): must be **idempotent**
  (`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, …),
  **non-destructive on prod-sized tables** (no `ALTER COLUMN TYPE` on
  `messages` / `usage_events`), **sequentially numbered** (never renumber),
  **forward-only** (no down migrations — write a new migration to undo).
  Embedded in the binary and auto-applied at startup;
  `E2A_MIGRATION_MODE=auto|verify|skip` controls behavior.
- **IDs**: resources use `{type}_{random}` IDs (e.g. `msg_abc123`).
- **Config**: `config.yaml` for non-secrets + `E2A_*` env var overrides (env
  wins). Secrets go in env vars only, never in the file. `env: production`
  enforces TLS, HTTPS webhook URLs, and HMAC-secret strength (≥32 bytes).
- **Commits**: `type(scope): short imperative subject` (≤72 chars); types
  include `feat`, `fix`, `chore`, `docs`, `test`, `refactor`, `deps`,
  `release`; scopes are package/surface-shaped (e.g. `feat(api)`,
  `fix(web)`). Not formally Conventional Commits. One PR per feature — no
  bundled drive-by cleanup. CI must be green.
- **Coverage floors** only move up (see Testing strategy).
- **Postgres**: local dev runs on port **5433** (not 5432) via docker compose.
- The Mailpit service in `docker-compose.yaml` is local-dev only — production
  deployments must drop it and point `E2A_OUTBOUND_SMTP_*` at a real relay.

## Security considerations

- The security model rests on: SPF/DKIM/DMARC evaluation, webhook endpoint
  signing + SSRF guard, HITL approval
  tokens, API keys (only hashes are stored), and fail-closed external
  integrations (SNS signature verification, OIDC discovery).
- Never commit secrets. `.env` is gitignored; use it or env vars for
  `E2A_HMAC_SECRET`, OAuth secrets, SMTP credentials, `GEMINI_API_KEY`, etc.
  `docker-compose.yaml`'s baked-in `E2A_HMAC_SECRET` is demo-only.
- Webhook delivery is SSRF-guarded; keep URL validation fail-closed when
  touching `internal/webhookdelivery`. Production mode forces HTTPS webhook
  URLs.
- Report vulnerabilities privately per `SECURITY.md`
  (security@tokencanopy.com or GitHub private advisories) — never a public
  issue. Data handling/retention: `docs/data-handling.md`.

## Deployment and publishing

- **Server image**: root `Dockerfile` (multi-arch, CGO-free, alpine,
  non-root, `/api/health` healthcheck) → `ghcr.io/tokencanopy/e2a` via
  `build-image.yml`. `Dockerfile.prober` builds the co-versioned self-test
  runner. Ports: SMTP 2525, HTTP 8080. Deployment guide:
  `docs/deployment.md`.
- **Migrations ship with the binary** and apply on startup — no manual
  migration ceremony on deploy.
- **Publishing** (all tag/dispatch-triggered GitHub workflows):
  - Python SDK: bump `sdks/python/pyproject.toml` version, tag `python-v*`.
  - TS SDK: bump `sdks/typescript/package.json` version, tag `ts-sdk-v*`.
  - CLI: bump `cli/package.json` version, then GitHub release publish or
    `gh workflow run "Publish CLI" --ref main`.
  - MCP: hosted-only — `publish-mcp-http.yml` pushes the HTTP image on tag;
    npm publish is retired.
