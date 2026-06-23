# Agent Protection — config sub-resource (beta)

Status: proposed (2026-06-22). Branch: `feat/protection-config-api`.
Supersedes the flat per-agent screening config shipped in
`docs/design/2026-06-20-agent-screening-hitl.md` §4.1 (Slices 3/5). This reshapes
the *public surface* only; the detection mechanism (`internal/piguard`,
`internal/*/screening.go`) and storage columns are unchanged.

---

## 1. Problem statement

The per-agent screening config — inbound/outbound policy gate + content scan +
hold mechanism — currently ships as **12 flat sibling fields** on `AgentView` /
`UpdateAgentRequest` (`inbound_policy`, `inbound_policy_action`, `inbound_scan`,
`inbound_scan_review_threshold`, … ×2 directions, + `hitl_ttl_seconds`,
`hitl_expiration_action`). Three problems with that surface:

1. **#13 — threshold leak.** `getAgent` is reachable by an **agent-scoped**
   credential (`resolveOwnedAgent`), and `AgentView` returns the raw scan
   thresholds. An injected agent can read its own `*_scan_block_threshold` and
   calibrate exfil content to score just under it — defeating the egress firewall.
2. **#21 — MCP can't configure it.** The hand-written MCP `update_agent` still
   advertises the **retired** `hitl_enabled`/`hitl_mode` (silent no-op) and
   exposes **none** of the real screening fields, so screening is unconfigurable
   over MCP.
3. **Verbosity + axis confusion.** 12 flat fields, raw float thresholds that are
   detector-specific (non-portable across a detector swap), and a four-value
   `inbound_policy` enum that mixes a *trust* ladder (`open`/`allowlist`/`domain`)
   with an *anti-spoofing* gate (`verified_only`) that is really a different axis.

**Desired outcome:** a single, account-owned **protection** config resource with
a clean shape, an account-scope boundary that structurally closes #13, a
dedicated MCP tool pair that closes #21, and a **beta** marking so the contract
can evolve post-GA.

## 2. Goals and non-goals

### Goals
- Relocate the screening config to a sub-resource: `…/agents/{email}/protection`.
- Make the whole resource **account-scope only** → agent-scoped creds (the entity
  being screened) cannot read or write it (#13, structurally).
- Replace raw float thresholds with a **semantic sensitivity level**
  (`off|low|medium|high`) — portable across detector swaps, and far less useful
  to a leak even if it ever surfaced.
- Collapse the `verified_only` axis out of the trust enum: gate trust is
  `open|allowlist|domain`, symmetric inbound/outbound.
- Slim `AgentView` / `UpdateAgentRequest` back to identity; add a dedicated MCP
  `get_protection` / `update_protection` (account tier).
- Mark every protection operation/field **Beta** (prose in `doc:`/`Description`,
  matching the webhook screening-events beta convention).
- **No data migration** for the relocation; one **additive** column for the
  sensitivity level.

### Non-goals (v1 — all deferrable under the beta umbrella)
- `verified_only` / DMARC-alignment gating (returns later as a composable
  `gate.require_auth` boolean, never a fourth enum rung).
- `outbound.gate.scope` conversation-containment (the retired `high_impact`
  predicate) — deferred, see D1.
- Account-level **default** protection config + inheritance/cascade.
- Raw-float threshold override for power users.
- A public `/v1` surface for the `screening_events` audit log (audit #6 — separate).
- Renaming internal packages or the `screening_events` table (mechanism stays
  "screening"; only the public surface is "protection"). _Update: the table was
  later renamed to `protection_events` (+ an agent FK) in the audit-fixes pass;
  the screening process and Go packages keep their "screening" name._

## 3. Relevant context and constraints

- **Storage already exists.** `agent_identities` carries every needed column
  (migrations `033_inbound_policy`, `040_screening`, `041_scan_config`):
  `inbound_policy`, `inbound_allowlist`, `inbound_policy_action`, `inbound_scan`,
  `inbound_scan_{review,block}_threshold`, the `outbound_*` mirror,
  `hitl_ttl_seconds`, `hitl_expiration_action`. Defaults already encode
  safe-permissive (`open`/`flag`/scan `off`, thresholds `0.5`/`0.9`). The
  reshape is presentation-only over these columns.
- **Store methods exist:** `UpdateAgentInboundPolicy`, `UpdateAgentScanConfig`
  (+ `identity.ScanConfig`), `UpdateAgentHITL`, `ValidateScanConfig`
  (`internal/identity`). Reused; one new wrapper consolidates them under a single
  transactional write.
- **Scope gate pattern:** handlers call `s.requireAccountScope(ctx)` then
  `s.resolveOwnedAgent(ctx, address)` (see `handleUpdateAgent`). `requireAccountScope`
  403s any non-account scope (`internal/httpapi/scope.go`, the KEYSTONE).
- **Beta marking is prose**, not a machine extension: a `doc:`/`Description`
  string ("Beta: … unstable … may change before stable"). Pattern in
  `webhooks.go:41`.
- **Spec + SDK are drift-gated:** `make spec` / `TestSpecGoldenNoDrift` and
  `make generate-sdk` / `generate-sdk-check`. Any handler change requires
  regenerating `api/openapi.yaml` + `sdks/*/generated`.
- **MCP tiers are drift-gated:** every tool name must appear in exactly one of
  `RUNTIME_TOOLS` / `ADMIN_TOOLS` (`mcp/src/tools/tiers.ts`), asserted by
  `assertToolTiersComplete`.
- **Migration rules (CLAUDE.md):** additive, idempotent (`ADD COLUMN IF NOT
  EXISTS`), non-destructive on prod-sized tables. A `TEXT` add is safe.

## 4. Proposed design

### 4.1 Resource + routes

```
GET   /v1/agents/{email}/protection      // account scope
PUT   /v1/agents/{email}/protection      // account scope, full replace
```

A **singleton sub-resource** — every agent always has protection config (seeded
by column defaults), so `GET` is always 200 (never 404), and `PUT` replaces it
wholesale. PUT (not PATCH) because the config is a small bounded blob: full
replace removes deep-merge ambiguity. The three top-level keys (`inbound`,
`outbound`, `holds`) are **required** — you cannot send a partial body that
silently resets a section you didn't mean to touch (a missing top-level key is a
422, not a default-reset); leaves within each are optional and fill from
defaults. Partial edits = read-modify-write (GET → mutate → PUT). Both operations
carry a **Beta** `Description`.

Registered in a new `registerAgentProtection()` (mirrors `registerAgentWrites`),
both ops `Tags: ["agents"]`, `Security: bearer`.

### 4.2 Wire shape (`ProtectionConfigView`, also the PUT body)

```jsonc
{
  "inbound": {
    "gate": { "policy": "open|allowlist|domain", "allowlist": ["partner.com"], "action": "flag|review|block" },
    "scan": { "sensitivity": "off|low|medium|high" }
  },
  "outbound": {
    "gate": { "policy": "open|allowlist|domain", "allowlist": [], "action": "flag|review|block" },
    "scan": { "sensitivity": "off|low|medium|high" }
  },
  "holds": { "ttl_seconds": 604800, "on_expiry": "approve|reject" }
}
```

- `gate.policy` — trust ladder, monotonic: `open` (loosest) → `domain` →
  `allowlist` (tightest). Symmetric inbound/outbound (no `verified_only`).
- `gate.allowlist` — addresses or domains per `policy`; `nullable:"false"` → `[]`.
  Cap 1000 (reuse existing inbound cap).
- `gate.action` — what a gate non-match does (gate is binary; operator picks the
  consequence). Maps to the existing `*_policy_action` columns.
- `scan.sensitivity` — single knob replacing `*_scan` (on/off) + the two float
  thresholds. `off` = scan disabled; `low|medium|high` = scan on with an
  increasingly aggressive (review, block) band.
- `holds` — the shared review-queue mechanism (the surviving HITL fields,
  renamed to retire "hitl" from the public surface).

**Defaults** (a `PUT {}` resets to these — identical to today's behavior):

```jsonc
{ "inbound":  { "gate": { "policy": "open", "allowlist": [], "action": "flag" }, "scan": { "sensitivity": "off" } },
  "outbound": { "gate": { "policy": "open", "allowlist": [], "action": "flag" }, "scan": { "sensitivity": "off" } },
  "holds":    { "ttl_seconds": 604800, "on_expiry": "reject" } }
```

### 4.3 Sensitivity ↔ threshold mapping

The piguard engine scores 0..1 and selects an action from a (review, block)
threshold pair. The level is the operator-facing contract; the pair is internal:

| sensitivity | scan | review band | block band |
|---|---|---|---|
| `off`    | disabled | — | — |
| `low`    | on | ≥ 0.70 | ≥ 0.95 |
| `medium` | on | ≥ 0.50 | ≥ 0.90 |
| `high`   | on | ≥ 0.30 | ≥ 0.80 |

`medium` deliberately equals today's default pair (0.5 / 0.9) so existing agents
read back as `medium`-tuned (with scan still `off`).

**Storage decision (as built, O4):** added an **additive**
`inbound_scan_sensitivity` / `outbound_scan_sensitivity` `TEXT` column
(`migration 045`, default `'off'`). The sensitivity column is the API
source-of-truth for read-back. **Deviation from the first draft:** rather than
have the piguard engine map level→thresholds at eval time (which would touch the
security-critical screening hot path), `UpdateAgentProtection` writes the
sensitivity column **and** derives + writes the existing `*_scan` toggle +
`*_scan_*_threshold` columns in the same statement. The engine is therefore
**unchanged** — it keeps reading the float thresholds. The two can't drift
because only `UpdateAgentProtection` writes them, together. The retained float
columns are also the future raw-override escape hatch.

### 4.4 Handler flow

`PUT`:
1. `requireAccountScope(ctx)` → 403 for agent scope (**#13**).
2. `resolveOwnedAgent(ctx, email)` → 404 if not owned.
3. Validate body (`ValidateProtectionConfig`): enums (`policy`, `action`,
   `sensitivity`, `on_expiry`), allowlist ≤ 1000, `ttl_seconds` ≥ 0. Levels are
   valid-by-construction so no threshold-ladder check is needed at the edge.
4. Map sensitivity→(scan,review,block); write via a single
   `UpdateAgentProtection(ctx, agentID, userID, cfg)` store method (one
   transaction over the existing columns + the new sensitivity column).
5. Return the freshly-read `ProtectionConfigView` (200).

`GET`: scope + ownership as above; project the agent's columns into
`ProtectionConfigView`.

### 4.5 Agent surface slim-down

- **`AgentView`** — drop all 12 screening/HITL fields. Now identity + status only.
  This removes the #13 leak at the read layer too: `getAgent` (and MCP
  `get_agent`, a runtime/agent-visible tool) no longer carries thresholds.
- **`UpdateAgentRequest`** — drop all screening/HITL fields and **reduce the
  agent PATCH to a single mutable `name`** (display-name rename; agents can't be
  renamed today). `PATCH /v1/agents/{email}` keeps account scope and returns the
  updated `AgentView`. Every removed field now lives on `/protection`.
- Columns are untouched; only the *views/requests* shrink.

### 4.6 MCP

- New tools **`get_protection`** + **`update_protection`** in `mcp/src/tools/`,
  both added to **`ADMIN_TOOLS`** in `tiers.ts` (account-only). This is the
  MCP-layer #13 fix: an agent-scoped MCP session never sees protection config.
  `assertToolTiersComplete` enforces they're tiered.
- Args are flat keyword params (LLM-friendly) mapped into the nested PUT body —
  e.g. `inbound_gate_policy`, `inbound_scan_sensitivity`, `holds_ttl_seconds`.
- **`update_agent`** loses the retired `hitl_*` inputs (closes #21); its
  description is rewritten around identity. `get_agent`'s description drops the
  dead `hitl_enabled/hitl_mode`.
- Tool descriptions carry the same **Beta** sentence.

### 4.7 Beta marking

Every protection operation `Description` and the `ProtectionConfigView` doc lead
with: *"Beta: the agent protection config is unstable — its shape may change
before it is declared stable."* Mirrors `webhooks.go` screening-events prose.
Consequence: the protection contract is **explicitly not frozen at GA**, so the
deferred dimensions (§2 non-goals) can land post-GA without a stable-contract
break.

### 4.8 Regen + downstream

`make spec` → `api/openapi.yaml`; `make generate-sdk` → TS/Py bases; re-home the
hand SDK ergonomic layer (`client.agents.protection.get/replace`); MCP tools;
web dashboard agent-settings page reads/writes the sub-resource. All gated by
`spec-check` / `generate-sdk-check` / MCP tier test.

## 5. Edge cases and failure handling

- **Agent-scoped caller** → 403 at `requireAccountScope` before any read. The
  resource is invisible to the screened entity. Fail-closed.
- **Partial PUT body** — the three top-level keys are **required**, so a body
  missing `inbound`, `outbound`, or `holds` is **422-rejected** (no silent
  section reset). Within a present section, omitted leaves take documented
  defaults. Real edits are GET → modify → PUT (the SDK ergonomic layer does the
  read-modify-write).
- **Invalid enum / allowlist > 1000 / negative ttl** → 422
  (`ValidateProtectionConfig`), nothing written.
- **Unknown / unowned agent** → 404 via `resolveOwnedAgent` (no existence oracle
  across tenants).
- **Legacy non-canonical float pair** (an agent whose thresholds aren't a named
  level) — avoided by making the sensitivity column the source of truth (§4.3);
  back-compat read falls back to nearest level only if the column is unset.
- **Concurrent PUTs** — last-writer-wins on a single-row update (same as today's
  per-field updates); no lost-update protection in v1 (acceptable for operator
  config; revisit with an ETag if needed).
- **Defaults fail safe-permissive, not fail-closed.** Deliberate: defaulting to
  `block`/scan-on would silently quarantine every existing agent's mail. Turning
  protection *on* is an explicit operator action. (The hard scope ceiling and
  SPF/DKIM verdict recording are unaffected and always on.)
- **`holds.on_expiry` semantics** unchanged from the existing HITL mechanism.
- **Legacy `verified_only` inbound policy (known limitation, fail-closed).** A
  pre-existing agent whose `inbound_policy` is `verified_only` reads back that
  value on GET (Huma doesn't validate responses), but a PUT echoing it is
  **422-rejected** (`inboundGatePolicyValid` excludes it). So a read-modify-write
  on such an agent fails until the operator explicitly sets a settable
  `inbound.gate.policy`. This is fail-closed — the anti-spoofing gate is never
  silently downgraded — but is an operability snag worth a release note. The
  clean resolution is the deferred `gate.require_auth` flag (§6), which folds
  `verified_only` back in as a composable boolean.
- **Migration 045 backfill.** Pre-045 agents with `scan='on'` get their
  sensitivity level re-derived from the stored review threshold (the column
  default `'off'` would otherwise misreport and let a read-modify-write PUT
  disable a live scan). Idempotent; only touches `scan='on'` rows still at
  `'off'`.

## 6. Scalability and extensibility notes

The resource is the natural growth point; every deferred item is **additive**
under the beta marking (no breaking change):

- **`gate.require_auth: bool`** — the `verified_only`/DMARC-alignment axis, as a
  composable boolean that stacks on any trust level (inbound only). Adds a new
  optional field.
- **`outbound.gate.scope: "any"|"conversation"`** — the `high_impact`
  containment predicate as a first-class gate level (O1).
- **Account-default + cascade** — `PUT /v1/account/protection-default` that
  per-agent config inherits from (effective = merge(default, agent)). Per-agent
  config stays authoritative; purely additive.
- **Raw-float override** — optional `scan.review_threshold`/`block_threshold`
  overriding the level, backed by the retained threshold columns.
- **Per-category / multi-detector config** — nests under `scan` when piguard
  grows beyond the single heuristic detector (the `Detector` seam + `Weights`
  already anticipate this).

Narrow-for-v1 choices: single sensitivity knob (not per-category), PUT-replace
(not PATCH merge), no cascade. Each is a deliberate "smallest correct surface,"
expandable later because beta + additive.

## 7. Verification strategy

- **Store (`internal/identity`):** `UpdateAgentProtection` round-trip; sensitivity
  column persistence; level→threshold mapping; allowlist cap; wrong-owner; the
  default seed. (DB-backed per the schema-change convention.)
- **Handler (`internal/httpapi`):** account-scope 403 for agent scope (the #13
  regression guard — assert an agent token cannot GET/PUT protection);
  ownership 404; PUT-replace reset; 422 on bad enums; GET reflects PUT.
- **AgentView regression:** assert `AgentView` no longer serializes any
  `*_threshold` field (the #13 read-layer guard).
- **Spec drift:** `make spec` + `TestSpecGoldenNoDrift`; commit `api/openapi.yaml`.
- **SDK drift:** `make generate-sdk` + `generate-sdk-check`; ergonomic layer test.
- **MCP:** `assertToolTiersComplete` covers `get_protection`/`update_protection`
  (admin tier); a test that an agent-scoped tool list excludes them; flat-arg →
  nested-body mapping unit test; `update_agent` no longer accepts `hitl_*`.
- **Contract tests (TS + Py):** GET/PUT round-trip against the live server.
- **Web:** agent-settings page renders + writes the new shape (Jest).
- **Manual:** Mailpit local run — set `outbound.scan.sensitivity: high`, send a
  scoring message, confirm hold; confirm an agent-scoped key is 403 on
  `/protection`.

Most likely regressions: (a) a screening field left on `AgentView` (re-opens
#13); (b) the new MCP tools left untiered (caught by the assert); (c) stale spec
golden.

## 8. Decisions and open questions

Resolved (2026-06-22):
- **D1 — `outbound.gate.scope` containment: DEFERRED.** Not in v1. Returns
  post-GA as an additive field (`"any"|"conversation"`) under the beta umbrella.
  The `scope` knob is absent from the v1 wire shape (§4.2).
- **D2 — `updateAgent` → name-only.** The agent PATCH drops every screening/HITL
  field and updates only the display `name` (§4.5). Not removed.
- **D3 — PUT required top-level keys.** `inbound`, `outbound`, `holds` are all
  required; leaves optional with default fill (§4.1, §5).

Still open:
- **O4 — sensitivity column vs derive-on-read** (§4.3). Recommend the **additive
  column** (`migration 045`); confirm acceptance.
- **O5 — exact level→threshold values** (§4.3 table). Calibrate against piguard's
  score distribution before freezing the numbers (internal, so tunable even
  post-beta).
