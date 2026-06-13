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
**in place** — break freely, no compatibility shims, no deprecation windows.
This is the cheapest this change will ever be.

The redesign also **moves the surface to a dedicated host with a clean prefix**:

> **Canonical base URL: `https://api.e2a.dev/v1`**

All API endpoints live on the dedicated `api.e2a.dev` host (mirroring
AgentDrive's `api.agentdrive.run`). The version goes straight on the path as
`/v1` — the host already says "api", so the legacy `/api/v1` double-segment is
dropped. There is no `/v2`: `/v1` is the in-place namespace we keep reshaping
while in beta. (Distinct hosts, distinct jobs: `api.e2a.dev` = the REST/MCP
control plane; `send.e2a.dev` = the SMTP relay; `agents.e2a.dev` = the shared
inbound email domain for agents without a custom domain.) Every *target* path
below is relative to `https://api.e2a.dev/v1`; the `/api/v1/...` paths in §1
are the **current** routes being replaced.

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

Canonical resources, all under `https://api.e2a.dev/v1` (paths below are
relative to that base):

| Resource | Routes (target) |
|---|---|
| **agents** | `GET/POST /agents` · `GET/PATCH/DELETE /agents/{address}` · `POST /agents/{address}/test` (top-level, keyed by full email; create enforces caller owns the verified domain) |
| **domain's agents** (filtered view) | `GET /domains/{domain}/agents` — list agents on a domain (management view; not a separate identity namespace) |
| **messages** (per agent; inbound + outbound) | `GET /agents/{address}/messages` (filters incl. `direction`, `status`; held outbound drafts = `status=pending_approval`) · `GET …/messages/{id}` · `GET …/messages/{id}/attachments/{index}` · `PATCH …/messages/{id}` (labels/read) |
| **outbound** (unified) | `POST /agents/{address}/messages` — one endpoint for *new thread, reply, and forward*, disambiguated by body (`in_reply_to` / `forward_of` absent ⇒ new) |
| **conversations** (derived thread view) | `GET /agents/{address}/conversations` · `GET …/conversations/{id}` |
| **stream** (inbound transport) | `GET /agents/{address}/ws` — WebSocket; first-class + documented (today it's side-registered + mode-gated) |
| **approvals (HITL)** | `POST /agents/{address}/messages/{id}/approval {decision: approve\|reject}` — the one transition (agents; API-key/OAuth). Held drafts are listed via `GET …/messages?status=pending_approval` and read via the message GET (a held draft is just a message). Human magic link: `GET /approvals/{token}` renders an **HTML confirmation page with NO side effect** (prefetch-safe), whose buttons `POST /approvals/{token} {decision}` into the same transition (token = single-use, short-TTL capability). **Never a mutating GET** — email scanners/prefetchers would auto-trigger it. |
| **domains** | `GET/POST /domains` · `GET/PATCH/DELETE /domains/{domain}` · `POST /domains/{domain}/verify` (ownership + nudges a sending-identity re-check). The domain resource carries two independent statuses: `verified` (inbound/ownership, DNS TXT) and `sending_status ∈ {none,pending,verified,failed}` + `sending_error?` + `dns_records` + `last_checked_at?` (async SES sending identity — see §4 decision 4). `GET /domains/{domain}` is the poll target; no separate status endpoint. |
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
   * **Push** — `/v1/webhooks` event subscriptions (see decision 2a).

   `agent_identities.webhook_url` is already deprecated in-code
   (`X-E2A-Deprecation` header, sunset 2026-12-01) in favor of `/webhooks`;
   we **remove it outright**. With both `agent_mode` and `webhook_url` gone,
   `cloud`/`local` has nothing left to distinguish. Removes the create
   dead-end (no forced webhook at creation).

2a. **`/v1/webhooks` is the single push mechanism.** It's the existing
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
4. **Custom-domain sender identity (async).** When the agent's domain is
   *sending-verified*, outbound `From` = the agent's own address (DKIM
   already signs the custom domain). Domain verification programmatically
   registers the SES sending identity via **BYODKIM** (reuse e2a's existing
   per-domain key). SES verification is **async**, so the domain carries
   `sending_status ∈ {none,pending,verified,failed}` (+ `sending_error?`,
   `dns_records`, `last_checked_at?`); the `From` switch gates on
   `sending_status == verified`. Pending→verified is driven by a
   **River-scheduled reconciler** polling SES `GetEmailIdentity`;
   `POST /domains/{domain}/verify` forces an immediate re-check; optionally a
   `domain.sending_verified` / `domain.sending_failed` **webhook event** lets
   agents skip polling. `failed` carries an actionable reason + the DNS to fix.
5. **One HITL transition, prefetch-safe.** Collapse the nested approve/reject
   AND the top-level magic-link into a single `approval` sub-resource
   (`POST …/messages/{id}/approval {decision}`). The human magic link is
   `GET /approvals/{token}` rendering an **HTML confirmation page with NO
   side effect**; its buttons `POST /approvals/{token} {decision}` into the
   same transition. **Never a mutating GET** — email scanners/link-prefetchers
   would auto-approve/reject. Token = single-use, short-TTL capability.
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
  self-host isn't forced onto OAuth. **Two scopes drive the MCP tier split
  (§6a):** `scope=agent` (runtime/inbox tools, bound to one agent) and
  `scope=admin` (provisioning: agent/domain/webhook/event management). A
  deployed support agent runs on an `agent`-scoped token; the operator sets it
  up once with an `admin`-scoped connection. API-key (self-host) gets both.
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

## 6a. MCP tool surface & API correspondence

The MCP server is the **primary** way an agent (and its operator) drives e2a —
the SDK and raw REST exist for back-end/programmatic use, but the everyday
"stand up and run a support agent" journey is MCP. Two principles govern it:

* **Hosted MCP (OAuth) is first-class, and the tool surface is
  transport-independent.** The *same* tools are exposed whether the server runs
  over **stdio with an `E2A_API_KEY`** (self-host / local) or as the **hosted
  Streamable-HTTP server authenticated by OAuth 2.1** (`https://api.e2a.dev/mcp`;
  PKCE + refresh, per-agent scope — §5). Auth is a transport/connection concern,
  **never a tool argument**: no tool takes a key, and identity is resolved from
  the bearer/OAuth token. A user connecting from Claude/ChatGPT pastes nothing.
* **Curated for ergonomics, contract-locked to the spec.** Tools stay
  hand-written (intent-revealing names, forgiving inputs, directive errors) but
  each maps to a declared `operationId`, and the §6 *contract-drift* gate
  enforces the mapping + field coverage. Several tools may map to one operation
  (send/reply/forward; approve/reject), and every tool's request body must
  validate against its operation's schema. The `noMcp` / `intentionallyOmitted`
  allowlists the gate checks against are defined at the end of this section.

**Hosting convention — `https://api.e2a.dev/mcp` (a path on the API host, not a
`mcp.` subdomain).** This matches AgentDrive (`api.agentdrive.run/mcp`) and the
§1 rule that all API surface lives on `api.e2a.dev`, and keeps the MCP endpoint,
the REST API, and the OAuth authorization server **same-origin** — so
`/.well-known/oauth-protected-resource` discovery and the resource↔AS
relationship need no cross-origin hop. The MCP server stays a separate process;
the ingress path-routes `/mcp` to it, so that deployment detail never leaks into
the public URL. **Config change:** the current `MCP_ALLOWED_HOSTS` /
`MCP_PUBLIC_URL` defaults point at `mcp.e2a.dev` — retarget them to
`api.e2a.dev` (DNS-rebinding allow-host) and `https://api.e2a.dev/mcp`.

### The canonical journey — AgentDrive standing up `support@agentdrive.run`

This is exactly the flow we ran by hand; each step is one or two tool calls.

1. **Connect.** Add the hosted connector `https://api.e2a.dev/mcp` in Claude →
   OAuth 2.1 grant, no key pasted. (Self-host: stdio server + `E2A_API_KEY`.)
2. **Bring the domain.** `register_domain {domain:"agentdrive.run"}`
   → `POST /domains`; returns the DNS records (MX/TXT/DKIM) to publish.
3. **Publish DNS, then verify.** `verify_domain {domain:"agentdrive.run"}`
   → `POST /domains/{domain}/verify`. Flips inbound `verified` **and** kicks off
   async SES sending-identity registration (BYODKIM — §4 decision 4).
4. **Wait for sending-verified.** `get_domain {domain:"agentdrive.run"}`
   → `GET /domains/{domain}`; poll `sending_status` until `verified` (or
   subscribe the `domain.sending_verified` webhook event). Only then is outbound
   `From` the agent's own address instead of the relay.
5. **Create the agent.** `create_agent {address:"support@agentdrive.run",
   name:"AgentDrive Support"}` → `POST /agents`. Full email required; its domain
   must be a verified domain we own. No mode, no forced webhook.
6. **(Optional) gate replies behind a human.** `update_agent {hitl_enabled:true,
   hitl_ttl_seconds:…, hitl_expiration_action:"reject"}` → `PATCH /agents/{address}`.
7. **(Optional) push instead of poll.** `create_webhook {url,
   events:["email.received", …]}` → `POST /webhooks`; persist the returned
   `signing_secret` (shown once).
8. **Run the loop.** Inbound: `list_messages`/`get_message`. Outbound:
   `reply_to_message`/`send_email`. HITL on: `list_pending_approvals` →
   `approve_message`/`reject_message`.

### Tool catalogue (target surface, mapped to `/v1`)

Paths are relative to `https://api.e2a.dev/v1`. `{address}` resolves per call:
explicit `address` arg → `E2A_AGENT_ADDRESS` env (stdio) / token-bound agent
(OAuth) → single-agent auto-resolve → directive error. The per-tool
`agent_email` arg is renamed **`address`** (always a full email).

**Two tiers, scope-gated.** The surface splits by persona, and hosted MCP
exposes each tier per OAuth scope (§5):

* **Runtime / inbox** (`scope=agent`) — what a deployed agent uses every turn:
  `whoami`, `list_agents`, `list_messages`, `get_message`, `get_attachment`,
  `update_message_labels`, `list_conversations`, `get_conversation`,
  `send_email`, `reply_to_message`, `forward_message`, `list_pending_approvals`,
  `approve_message`, `reject_message`.
* **Admin / setup** (`scope=admin`) — provisioning, done once by the operator
  (the AgentDrive setup journey above): agent create/update/delete, all of
  domains, all of webhooks, all of events.

A runtime-scoped token therefore sees ~14 tools, not ~29 — a smaller decision
space and no way for a support agent to `delete_domain`. Self-host (API key)
sees both tiers. The drift-gate map records each tool's tier next to its
`operationId`.

**Agents**

| Tool | Key params | → operation | Notes |
|---|---|---|---|
| `whoami` | — | `GET /account` (+ `GET /agents`) | Caller identity + default agent; directive error on 0/2+ agents with none defaulted. |
| `list_agents` | — | `GET /agents` | |
| `create_agent` | `address`*, `name?` | `POST /agents` | **Changed:** drop `slug`/`agent_mode`/`webhook_url`; full email on a verified owned domain. (The #206 coverage target — `address` must be exposed.) |
| `update_agent` | `address?`, `name?`, `hitl_enabled?`, `hitl_ttl_seconds?`, `hitl_expiration_action?` | `PATCH /agents/{address}` | **Changed:** drop `agent_mode`/`webhook_url`. |
| `delete_agent` | `address?`, `confirm:true`* | `DELETE /agents/{address}` | Destructive guard kept. |

**Messages (inbound + outbound, one collection)**

| Tool | Key params | → operation | Notes |
|---|---|---|---|
| `list_messages` | filters (`status`,`from`,`subject_contains`,`labels`,`since/until`,`conversation_id`,`direction?`), `cursor`,`limit`,`address?` | `GET /agents/{address}/messages` | Cursor pagination (§4.7). |
| `get_message` | `message_id`*,`address?` | `GET /agents/{address}/messages/{id}` | Flat `GET /messages/{id}` removed — one address. Also reads held outbound drafts. |
| `get_attachment` | `message_id`*,`index`*,`address?` | `GET /agents/{address}/messages/{id}/attachments/{index}` | **Changed:** dedicated endpoint (was a full-message re-fetch). |
| `update_message_labels` | `message_id`*,`add_labels?`,`remove_labels?`,`address?` | `PATCH /agents/{address}/messages/{id}` | Labels/read folded into the message PATCH. |
| `send_email` | `to`*,`subject`*,`body`*,`html?`,`cc/bcc?`,`attachments?`,`from?`,`reply_to?`,`idempotency_key?`,`address?` | `POST /agents/{address}/messages` | New-thread case. **New `from`,`reply_to`** (decision 3 / #206 coverage). |
| `reply_to_message` | `message_id`*,`body`*,`html?`,`reply_all?`,`cc/bcc?`,`attachments?`,`reply_to?`,`idempotency_key?`,`address?` | `POST /agents/{address}/messages` | Sets `in_reply_to`. |
| `forward_message` | `message_id`*,`to`*,`body?`,`cc/bcc?`,`attachments?`,`idempotency_key?`,`address?` | `POST /agents/{address}/messages` | Sets `forward_of`. |

> send/reply/forward all map to the single `sendMessage` operation; the body's
> `in_reply_to`/`forward_of` selects the mode. Kept as three tools for intent
> clarity — coverage check #4 treats them as jointly covering `sendMessage`.

**Conversations** — `list_conversations` → `GET /agents/{address}/conversations`;
`get_conversation {conversation_id}` → `GET …/conversations/{id}`.

**Approvals (HITL)**

| Tool | Key params | → operation | Notes |
|---|---|---|---|
| `list_pending_approvals` | `address?` | `GET /agents/{address}/messages?status=pending_approval&direction=outbound` | Curated filter over the messages list. |
| `approve_message` | `message_id`*, optional overrides (`subject/body/html/to/cc/bcc/attachments`), `idempotency_key?`,`address?` | `POST …/messages/{id}/approval {decision:"approve", …overrides}` | |
| `reject_message` | `message_id`*,`reason?`,`address?` | `POST …/messages/{id}/approval {decision:"reject"}` | approve+reject → one `approval` operation, two ergonomic tools. |

> Read the draft with `get_message` (a held draft is just a message);
> `get_pending_message` is **removed**. The human magic-link
> `GET /approvals/{token}` is a **browser** flow, not a tool (`noMcp`).

**Domains** — `list_domains` → `GET /domains`; **`get_domain {domain}`** (new)
→ `GET /domains/{domain}` (surfaces `verified` + `sending_status`/
`sending_error`/`dns_records` — the poll target); `register_domain {domain}`
→ `POST /domains`; `verify_domain {domain}` → `POST /domains/{domain}/verify`
(ownership + nudges the SES re-check); `delete_domain {domain, confirm:true}`
→ `DELETE /domains/{domain}`.

**Webhooks & events** — 1:1 with the resources: `list_webhooks` / `get_webhook`
/ `create_webhook` / `update_webhook` / `delete_webhook(confirm)` /
`rotate_webhook_secret` / `test_webhook` / `list_webhook_deliveries`, and
`list_events` / `get_event` / `redeliver_event` → the `…/webhooks…` and
`/events…` operations. `create_webhook` / `rotate_webhook_secret` return
`signing_secret` once.

### `noMcp` / `intentionallyOmitted` (what the §6 gate checks against)

* `GET /agents/{address}/ws` — raw inbound transport, not a tool; MCP clients
  poll `list_messages` or subscribe via webhooks. **`noMcp`.**
* `GET /approvals/{token}` + `POST /approvals/{token}` — human browser
  magic-link; agents use the `approval` transition. **`noMcp`.**
* `GET /account/export`, `DELETE /account`, account/signing-secret CRUD —
  console/operator-only, deliberately out of the agent surface. **`noMcp`**
  (revisit if a managed-operator tool is wanted).
* Internal-only response fields stay on `intentionallyOmitted` so coverage
  check #4 stays green without exposing plumbing.

### Net change vs. today (~33 → ~30 tools, all coverage-checked)

* **Renames:** `agent_email`→`address` (full email) on every tool;
  `get_attachment_data`→`get_attachment`; env `E2A_AGENT_EMAIL`→
  `E2A_AGENT_ADDRESS` (legacy name still accepted).
* **Removed from tools:** `slug`, `agent_mode`, `webhook_url`
  (create/update_agent); `get_pending_message` (use `get_message`);
  flat-message addressing.
* **Added:** `from`/`reply_to` on outbound; `get_domain` (sending_status poll);
  dedicated attachment fetch.
* **Collapsed:** approve/reject → one `approval` operation (two tools);
  send/reply/forward → one `sendMessage` operation (three tools).

### Recommended design updates (beyond a 1:1 port)

The existing surface works but wasn't optimally designed; these are the changes
worth making while we're reshaping the contract anyway, roughly in priority:

1. **Tier + scope-gate the tools (above).** Highest-leverage change: a deployed
   agent shouldn't carry ~30 tools or hold delete-domain power. Cuts the runtime
   decision space ~2× and enforces least-privilege at the token.
2. **Add MCP tool annotations** (`readOnlyHint`, `destructiveHint`,
   `idempotentHint`, `title`) on every tool. Lets clients auto-approve reads,
   flag the three `confirm:true` deletes, and de-risk retries — and it's a
   prerequisite for the Connectors-directory listing. Today none are set.
3. **One pagination shape everywhere.** Current list tools mix `token`/
   `next_token`, `page_size`, and bare `limit`. Standardize on `cursor` + `limit`
   in, `next_cursor` out (mirrors the API's §4.7) — one "get the next page" model.
4. **Surface the structured error `code`.** Return the API envelope's
   machine-branchable `code` (e.g. `domain_not_verified`, `message_not_pending`,
   `sending_not_verified`) in tool errors so agents branch on a code, not on
   prose. Pairs with the §4.6 envelope.
5. **Stop round-tripping attachments as base64 through the model.**
   `get_attachment` should return metadata + a short-lived signed **download
   URL** by default, with inline base64 only on explicit request for small
   files — removing the silent 2 MB decode cap (a current footgun) and the token
   cost of streaming binaries through context. (Send side keeps small inline
   base64; a presigned **upload** URL is the symmetric future step.)
6. **Fold delivery debugging into events.** `get_event` already carries the
   per-webhook `delivery_status`; drop `list_webhook_deliveries` and let
   `list_events {webhook_id, status}` + `get_event` be the one observability
   path. `redeliver_event` stays. (Net −1 tool, one mental model for "did my
   events go out.")
7. **Idempotency-key on every creating tool**, not just send/approve — add it to
   `create_agent`, `create_webhook`, `register_domain` for uniform retry-safety.
8. **(Minor) consistent vocabulary.** `send_email` vs `reply_to_message`/
   `forward_message` mixes "email" and "message." Either standardize the noun
   (`message`) or consciously keep `send_email` because it reads best — a
   deliberate choice, not an accident.

Applying 1 + 6: a runtime agent sees ~14 tools; the full self-host surface ~29.

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
| host + prefix `…/api/v1/*` | **move** | dedicated host `api.e2a.dev`, prefix `/v1` (base `https://api.e2a.dev/v1`) |
| `GET/POST /approve`, `/reject`, `/pending` | **collapse** | `POST …/messages/{id}/approval` + magic-link GET alias |
| `POST …/messages/{id}/approve|reject` | **collapse** | same `approval` sub-resource |
| `GET /info`, `GET /users/me/limits` | **merge** | `GET /account` |
| `/users/me/*` | **rename** | `/account/*` |
| `create_agent` (shared-domain only, `agent_mode`) | **change** | `address` field, optional webhook, no mode |
| `POST /send` body | **extend** | add `from`, `reply_to` |
| `agent_mode` column + CHECK | **drop** | no modes; inbound via poll / `ws` / `/webhooks` |
| `agent_identities.webhook_url` (legacy per-agent webhook, already `X-E2A-Deprecation`'d) | **remove completely** | `/v1/webhooks` — the single, first-class push mechanism |
| `/api/v1/webhooks` (subscriber resource) | **keep, elevate to first-class** | canonical push: event subscriptions, filters, HMAC, deliveries, retries |
| `GET /agents/{email}/ws` (side-registered, mode-gated) | **promote** | first-class, documented inbound transport |
| outbound `From` always relay | **change** | agent address when `sending_verified` |
| error envelopes / pagination (per-handler) | **standardize** | one envelope, cursor pagination |
| MCP tools (hand-aligned, drifting) | **re-curate + lock** | hand-written but mapped to `operationId` + coverage-checked vs. the spec (§6, §6a) |
| no OAuth | **add** | OAuth 2.1 hosted MCP |

## 9. Rollout (in place, no compat)

Break the current `/api/v1` surface directly and move it to
`https://api.e2a.dev/v1`; update all consumers in lockstep.

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
   typed handlers); no hand-authoring, swaggo annotations removed. SDKs are
   generated (OpenAPI Generator); the **MCP surface is hand-curated and
   contract-locked**, not generated (§6a — tool↔`operationId` map + coverage
   gate). Open sub-point: confirm the py/ts SDK generator config under
   OpenAPI Generator.
3. ~~Magic-link alias shape~~ — **resolved:** one transition
   (`POST …/messages/{id}/approval {decision}`); the human magic link is
   `GET /approvals/{token}` → HTML confirmation page (no side effect),
   buttons `POST /approvals/{token}` into the same transition. Never a
   mutating GET (prefetch-safe). See §4 decision 5 + the approvals row.
4. ~~SES identity provisioning failure UX~~ — **resolved:** status lives on
   the domain resource (`sending_status` + `sending_error` + `dns_records` +
   `last_checked_at`); a River reconciler polls SES, `POST /domains/{domain}/verify`
   forces a re-check, and optional `domain.sending_verified/_failed` webhook
   events allow push instead of poll. See §4 decision 4 + the domains row.

All §10 questions are now resolved. Remaining design sub-points (not blockers):
shared-`agents.e2a.dev` carve-out for the "owns a verified domain" rule;
exact backoff schedule + signature-rotation grace window for webhooks.
