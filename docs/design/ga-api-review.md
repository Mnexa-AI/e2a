# e2a GA API/SDK/MCP readiness review — decisions log

| | |
|---|---|
| **Status** | In progress — walkthrough review before freezing `/v1` as the GA contract |
| **Date** | 2026-06-19 |
| **Scope** | Phase 1: API (`/v1`, 39 ops, 9 groups) · Phase 2: SDKs (TS + Python) · Phase 3: MCP tools |
| **Method** | Walk every operation group by group; log each finding + decision here as we go. Nothing is edited in code until the walkthrough is complete — this doc is the execution checklist. |
| **Authoritative source** | `api/openapi.yaml` (Huma-generated) + the Go handlers in `internal/httpapi/` |
| **Branch** | `ga-api-review-fixes` |

### Implementation progress (Slice A — contained struct/handler changes)

- ✅ **I-1** info `version` — `DeploymentInfoView.Version` + `APIVersion` const (single-sourced into huma config). Verified in spec.
- ✅ **A-1/A-2** account identity + `AccountView` rename — `user`/`scope`/conditional `agent_address`; `whoami` now authenticates any scope. Verified.
- ✅ **D-4** `registerDomain.domain` required · **D-1** `sending_status` enum · **D-5** `deleteDomain ?confirm=DELETE` (+ tests updated). Verified, `make spec` clean, `go test ./internal/httpapi` green.
- ✅ **agents** — AG-5 (createAgent→AgentView), AG-1/2 (drop `slug`, `email` required, shared-domain via email domain), AG-6 (`?confirm=DELETE`, checked after ownership), AG-7 (update input enums → bogus now 422). Tests + scope_test + contract scenarios updated. Verified.
- ✅ **messages** — MSG-1 (`status`→`read_status`, field + filter), MSG-3 (to/subject/body required → 422; reply body; forward to+body), MSG-9 (unified `SendResultView`; `message_id` now the e2a id + `provider_message_id`/`sent_as`/`method` enum/`edited` *bool; plumbed `OutboundResult.ProviderMessageID`/`SentAs`), MSG-10 (`RejectRequest`), MSG-11 (`AuthVerdict`), MSG-12 (`auth_headers` optional). Tests + scenarios + e2e updated. Verified.
- ✅ **webhooks** — WH-1 (`url`+`events` required), WH-2 (events item-enum on create/update/test, 12 values; unknown → 422), WH-3 (`signing_secret` removed from `WebhookView`; new `CreateWebhookResponse` carries it on create only; rotate keeps its own), WH-6 (`rotateSecretResponse` / `TestWebhookResponse` / `RedeliverEventRequest`). Tests updated. Verified.
- ✅ **A-3 (nil→[] sweep)** — added generic `orEmpty[T]` + `nullable:"false"` across all response views (message to/cc/reply_to/labels, AgentView inbound_allowlist, DomainView sending_dns_records, Conversation participants/labels/messages, WebhookView/CreateWebhookResponse events, WebhookFiltersView, RedeliverView deliveries, UpdateMessageResultView labels, request attachments) + top-level `UserExport.*` coalesced in the handler. **Exception (documented, F7 GDPR full-row dump):** the *nested* export-row internals on the raw `identity.Message`/`identity.AgentIdentity` schemas — `Message.*` nullable arrays, `Message.auth` (the `Result` schema), and `AgentIdentity.inbound_allowlist` — keep their raw shape. `identity` cannot import the httpapi `AuthVerdict` view type (import cycle), so MSG-11's rename applies to the public message-read views (`MessageView`/`MessageSummaryView` → `AuthVerdict`, done); the export row's `Message.auth` is the sole remaining `Result` reference and is intentionally left raw.
- ✅ **Slice B (event vocab rename)** — `email.approved`→`email.approval_accepted`, `email.rejected`→`email.approval_rejected`. Single-sourced consts in `webhookpub/event.go` (emission auto-updates) + 6 hardcoded enum-tag sites (EventJSON.type + 5 webhook sites) + e2e tests + `docs/events.md`. **Note:** pre-existing stored events keep their old `type` strings (historical; server reads strings without response-enum validation; no users to migrate, per design §1).
- ✅ **Slice C (path `{address}`→`{email}`)** — all 10 per-agent routes + the chi WS route renamed (placeholder + `path:` tag + `chi.URLParam`); Go field name `Address` kept. The suppression recipient `/v1/account/suppressions/{address}` stays `{address}` (it's a recipient, not the agent). `spec_test` expectations updated. Test URLs pass real emails → unaffected.
- ✅ **Slice D (real pagination)** — keyset cursor on `(created_at, address)` for **suppressions** (A-5: store `ListSuppressions` + dep sig + handler limit+1/cursor) and on `(last_message_at, conversation_id)` for **conversations** (CV-3: `ConversationListFilter.After*` + HAVING keyset + handler). Plus **C-1** (`ConversationSummaryView` timestamps `string`→`time.Time`+`date-time`). Verified against real Postgres (identity + delivery DB tests) + new handler cursor round-trip tests (`TestListSuppressionsPagination`, `TestListSuppressionsBadCursor`).
- ⏳ Remaining: **Slice E** (`make generate-sdk` + SDK hand-layer = the deferred Phase-2 review).

> Flake note: several DB-backed integration tests (`webhookpub` outbox, `agent` identity) false-fail under parallel `go test`; run with `-p 1`. Confirmed passing isolated. Not related to these changes.
- 📌 **Follow-up (Slice C/E):** `tests/e2e-prod` (TS, not CI-gated) creates agents via `slug` — must switch to email-based create when slug was dropped. Tracked with the existing e2e-prod migration item.
- ⏳ Slice B (event vocab rename) · Slice C (path `{address}`→`{email}`) · Slice D (suppressions+conversations real pagination) · Slice E (SDK regen + hand-layer).

> **Global forward-compat rule (do not regress):** response enums are safe **only**
> because `sdks/python/scripts/strip-enum-validators.py` (generic — strips every
> `*_validate_enum`) keeps Python tolerant of unknown values and TS unions are
> erased at runtime. Keep the strip wired + the cross-SDK contract test green.
> Input/request enums are always safe (early validation).

---

## Phase 1 — API

### Group 1 — `info` + `account`

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **I-1** | post-GA | `GET /v1/info` has no `version` field | **FIX** — add `version` to `DeploymentInfoView` |
| **A-1** | HIGH | `GET /v1/account` (`whoami`) returns only plan/limits/usage — **no identity** | **FIX** — add `user.email` + `scope` always; `agent_address` populated **only** for agent-scoped tokens (null/omitted for account scope) |
| **A-2** | MED | response type named `LimitsView` (legacy mirror) | **FIX** — rename `LimitsView` → `AccountView` |
| **A-3** | LOW (global) | nullable arrays (`["array","null"]`) force null-checks | **FIX** — coalesce `nil → []` everywhere a list can be absent (export + all views) |
| **A-5** | MED | `listSuppressions` is `Page[T]` but null-cursor single page; suppressions grow unbounded | **FIX** — real cursor pagination (SQL `LIMIT` + live `next_cursor`) |

### Group 2 — `domains`

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **D-4** | MED | `RegisterDomainRequest.domain` is `omitempty` → not required (only field, generates optional) | **FIX** — mark `domain` **required** |
| **D-5** | MED | `deleteDomain` has no confirm guard (destructive: deprovisions SES identity, breaks all agents on domain) | **FIX** — add `?confirm=DELETE` (match `deleteAccount`) |
| **D-1** | MED | `sending_status` is bare `string` (the field the onboarding journey tells agents to poll) | **FIX** — add `enum:"none,pending,verified,failed"` (forward-compat-safe) |
| **D-6** | LOW | 3 DNS shapes: `DNSRecordView{host,value,priority?}` (inbound elem), `DNSRecordsView{mx,txt,dkim}` (wrapper), `SendingDNSRecordView{type,name,value}` (SES passthrough) | **LEAVE (Option C, documented)** — divergence is principled (fixed self-authored set vs variable SES passthrough); renaming risks breaking stored-JSON unmarshal |
| **D-7** | — | should `sending_*` be `outbound_*`? | **KEEP `sending_*`** — domain send-capability is a different axis from message `direction`; matches ESP industry vocabulary |

**Data-flow note (DNS):** `dns_records` (inbound) is computed synchronously in `domainView()` from row columns (MX←`SMTPDomain`, TXT←`verification_token`, DKIM←per-domain key). `sending_dns_records` is async SES passthrough: River worker → `SetSendingStatus(...)` writes SES's `{type,name,value}` into the `sending_dns_records` JSON column → replayed verbatim on read.

### Group 3 — `agents`

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **AG-5** | MED | `createAgent` returns skinny `CreateAgentResponse{id,email,domain}` ≠ `AgentView` (list/get/update) | **FIX** — `createAgent` returns full `AgentView` (one shape across create/get/update/list) |
| **AG-1/2** | MED | `email`-XOR-`slug` polymorphic create; neither required; design said drop slug | **FIX** — drop `slug`; `email` **required**; shared-domain registration detected when email domain == `SharedDomain` (validate local-part as slug, skip ownership check). Shared-domain **is** a GA feature, just `xyz@agents.e2a.dev` |
| **AG-3** | LOW-MED | path param `{address}` vs body/view field `email` — two words, one concept | **FIX** — standardize on **`email`**; rename path param `{address}` → `{email}` (carries into SDK params + MCP tool arg) |
| **AG-4** | LOW | `AgentView` exposes both `id` and `email` (identical today) | **KEEP both** — document `id` is the stable handle, == `email` today, may diverge later |
| **AG-6** | LOW-MED | `deleteAgent` no confirm guard | **FIX** — add `?confirm=DELETE` (uniform across all 3 deletes) |
| **AG-7** | LOW-MED | `UpdateAgentRequest` fields (`hitl_expiration_action`,`hitl_mode`,`inbound_policy`) bare string; response has enums | **FIX** — add input enums to request |
| **AG-8** | — | consolidate `hitl_*`/`inbound_*` into nested objects? | **KEEP FLAT for now** — PATCH simplicity + MCP flat-arg alignment; revisit the HITL/inbound contract after the overall API stabilizes |

### Group 4 — `messages` + outbound actions

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **MSG-3** | MED | `SendEmailRequest`/`ReplyRequest`/`ForwardRequest` have **no required fields** (launch-review item, never landed) | **FIX** — required matrix (RFC-grounded): **send** → `to`,`subject`,`body` required; **reply** → `body` required (to/subject derived); **forward** → `to`,`body` required (subject derived). `html_body` stays optional addition |
| **MSG-1** | MED | generic `status` field actually = inbox read-state (`unread`/`read`/`""`); caused bug B2; sits among 4 `*_status` fields | **FIX** — rename `status` → `read_status` (response field **and** `listMessages` filter param) |
| **MSG-11** | LOW-MED | `MessageView.auth` schema typed `Result` (leaky `emailauth.Result`) — the trust primitive named meaninglessly | **FIX** — rename wire schema `Result` → `AuthVerdict` |
| **MSG-9/6** | LOW-MED | `send` returns `SendResultView`, `approve` returns `ApproveResultView` — divergent; `method` unexplained bare string | **FIX** — one unified `SendResultView` for send/reply/forward/approve/testAgent: `status` enum{sent,pending_approval}, `message_id`, `provider_message_id?`, `sent_as?` enum{own_address,relay}, `method?` enum{smtp,loopback}, `approval_expires_at?`, `edited?` `*bool` (approve-only). `reject` keeps `RejectResultView` (not a send) |
| **MSG-10** | LOW | reject body typed `RejectInputBody` (others are `*Request`) | **FIX** — rename `RejectInputBody` → `RejectRequest` |
| **MSG-12** | LOW | `auth_headers` (raw blob) required even on outbound; `auth` (verdict) optional | **FIX** — make `auth_headers` optional (omit on outbound); `auth`(`AuthVerdict`) is the primary verdict |

**`method` semantics:** `smtp` (normal SES send) vs `loopback` (self-send to agent's own address, e.g. `testAgent`). Distinct from `sent_as` (From-identity for DMARC).

### Group 5 — `conversations`

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **CV-3** | MED | `listConversations` returns `Page[T]` but **can't paginate** — no `cursor` param, `next_cursor` always null (store takes only `limit`, no after-key). High-cardinality list stuck single-page | **FIX** — implement real cursor pagination (store after-key change, same class as A-5) |
| **C-1** | MED | `last_message_at`/`first_message_at` are plain `string` (no `format: date-time`) on both views — only string-typed timestamps in the surface | **FIX** — `*time.Time` + `format: date-time` |
| **A-3** | LOW | `labels`/`messages`/`participants` nullable arrays | **FIX** — `[]` |

Shape is clean: `ConversationDetailView` embeds `ConversationSummaryView` + `{participants,labels,messages}`; member messages use `MessageSummaryView` (B4b carries `webhook_status`).

### Group 6 — `webhooks`

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **WH-3** | MED | `signing_secret` declared on shared `WebhookView` (get/list/update); runtime safe (`webhookView(wh,false)`) but spec advertises it on reads | **FIX** — secret appears **only in the create response**; split into `CreateWebhookResponse` (= `WebhookView` + `signing_secret`); `WebhookView` (get/list/update) has no secret field; rotate keeps `RotateSecretBody` |
| **WH-1** | MED | `CreateWebhookRequest.url`/`events` optional | **FIX** — mark `url` + `events` **required** |
| **WH-2** | MED | `events` items + `testWebhook.event` lack enum (Slice-8 list item) | **FIX** — enum-constrain against `webhookpub.AllEventTypes` (forward-compat-safe) |
| **WH-6** | LOW | inconsistent type names: `RotateSecretBody`, `TestWebhookOutputBody`, `TestWebhookRequest`, `RejectInputBody` | **FIX** — naming sweep → `*Request` (inputs) / `*Response`/`*Result`/`*View` (outputs) |
| **WH-7** | LOW-MED | `listWebhookDeliveries` single-page (no cursor), high-cardinality debug log | **ACCEPT single-page** + set a large default limit; documented (debug view) |
| **WH-4** | — | `deleteWebhook` confirm guard? | **NO confirm** — low blast radius (re-creatable, no data loss). Deletes-with-confirm stay: account/domain/agent only |

Good: `rotate-secret` (Idempotency-Key, #8 fix), `updateWebhook` full-replace + 409-on-re-enable-in-cooldown.

### Group 7 — `events`

| ID | Sev | Finding | Decision |
|---|---|---|---|
| **EV-1** | LOW | HITL event names `email.approved`/`email.rejected` — ambiguous; design wanted `approval_*` | **FIX** — rename to **`email.approval_accepted`** + **`email.approval_rejected`** (symmetric, unambiguous). Frozen-vocabulary change: update `webhookpub.AllEventTypes` + emission sites + the `EventJSON.type` enum |
| **EV-5** | — | `EventJSON.data` untyped generic `object` (F5 — `WebhookEvent.data` is `unknown` in SDKs) | **DEFER** (F5 / Slice S2) — typing later via `oneOf`/discriminator on `type` is additive/safe. Highest-value post-GA SDK ergonomic |
| **EV-7** | LOW | `listEvents` `type` filter param bare string | optional — could enum against event vocabulary |
| **WH-6** | LOW | `RedeliverEventInputBody` | naming sweep → `RedeliverEventRequest` |
| **A-3** | LOW | `RedeliverView.deliveries` nullable | `[]` |

Strong: rich filters + real cursor pagination on `listEvents`; `schema_version`; redeliver statuses enum'd; B4a/B4b in place.

> **Note:** the event-type rename does **not** touch the message-level `hitl_status` enum (`pending_approval,sent,rejected,expired_approved,expired_rejected`) — that's a separate axis (a message's HITL state, not a webhook event).

---

## Phase 1 (API) — summary

39 ops / 8 groups reviewed. ~35 decisions, grouped for execution:

**A. Required fields (codegen correctness):** D-4 `domain` · AG-1 `email` · WH-1 `url`+`events` · MSG-3 `to`/`subject`/`body` (per-op matrix).
**B. Identity & shape additions:** I-1 `version` · A-1 account identity (`user.email`+`scope`+conditional `agent_address`) · AG-5 `createAgent`→`AgentView` · AG-4 doc `id`==`email`.
**C. Enums:** D-1 `sending_status` · AG-7 update-agent inputs · WH-2 `events` vs `AllEventTypes` · EV-1 event renames.
**D. Renames / naming:** A-2 `AccountView` · AG-3 `email` everywhere (path `{address}`→`{email}`, SDK+MCP) · MSG-1 `status`→`read_status` · MSG-11 `Result`→`AuthVerdict` · WH-6/MSG-10 `*Body`→`*Request`/`*Response` sweep · EV-1 `approval_accepted`/`approval_rejected` · D-7 keep `sending_*`.
**E. Confirm guards:** D-5 domain · AG-6 agent. (NOT webhook WH-4.)
**F. Pagination:** A-5 suppressions (real) · CV-3 conversations (real) · WH-7 deliveries (single-page + large limit).
**G. Result-shape unification:** MSG-9 unified `SendResultView` · WH-3 secret only in create response.
**H. Global:** A-3 nullable arrays → `[]`.
**I. Deferred:** EV-5 typed event payloads (S2) · AG-8 flat `hitl_*`/`inbound_*` (revisit post-stable).

**Suggested execution order:** (1) edit Huma view/request structs for A–H; (2) `make spec`; (3) `make generate-sdk`; (4) re-home hand-written SDK ergonomic layers for the renames; (5) update MCP tools (incl. `email` naming); (6) tests + contract tests.

---

## Phase 2 — SDKs

✅ **Slice E (regen + re-home) done** (commit `47292aa`). Both `generated/` bases
regenerated from the reshaped `api/openapi.yaml` (OAG v7.16.0, deterministic —
`generate-sdk-check` clean); hand-written ergonomic layer re-homed onto the
renamed types; conversations + suppressions `.list()` now follow `next_cursor`;
deletes pass `?confirm=DELETE`; `status`→`read_status` filter. **TS 84 + Python
131 tests green.**

⏳ **MCP + CLI do not compile against the reshaped SDK** — the consumer-port /
§6a MCP tool re-curation round. Errors are a mix of mechanical (renamed type
imports, `status`→`read_status`, `.status`→`.read_status`, forward `to` now
required) and deeper tool-surface decisions (MCP/CLI still send `slug`/
`agent_mode` on create; `send_email`/`approve_pending_message` tool names; etc.).
This is the round the design (§13) already tracks as pending.

_(Phase-2 SDK walkthrough — the interface review against the frozen spec —
resumes next.)_

## Phase 3 — MCP

_(pending — tool by tool; note §6a re-curation gap; carry the `email` naming decision AG-3)_

---

## Cross-cutting / open

- **Deletes:** uniform `?confirm=DELETE` on `deleteAccount` (exists) / `deleteDomain` (D-5) / `deleteAgent` (AG-6).
- **`email` vocabulary (AG-3)** reverberates: path params, SDK method params, MCP tool arg all → `email`.
- After the walkthrough: batch all FIX items into the Huma structs, run `make spec` + `make generate-sdk`, regenerate, re-test.
