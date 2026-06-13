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
| **agents** | `GET/POST /agents` · `GET/PATCH/DELETE /agents/{address}` · `POST /agents/{address}/test` (top-level, keyed by full email; create enforces caller owns the verified domain) |
| **domain's agents** (filtered view) | `GET /domains/{domain}/agents` — list agents on a domain (management view; not a separate identity namespace) |
| **messages** (per agent, the inbox) | `GET /agents/{address}/messages` · `GET …/messages/{id}` · `PATCH …/messages/{id}` (labels/read) |
| **outbound** (unified) | `POST /agents/{address}/messages` — one endpoint for *new thread, reply, and forward*, disambiguated by body (`in_reply_to` / `forward_of` absent ⇒ new) |
| **conversations** (derived thread view) | `GET /agents/{address}/conversations` · `GET …/conversations/{id}` |
| **stream** (inbound transport) | `GET /agents/{address}/ws` — WebSocket; first-class + documented (today it's side-registered + mode-gated) |
| **approvals (HITL)** | `POST /agents/{address}/messages/{id}/approval {decision: approve|reject}` — the one transition; magic-link `GET /approvals/{token}` stays as a thin human alias that resolves to the same handler |
| **domains** | `GET/POST /domains` · `GET/PATCH/DELETE /domains/{domain}` · `POST /domains/{domain}/verify` |
| **webhooks** | `GET/POST /webhooks` · `GET/PATCH/DELETE /webhooks/{id}` · `…/deliveries` · `…/test` · `…/rotate-secret` · `…/redeliver-since` |
| **events** (delivery log) | `GET /events` · `GET /events/{id}` · `POST /events/{id}/redeliver` |
| **account** | `GET /account` (replaces `/info` + `/users/me/limits`) · `GET /account/export` · `DELETE /account` · signing-secrets CRUD |

### Resource relationships

* **agent ↔ domain.** An agent *is* a full email; its domain is a property
  (`agent.domain`), constrained at create-time to a verified domain the
  caller owns. Agents stay a **top-level** resource (`/agents/{email}`) — the
  email already encodes the domain, so nesting under `/domains/{domain}/…`
  would duplicate it in the path and burden every per-agent operation.
  `/domains/{domain}/agents` exists only as a filtered listing.
* **conversation ↔ message (1 : N, derived).** A *message* is one email
  (inbound or outbound). A *conversation* is a thread — the set of messages
  sharing a `conversation_id`, scoped to an agent. There is **no
  `conversations` table**; it's a read-only aggregate over
  `messages.conversation_id` (`store.go`: "thin read layer"). Threading
  establishes membership: inbound replies join via `In-Reply-To`/`References`;
  an outbound message with `in_reply_to` joins its thread, otherwise the
  server assigns a fresh `conversation_id`. Messages are canonical;
  conversations are the inbox/thread view over them.

### Key contract decisions

1. **Agent address is the identifier and is always a full email.** `create`
   and every path require the full address (`support@agentdrive.run`) — no
   bare local-part, no `@`-disambiguation, no implicit default domain.
   Explicit and unambiguous. The email's domain MUST be a verified domain the
   caller owns (enforced at create). Path-encode the address. The MCP
   `create_agent` field follows the same rule.
2. **Drop `agent_mode` AND the per-agent `webhook_url`.** Inbound is always
   persisted and consumable via three transports over the same store, with no
   mode to choose:
   * **Poll** — `GET /agents/{address}/messages` (+ conversations).
   * **Stream** — `GET /agents/{address}/ws` (WebSocket; lightweight
     notification → fetch via REST). Promote this from a side-registered,
     mode-gated endpoint to a first-class, documented transport.
   * **Push** — `/api/v1/webhooks` event subscriptions (see decision 2a).

   `agent_identities.webhook_url` is already deprecated in-code
   (`X-E2A-Deprecation` header, sunset 2026-12-01) in favor of `/webhooks`;
   we **remove it outright**. With both `agent_mode` and `webhook_url` gone,
   `cloud`/`local` has nothing left to distinguish. Removes the create
   dead-end (no forced webhook at creation).

2a. **`/api/v1/webhooks` is the single push mechanism.** It's the existing
   multi-subscriber resource: event-type subscriptions with filters
   (`agent_ids`, `conversation_ids`, `labels`), a per-webhook HMAC secret
   (`X-E2A-Signature: t=…,v1=…`), a deliveries log, retries, `rotate-secret`,
   `redeliver-since`, `test`, and auto-disable. Keep it as-is in shape;
   the only change is that it's now the *only* push path (no agent URL field).
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

### Webhook delivery: build vs. framework (decision)

**Keep delivery hand-rolled — do NOT adopt an external webhook
framework/service (Svix/Convoy/Hookdeck) for v1.** A framework relocates
risk (adds infra + a service in the data path + vendor coupling) rather than
removing it, and it fights e2a's self-host + provider-agnostic posture for a
modest event volume. The domain-specific parts are already built and low-risk
(HMAC signing, subscription filters, the event→delivery model). Decision:

* **Semantics stay hand-rolled and ours:** subscriptions + filters, HMAC
  signing (+ rotation grace), the event vocabulary, SSRF/URL validation.
* **Run the delivery *worker* on [River](https://riverqueue.com)** — a Go,
  Postgres-backed job queue (no Redis, no new service; just tables in the
  existing DB). It owns the concurrency-heavy, bug-prone mechanics:
  atomic claim (`FOR UPDATE SKIP LOCKED`), **transactional enqueue** (enqueue
  the delivery job in the same tx that writes the event — no lost/rolled-back
  jobs), retries-with-backoff, max-attempts, dead-letter, unique-jobs
  idempotency. Replaces the hand-rolled poll/lease/`next_retry_at` loop (the
  part most prone to subtle bugs). e2a keeps the `Work()` body (sign + POST +
  record outcome) and the auto-disable policy.
* **Pin correctness with an adversarial test matrix** (the real bug
  insurance, required regardless):
  * **at-least-once:** kill the worker mid-send → redelivered, never lost.
  * **idempotency/dedup:** stable delivery id; same event never double-fires.
  * **retry/backoff:** schedule matches spec, capped, dead-letters after N.
  * **signature:** correct HMAC + rotation grace (two valid sigs) + clock-skew window.
  * **isolation:** one permanently-failing subscriber can't block others or grow unbounded.
  * **SSRF:** HTTPS-only, no private IPs.
  * **ordering:** document the guarantee (none — dedup receiver-side) and test to it.
* **Revisit a self-hostable gateway (Convoy, Go+Postgres) only on a scale
  trigger** — high fan-out, strict per-subscriber rate limiting, or wanting a
  prebuilt delivery dashboard. Not a v1 concern.

## 5. Auth model

* **API key** (`E2A_API_KEY`) — unchanged; the self-host default.
* **Agent JWT** (`identity_assertion` → short-lived access token) — unchanged.
* **OAuth 2.1 (PKCE + refresh) — new, first-class for hosted MCP.** Lets a
  user connect from Claude/ChatGPT with no pasted key and per-agent scoping.
  Mirrors the AgentDrive MCP-OAuth design. API keys remain supported so
  self-host isn't forced onto OAuth.
* **HITL magic-link tokens** — unchanged, scoped to a single approval.

## 6. Source of truth & drift control

* **OpenAPI 3.1 is authoritative and FRAMEWORK-GENERATED — never
  hand-authored.** Build the HTTP layer on **[Huma](https://huma.rocks)**
  (`danielgtaylor/huma`): each operation is declared with typed Go
  input/output structs, and Huma emits the OpenAPI 3.1 spec *and* validates
  requests from those same definitions — so the handler **is** the contract
  and the spec cannot drift by construction. Pair Huma with **chi** during
  the rewrite (mux→chi; we're reshaping every route anyway). **Delete the
  existing swaggo annotations** — swaggo is OpenAPI 2.0 + comment-driven
  (drift-prone). Rejected alternatives: **ogen** (spec-first = hand-authoring)
  and **goa** (heavier all-in design-DSL framework; Huma gives the same
  no-drift guarantee with a lighter footprint).
* **SDKs are generated** from the spec (OpenAPI Generator) — structurally
  cannot drift; CI regenerates and fails on any diff vs. the committed output.
* **MCP is hand-curated** (for ergonomics) but **contract-locked to the spec**
  by CI tests (below).

#### Anti-drift CI gate (the durable guard #206 deferred)

`#206` is the canonical drift: the MCP `create_agent` zod schema **omitted
`email`** — a field the REST contract + SDK already accepted — so custom-domain
inboxes were uncreatable via MCP. The PR fixed the symptom and explicitly left
"a contract test asserting MCP request bodies validate against the API schema"
as the durable fix. We build it as one CI job, **"contract-drift,"** that makes
this class un-mergeable:

1. **Emit the spec fresh from Huma** in CI (never trust a stale committed
   snapshot) — this is the source of truth everything else validates against.
2. **SDK regen-diff** — regenerate the SDKs from the fresh spec; fail if they
   differ from the committed output (keeps SDKs honest, no drift by construction).
3. **MCP request-validation** — for each tool, validate a representative
   emitted request body against the mapped operation's `requestBody` JSON
   Schema (ajv). Catches extra / renamed / wrong-typed fields.
4. **MCP coverage (the actual #206-preventer)** — assert every property of each
   operation's request schema is *either* exposed by its tool *or* on an
   explicit `intentionallyOmitted` allowlist. When the API gains a field, the
   build fails until the MCP exposes it or the omission is consciously waived.
5. **Tool↔operation map enforced** — a declared `tool → operationId` map; fail
   on a tool mapping to a nonexistent operation, or an operation with no tool
   that isn't on a `noMcp` allowlist (no orphans, no silently-unexposed
   endpoints).

Result: "MCP/SDK consistent with the API" is enforced by the build, not by
review diligence — a #206-style omission can't merge.

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
| `agent_mode` column + CHECK | **drop** | no modes; inbound via poll / `ws` / `/webhooks` |
| `agent_identities.webhook_url` (legacy per-agent webhook, already `X-E2A-Deprecation`'d) | **remove completely** | `/api/v1/webhooks` — the single, first-class push mechanism |
| `/api/v1/webhooks` (subscriber resource) | **keep, elevate to first-class** | canonical push: event subscriptions, filters, HMAC, deliveries, retries |
| `GET /agents/{email}/ws` (side-registered, mode-gated) | **promote** | first-class, documented inbound transport |
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

1. ~~Default domain for bare local-part agents~~ — **resolved:** addresses
   are always full emails (no bare local-part), so there is no default-domain
   question.
2. ~~OpenAPI: generate-from-Go vs hand-author~~ — **resolved:** framework-
   generated via **Huma** (code-as-contract, OpenAPI 3.1 + validation from the
   typed handlers); no hand-authoring, swaggo annotations removed. Open
   sub-point: pick the downstream generators (MCP-tools-from-OpenAPI and the
   py/ts SDK generator — e.g. openapi-generator / Speakeasy / Fern).
3. **Magic-link alias shape** — keep `GET /approvals/{token}` returning HTML
   for humans while the JSON transition lives at the `approval` sub-resource?
4. **SES identity provisioning failure UX** — how to surface async
   `sending_verified` pending/failed state to the agent (poll endpoint?
   field on the domain resource?).
