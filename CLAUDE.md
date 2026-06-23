# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

e2a is an authenticated email gateway for AI agents. It provides SMTP relay with SPF/DKIM verification, WebSocket real-time delivery, webhook delivery, a CLI, TypeScript and Python SDKs, and a Next.js web dashboard. Polyglot monorepo: Go (backend), TypeScript (CLI + SDK), Python (SDK), React/Next.js (web).

## Common Commands

### Go backend
```bash
make build              # go build -o bin/e2a ./cmd/e2a
make run                # build + run (uses config.yaml; copy from config.example.yaml first)
make test               # all Go tests (needs Postgres on :5433)
make test-unit          # Go unit tests only (no DB needed)
make test-integration   # Go integration tests (needs Postgres on :5433)
make test-e2e           # Go e2e tests (needs Postgres on :5433)
make docker-up          # start local Postgres + Mailpit via docker compose
make migrate            # apply SQL migrations to local DB
```

Go tests that need the database use `E2A_TEST_DATABASE_URL="postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable"`.

**Outbound mail in dev (Mailpit catch-all).** `make docker-up` also starts [Mailpit](https://github.com/axllent/mailpit) — a single-binary SMTP server that captures every outbound message and exposes them at http://localhost:8025. The dockerized `e2a` service points at it automatically. For `make run` (host Go binary), uncomment the Mailpit block in `config.example.yaml`'s `outbound_smtp` section before copying to `config.yaml`, or set `E2A_OUTBOUND_SMTP_HOST=localhost`, `E2A_OUTBOUND_SMTP_PORT=1025`, `E2A_OUTBOUND_SMTP_FROM_DOMAIN=e2a.localhost`. Use this to exercise HITL approval notifications and the `/v1/agents/{email}/test` button locally without real SMTP creds.

### TypeScript SDK & CLI (npm workspaces)
```bash
npm install --package-lock=false           # install all workspace deps
npm run build --workspace @e2a/sdk         # build SDK (must build before CLI)
npm test --workspace @e2a/sdk              # SDK unit tests (vitest)
npm run test:contract --workspace @e2a/sdk # SDK contract tests (needs live server)
npm test --workspace @e2a/cli              # CLI tests (vitest)
npm run build --workspace @e2a/cli         # build CLI
```

### Python SDK
```bash
cd sdks/python
pip install -e ".[dev]"     # install with dev deps
pytest tests/ -v            # unit tests
pytest tests/test_contract.py -v  # contract tests (needs live server)
```

### Web dashboard
```bash
cd web
npm install
npm run dev     # dev server (proxies /api/* to localhost:8080)
npm test        # Jest tests
npm run lint    # ESLint
npm run build   # static export
```

### Code generation
```bash
make spec           # regenerate api/openapi.yaml from the live /v1 Huma handlers
make generate-sdk   # regenerate the TS + Python SDK bases from api/openapi.yaml (OpenAPI Generator)
make generate       # both of the above
```

After changing a `/v1` handler, run `make generate` and commit the regenerated `api/openapi.yaml` plus the SDK bases in `sdks/typescript/src/v1/generated/` and `sdks/python/src/e2a/v1/generated/`. CI (`spec-check` + `generate-sdk-check`) fails if either is stale. (The legacy swag pipeline is gone — `web/public/openapi.yaml` is a frozen copy for the dashboard's API-reference page only and no longer feeds the SDKs.)

## Architecture

### Go backend (`cmd/e2a/` + `internal/`)

The main server (`cmd/e2a/main.go`) runs an SMTP relay and HTTP API. Key internal packages:

- **relay** — SMTP server, receives inbound email
- **emailauth** — SPF/DKIM verification on inbound messages
- **agent** — Agent CRUD, API endpoints, routes
- **identity** — Domain ownership verification and storage
- **headers** — HMAC-SHA256 signing of `X-E2A-Auth-*` headers
- **webhook** — HTTP POST delivery to agent endpoints with retry
- **ws** — WebSocket hub for real-time message push
- **outbound** — Compose and send emails via upstream SMTP (SES)
- **billing** — Stripe integration, usage metering
- **auth** — API key authentication
- **config** — YAML config + env var overrides

Inbound flow: SMTP → emailauth (SPF/DKIM) → agent lookup → headers signing → webhook or WebSocket delivery.

### OpenAPI spec source of truth

The `/v1` surface (`internal/httpapi`, Huma) emits its OpenAPI 3.1 document from
the typed handlers. `make spec` regenerates the committed copy at
`api/openapi.yaml`; `make spec-check` (and `TestSpecGoldenNoDrift`, which runs in
`make test-unit`) is the drift gate — the committed spec must byte-equal what the
live handlers emit, so it can never lag the server. Regenerate + commit
`api/openapi.yaml` after any `/v1` handler change.

### SDK type generation pipeline

The SDK base clients are generated from the canonical Huma spec by OpenAPI
Generator (`openapitools/openapi-generator-cli`), no swag step:

```
api/openapi.yaml (Huma 3.1)
  → openapi-generator (typescript) → sdks/typescript/src/v1/generated/   (the oag base)
  → openapi-generator (python)     → sdks/python/src/e2a/v1/generated/    (package e2a.v1.generated)
```

`make generate-sdk` (= `generate-sdk-ts` + `generate-sdk-py`) regenerates both
bases via `sdks/*/scripts/generate-oag.sh`; `make generate-sdk-check`
(and CI) is the drift gate. Over each generated base sits a hand-written
ergonomic layer (`client.ts` / `client.py` etc.) wired up via
`.openapi-generator-ignore`. Regenerate + commit the `generated/` trees after
any `/v1` handler change.

The old swag-annotation pipeline was fully retired (the `make swagger` target and
its `internal/agent/api_docs.go` source are gone). `web/public/openapi.yaml` is
retained only because the dashboard's API-reference page
(`web/public/scalar.html`) renders it; it is a frozen copy and not CI-checked.

### TypeScript SDK (`sdks/typescript/`)

Layered: generated types → `E2AApi` (raw HTTP) → `E2AClient` (high-level with `.parse()`, `.reply()`). WebSocket support in `v1/ws.ts`.

### CLI (`cli/`)

Commands: login, listen, config. Config stored in `~/.e2a/config.json`. The `listen` command supports `--forward` mode for proxying WebSocket messages to local HTTP endpoints.

### Web (`web/`)

Next.js 16 App Router with Tailwind CSS 4. In dev mode, rewrites `/api/*` to `localhost:8080`. Production builds as static export.

### Contract tests

Both TS and Python SDKs have contract tests that run against a real e2a server. The `cmd/e2a-contract-server` binary spins up a test instance with Postgres. CI handles this automatically.

## Publishing

### Python SDK
Triggered by tag push (`python-v*`).
1. Bump `version` in `sdks/python/pyproject.toml`
2. Commit and push to main
3. `git tag python-v<VERSION> && git push origin python-v<VERSION>`

### TypeScript SDK
Triggered by tag push (`ts-sdk-v*`) or `workflow_dispatch`.
1. Bump `version` in `sdks/typescript/package.json`
2. `npm run build --workspace @e2a/sdk`
3. Commit and push to main
4. `git tag ts-sdk-v<VERSION> && git push origin ts-sdk-v<VERSION>`

### CLI
Triggered by GitHub release publish or `workflow_dispatch`.
1. Bump `version` in `cli/package.json`
2. `npm run build --workspace @e2a/cli`
3. Commit and push to main
4. `gh workflow run "Publish CLI" --ref main`

### MCP server
Triggered by tag push (`mcp-v*`) or `workflow_dispatch`. Publishes `@e2a/mcp-server` to npm with provenance.
1. Bump `version` in `mcp/package.json`
2. `npm run build --workspace @e2a/mcp-server`
3. Commit and push to main
4. `git tag mcp-v<VERSION> && git push origin mcp-v<VERSION>`

The first publish requires `@e2a/mcp-server` to be configured as a trusted publisher on npmjs.com against `Mnexa-AI/e2a` + `publish-mcp.yml` (one-time, done in the npm web UI).

## Key Conventions

- **npm workspaces**: root `package.json` declares `cli` and `sdks/typescript` as workspaces. Always use `--workspace` flag for workspace commands. Use `--package-lock=false` for install.
- **Go module**: `github.com/Mnexa-AI/e2a`, Go 1.25
- **Go test tiers**: `test-unit` needs no DB. `test-integration` needs Postgres (runs identity/agent packages). `test-e2e` uses build tag `integration` and runs `internal/e2e/`. `make test` runs everything (including e2e) with `-tags integration -p 1`.
- **Schema changes**: when changing a table shape, add or update DB-backed tests for every package that writes direct SQL against that table. Higher-level e2e tests are not enough. Our migration helper is idempotent and will not automatically catch old query assumptions if runtime SQL drifts from the redesigned schema.
- **Migrations**: every `migrations/00N_*.sql` must be **idempotent** (use `CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, etc.) and **non-destructive on prod-sized tables** (`ALTER TABLE ... ADD COLUMN` is safe; `ALTER COLUMN TYPE` can rewrite the whole table — avoid on `messages` and `usage_events`). The e2a binary embeds `migrations/*.sql` via `migrations/embed.go` and auto-applies pending ones at startup against a `schema_migrations` tracker table; `E2A_MIGRATION_MODE` controls the behavior (`auto` default, `verify` to refuse and report pending, `skip` for emergency surgery). New migrations land in prod on the next binary deploy with zero manual ceremony.
- **Postgres**: local dev DB runs on port 5433 (not 5432) via docker compose.
- **ID format**: resources use `{type}_{random}` IDs (e.g., `msg_abc123`).
