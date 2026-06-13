# e2a API v1 — clean redesign

| | |
|---|---|
| **Status** | Proposed |
| **Date** | 2026-06-13 |
| **Audience** | e2a maintainers; SDK + MCP authors; downstream agent developers |
| **Role** | Reshape the existing `/api/v1` surface into one clean, consistent, agent-first contract — with the OpenAPI spec as the single source of truth that the MCP server, SDKs, and docs are generated from and drift-tested against. |
| **Related** | `docs/api.md` (current REST surface) · `docs/events.md` (webhook events) · PR #206 (MCP↔API drift) |

## 1. Problem statement

e2a's `/api/v1` surface grew organically and has drifted in ways that make
it harder than it should be for an agent (or a developer wiring one up) to
use confidently. Concretely, observed in the codebase today:

* **Inconsistent action placement.** Outbound send is top-level
  (`POST /api/v1/send`) while reply/forward are nested under the agent
  (`POST /api/v1/agents/{email}/messages/{id}/reply|forward`). Same concept
  ("the agent emits a message"), two shapes.
* **Two ways to address a message.** `GET /api/v1/messages/{id}` (flat) and
  `GET /api/v1/agents/{email}/messages/{id}` (nested) both exist.
* **Two HITL approve/reject mechanisms.** Top-level magic-link
  `GET/POST /api/v1/approve|reject|pending` (for humans) *and* nested
  `POST /api/v1/agents/{email}/messages/{id}/approve|reject`. Two routes for
  one state transition.
* **MCP ↔ API drift.** The MCP tools and the REST API are separate
  codebases (Go API, TS MCP) with no shared contract; gaps have already
  surfaced (PR #206; `send` lacks `from`/`reply_to`; `create_agent` only
  makes shared-domain agents).
* **Redundant state.** `agent_mode ∈ {cloud, local}` is derivable from
  "is a webhook configured?" and forces an onboarding coupling
  (cloud ⇒ webhook required) that dead-ends agent creation.
* **Sender identity.** Outbound `From` is always the shared relay
  (`agent@send.e2a.dev`), never the agent's own verified address, so human
  replies to a custom-domain agent bounce (no receivable From/Reply-To).
* **Auth is static-key-only.** No OAuth path for hosted MCP connections.
* **Docs are human-first.** Knowledge is split across README, the e2a skill,
  and SDK READMEs; there's no canonical agent-readable contract doc.

**Context that makes now the right time:** e2a is in beta, has **no external
API consumers, and makes no stability promise**. The only live consumer is
AgentDrive's feedback loop (internal, updated in lockstep). So we redesign
**in place under `/api/v1`** — break freely, no `/v2`, no compatibility
shims, no deprecation windows. This is the cheapest this change will ever be.

## 2. Goals and non-goals

**Goals**

* One coherent resource model with consistent verb/path/naming conventions.
* A single error envelope, one pagination scheme, and idempotency on all
  unsafe writes.
* The unified, ergonomic fields we already want: agent `address`
  (local-part *or* full email), optional webhook (no `agent_mode`),
  `from`/`reply_to` on outbound, custom-domain sender identity.
* **OpenAPI as the single source of truth**, with the MCP tools + SDKs
  generated/validated from it and a CI drift test across the Go↔TS split.
* Auth: API key (self-host) **and** OAuth 2.1 hosted-MCP (first-class).
* Agent-first docs (`e2a.md` + `llms.txt` + `setup.md` + `auth.md`),
  served by the binary so self-hosters get them too.

**Non-goals**

* No `/v2`, no compat layer, no migration window (no users to protect).
* Not changing the underlying delivery/threading engine — the resource
  *model* is mostly right; this is contract + consistency, not a rewrite.
* Not renaming things for taste — every change anchors to a concrete
  ergonomics or consistency win.

## 3. Principles

1. **Resources are nouns; transitions are sub-resources or PATCH** — not
   ad-hoc verbs scattered at different levels.
2. **One canonical place per concept.** A message has exactly one address;
   an action (send/reply/approve) has exactly one route.
3. **The spec is the contract.** Hand-written drift is designed out: OpenAPI
   generated from the Go handlers (or hand-authored and validated against
   them), MCP + SDK request/response shapes validated against it in CI.
4. **Conservative, fail-closed defaults**; explicit over implicit.
5. **Agent ergonomics first** — minimal required fields, forgiving inputs
   (`address` accepts local-part or full email), machine-branchable errors.

## 4. Target resource model

Canonical resources under `/api/v1`:

| Resource | Routes (target) |
|---|---|
| **agents** | `GET/POST /agents` · `GET/PATCH/DELETE /agents/{address}` · `POST /agents/{address}/test` |
| **messages** (per agent, the inbox) | `GET /agents/{address}/messages` · `GET …/messages/{id}` · `PATCH …/messages/{id}` (labels/read) |
| **outbound** (unified) | `POST /agents/{address}/messages` — one endpoint for *new thread, reply, and forward*, disambiguated by body (`in_reply_to` / `forward_of` absent ⇒ new) |
| **conversations** | `GET /agents/{address}/conversations` · `GET …/conversations/{id}` |
| **approvals (HITL)** | `POST /agents/{address}/messages/{id}/approval {decision: approve|reject}` — the one transition; magic-link `GET /approvals/{token}` stays as a thin human alias that resolves to the same handler |
| **domains** | `GET/POST /domains` · `GET/PATCH/DELETE /domains/{domain}` · `POST /domains/{domain}/verify` |
| **webhooks** | `GET/POST /webhooks` · `GET/PATCH/DELETE /webhooks/{id}` · `…/deliveries` · `…/test` · `…/rotate-secret` · `…/redeliver-since` |
| **events** (delivery log) | `GET /events` · `GET /events/{id}` · `POST /events/{id}/redeliver` |
| **account** | `GET /account` (replaces `/info` + `/users/me/limits`) · `GET /account/export` · `DELETE /account` · signing-secrets CRUD |

### Key contract decisions

1. **Agent address is the identifier and is forgiving.** `create`/path accept
   a bare local-part (`support`, on the account's default/ös chosen domain) or
   a full email (`support@agentdrive.run`); presence of `@` disambiguates.
   Path-encode the address. This also unifies the MCP `create_agent` field.
2. **Drop `agent_mode`.** Inbound is always persisted + pollable (`GET
   …/messages`) + streamable (`listen`). A `webhook_url` (or `webhooks[]`)
   is an *optional push target*. `cloud`/`local` cease to exist; behavior is
   derived from whether a webhook is configured. Removes the create dead-end.
3. **Outbound is one endpoint.** `POST /agents/{address}/messages` with a
   body carrying `to`, `subject`, `body`/`html`, optional `in_reply_to`
   (reply), `forward_of` (forward), `cc`/`bcc`, `attachments`,
   `idempotency_key`, and — new — `from` (defaults to the agent address) and
   `reply_to`. Eliminates the top-level `/send` vs nested `/reply` split.
4. **Custom-domain sender identity.** When the agent's domain is
   *sending-verified*, outbound `From` = the agent's own address (DKIM
   already signs the custom domain). Domain verification programmatically
   registers the SES sending identity via **BYODKIM** (reuse e2a's existing
   per-domain key) and flips a `sending_verified` flag; the `From` switch
   gates on it. (See §5 of `sender.go`; separate sender-identity slice.)
5. **One HITL transition.** Collapse the nested approve/reject + the
   top-level magic-link into a single `approval` sub-resource; the magic
   link is a convenience GET that calls the same code.
6. **One error envelope** (audit current handlers and standardize):
   `{ "error": { "code": "MACHINE_BRANCHABLE", "message": "human text",
   "details": {…} } }`, with stable `code` values documented in the spec.
7. **One pagination scheme** — opaque cursor (`?cursor=…&limit=…`) returning
   `{ items: [...], next_cursor: "…"|null }` across all list endpoints.
8. **Idempotency** — `Idempotency-Key` header (or body key) honored on all
   POSTs with side effects (send, create agent, webhook create, redeliver).

## 5. Auth model

* **API key** (`E2A_API_KEY`) — unchanged; the self-host default.
* **Agent JWT** (`identity_assertion` → short-lived access token) — unchanged.
* **OAuth 2.1 (PKCE + refresh) — new, first-class for hosted MCP.** Lets a
  user connect from Claude/ChatGPT with no pasted key and per-agent scoping.
  Mirrors the AgentDrive MCP-OAuth design. API keys remain supported so
  self-host isn't forced onto OAuth.
* **HITL magic-link tokens** — unchanged, scoped to a single approval.

## 6. Source of truth & drift control

* **OpenAPI 3.1 is authoritative.** Either generate it from the Go handlers
  or hand-author it and add a test asserting every route/param/response
  matches the running server (schemathesis-style or a snapshot diff).
* **MCP generated/validated from the spec.** The TS MCP tools' request
  bodies are validated against the OpenAPI request schemas in CI — the
  cross-language anti-drift test (the `RegisterAgentRequest` parity check
  generalized to every tool↔endpoint pair).
* **SDKs (py/ts) generated from the spec** (or contract-tested against it).
* Result: "MCP consistent with the API" becomes structural, not manual.

## 7. Agent-first docs

* Canonical **`e2a.md`** (frontmatter'd skill/contract), **`llms.txt`** at
  root pointing to it + `setup.md` + `auth.md`, **served by the binary**
  (one source, two channels) so self-host installs expose them too.
* `api.md` becomes generated from the OpenAPI spec, not hand-maintained.

## 8. Current → ideal gap table

| Current | Disposition | Target |
|---|---|---|
| `POST /send` | **move** | `POST /agents/{address}/messages` (new-thread case) |
| `POST /agents/{e}/messages/{id}/reply` | **fold** | `POST /agents/{address}/messages` + `in_reply_to` |
| `POST /agents/{e}/messages/{id}/forward` | **fold** | `POST /agents/{address}/messages` + `forward_of` |
| `GET /messages/{id}` (flat) | **remove** | use `GET /agents/{address}/messages/{id}` |
| `GET/POST /approve`, `/reject`, `/pending` | **collapse** | `POST …/messages/{id}/approval` + magic-link GET alias |
| `POST …/messages/{id}/approve|reject` | **collapse** | same `approval` sub-resource |
| `GET /info`, `GET /users/me/limits` | **merge** | `GET /account` |
| `/users/me/*` | **rename** | `/account/*` |
| `create_agent` (shared-domain only, `agent_mode`) | **change** | `address` field, optional webhook, no mode |
| `POST /send` body | **extend** | add `from`, `reply_to` |
| `agent_mode` column + CHECK | **drop** | webhook presence derives behavior |
| outbound `From` always relay | **change** | agent address when `sending_verified` |
| error envelopes / pagination (per-handler) | **standardize** | one envelope, cursor pagination |
| MCP tools (hand-aligned) | **regenerate** | from OpenAPI + drift test |
| no OAuth | **add** | OAuth 2.1 hosted MCP |

## 9. Rollout (in place, no compat)

Break `/api/v1` directly; update all consumers in lockstep.

* **Slice 1 — Contract + conventions.** Author the OpenAPI spec for the
  target surface; standardize the error envelope + cursor pagination +
  idempotency helpers; add the spec↔server test. (No behavior change yet
  beyond envelope/pagination.)
* **Slice 2 — Resource cleanup.** Unify outbound under
  `POST /agents/{address}/messages` (send/reply/forward); single message
  address; collapse HITL to `approval`; `/account`. Update MCP + SDKs from
  the spec; update AgentDrive's feedback loop (`feedback_api.sh`/comms is
  unaffected; the e2a `send`/`reply` calls move).
* **Slice 3 — Agent model.** `address` unification; drop `agent_mode`;
  optional webhook. Migration drops the column + CHECK.
* **Slice 4 — Sender identity.** `SenderIdentityProvider` (SES BYODKIM) +
  `sending_verified` + custom-domain `From`/`Reply-To`. Unblocks
  customer-reply→reopen for AgentDrive.
* **Slice 5 — OAuth hosted MCP.** OAuth 2.1 (PKCE + refresh), per-agent
  scope; keep API keys.
* **Slice 6 — Agent-first docs.** `e2a.md`/`llms.txt`/`setup.md`/`auth.md`,
  binary-served; `api.md` generated from the spec.

Each slice is independently shippable; 1–2 deliver most of the "clean and
consistent" win.

## 10. Open questions

1. **Default domain for bare local-part agents** — when `address` has no `@`,
   which domain (account default? shared `agents.e2a.dev`? require one once a
   custom domain is verified)?
2. **OpenAPI: generate-from-Go vs hand-author + validate** — pick the
   mechanism that best fits the Go stack (e.g. `swaggo` annotations already
   present in `api.go`?) vs a hand-authored spec with a conformance test.
3. **Magic-link alias shape** — keep `GET /approvals/{token}` returning HTML
   for humans while the JSON transition lives at the `approval` sub-resource?
4. **SES identity provisioning failure UX** — how to surface async
   `sending_verified` pending/failed state to the agent (poll endpoint?
   field on the domain resource?).
