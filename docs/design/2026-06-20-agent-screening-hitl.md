# Agent Screening + Human-in-the-Loop Review (pre-GA)

Status: Draft for review
Date: 2026-06-20
Branch: `worktree-design+agent-screening-hitl`
Author: design pass (formalizes a converged discussion)

---

## 1. Problem statement

e2a delivers inbound email to AI agents and sends outbound email on their behalf.
Email is a high-risk **indirect prompt-injection** vector: a malicious message can
carry instructions aimed at the recipient agent's LLM (hidden HTML, Unicode-tag
smuggling, encoded payloads), and a compromised or misbehaving agent can **exfiltrate
data** or **propagate attacks** through the outbound path. Real-world precedent:
EchoLeak (CVE-2025-32711), the M365 Copilot ASCII-smuggling leak, the Gemini-for-
Workspace hidden-text hijack.

Today e2a has:

- An **inbound ingestion gate** (`internal/inboundpolicy`): per-agent
  `inbound_policy` ∈ {open, allowlist, domain, verified_only}. It only ever
  **flags** (`messages.flagged` + `flag_reason`) and **still delivers** — it never
  holds or drops. It emits `email.flagged`.
- An **outbound HITL** hold (`hitl_enabled` + `hitl_mode` + `messages.status =
  pending_approval`): the only "hold for a human" machinery, and it is outbound-only.

There is **no content inspection** (only sender identity is checked), and there is
**no way to route a suspicious *inbound* message to a human** before the agent sees
it. We want a pluggable content-screening layer for both directions whose default
action is to route to human review, designed so external detectors (Lakera, Bedrock,
Model Armor, Prompt Guard, …) can be added later without reshaping the contract.

This is **pre-GA** work: `/v1` is not yet frozen, so we may make breaking renames to
get the surface right before it becomes a stable contract.

## 2. Goals and non-goals

### Goals

1. **Pluggable detection seam.** A `Detector` interface + normalized `Result`
   envelope that wraps heterogeneous detectors (boolean, score, enum-confidence,
   categories, spans) without lossy normalization. Ship **one** detector in v1: a
   dependency-free built-in `heuristics` detector.
2. **Symmetric policy surface.** A 2×2 of producer policies on the agent —
   inbound/outbound × gate/scan — each emitting an action `flag | review | block`.
3. **HITL as a shared review mechanism.** Generalize the existing outbound
   `pending_approval` hold into a **direction-aware review queue** that any policy
   can route into. Retire `hitl_enabled` / `hitl_mode`.
4. **Auditability.** A durable `screening_events` log (gate violations *and* scan
   detections), a denormalized verdict on the message row, and a notification event.
   Close the feedback loop (detection → human disposition) for threshold tuning.
5. **Fail safe, not silent.** Detector outage/timeout fails *to review*, never to
   silent-allow and never to hard-block.

### Non-goals (v1)

- **No external detector providers.** Design the seam; do not build Lakera/Bedrock/
  etc. adapters.
- **No OCR / image-text extraction execution.** Reserve the `image_ocr` segment
  type in the contract; do not run OCR in v1.
- **No reviewer RBAC split.** Inbound and outbound reviews go to the same humans.
  Model leaves room for per-direction scoping later; we do not build it now.
- **No ML model serving** (Prompt Guard etc.) in-process.
- **No dashboard build-out beyond what the review queue needs** (the web slice is
  tracked separately; this doc specifies the API/data it consumes).
- **No change to SPF/DKIM/DMARC** or the auth-header signing path.

## 3. Relevant context and constraints

Verified against the code on this branch:

- **AgentIdentity** (`internal/identity/store.go:78`): carries `HITLEnabled`,
  `HITLTTLSeconds`, `HITLExpirationAction`, `HITLMode`, `InboundPolicy`,
  `InboundAllowlist`. Read paths: `ListAgentsByUser` (enriched), `GetAgentByID`,
  `GetAgentByEmail`. The agent `SELECT` is at `store.go:746`.
- **Message** (`internal/identity/store.go:173`): one row per recipient. HITL fields
  (`Status`, `ApprovalExpiresAt`, `ReviewedAt`, `ReviewedByUserID`, `RejectionReason`,
  `Edited`, `BodyText/BodyHTML/AttachmentsJSON`) at `:246-267`; inbound flag fields
  (`Flagged`, `FlagReason`) at `:269-274`. Status constants at `:277-284`:
  `sent | pending_approval | rejected | expired_approved | expired_rejected`.
- **Inbound relay hook** (`internal/relay/server.go:327-333`): `inboundpolicy.
  EvaluateIngestion(...)` runs after SPF/DKIM/DMARC, before persistence. Two
  persistence paths exist — transactional outbox (`WithTx` +
  `CreateInboundMessageInTx` + `outbox.PublishTx`, `:422-442`) and a legacy
  goroutine path (`CreateInboundMessage`, `:443-451`). Events use
  `DeterministicEventID(messageID, eventType)` so MTA retries are idempotent
  (`:365`, `:392`).
- **Outbound HITL** (`internal/agent/api.go` `HoldForApprovalCore` /
  `actionGateHold`, ~`:967-1104`): composes, then holds as `pending_approval` with
  the draft body/attachments stored on the row; approve/reject via the
  `ApprovePending` / `RejectPending` deps (`internal/httpapi/httpapi.go:131`).
- **API surface**: additive-PATCH flat fields. `UpdateAgentRequest`
  (`internal/httpapi/agents_write.go:109`) — each field an optional pointer with its
  own `Update*` dep (`UpdateAgentInboundPolicy`, `UpdateAgentHITLMode`). `AgentView`
  (`internal/httpapi/operations.go:56`). Spec/SDK drift gates: `make generate` →
  `api/openapi.yaml` + `sdks/*/.../generated/`; CI `spec-check`,
  `generate-sdk-check`, and `TestSpecGoldenNoDrift` (in `make test-unit`).
- **Migrations**: idempotent + embedded + auto-applied; next is **037**. Pattern
  (from `036_hitl_mode.sql`): `ADD COLUMN IF NOT EXISTS … NOT NULL DEFAULT …`;
  CHECK constraints inside `DO $$ BEGIN … EXCEPTION WHEN duplicate_object THEN NULL;
  END $$;`. Never `ALTER COLUMN TYPE` on `messages` / `usage_events`.
- **CLAUDE.md schema rule**: any table-shape change needs DB-backed tests for *every*
  package writing direct SQL against that table (`messages`, `agent_identities`).
- **ID format**: `{type}_{hex}` via `generateID()` (`store.go:3340`). New prefix:
  `scr_` for screening events.

### Key existing-behavior constraint (drives a default)

`inbound_policy` today is **flag-and-deliver**. If we add `inbound_policy_action`
with default `review`, the migration would silently start **quarantining** mail for
every existing agent that set a gate. Therefore:

> **Gates default `inbound_policy_action`/`outbound_policy_action` to `flag`**
> (preserve today's behavior; low friction for a coarse identity signal).
> **Scans default to `review`** (new surface, no back-compat; review is the safe
> default for content verdicts).

This is a deliberate refinement of "actions default to review": it holds for the new
*scan* producers; the pre-existing *gate* producers default to `flag` for backward
compatibility. Operators opt a gate into `review`/`block` explicitly.

## 4. Proposed design

### 4.0 Conceptual model

```
DECISION LAYER — 4 producer policies on the agent, each emits an action
  inbound_policy   (sender gate,   binary)   ─┐
  outbound_policy  (recipient gate, binary)   │  action ∈ {flag, review, block}
  inbound_scan     (content, score 0..1)      │  review → enqueue HITL
  outbound_scan    (content, score 0..1)     ─┘

MECHANISM LAYER — HITL review queue (direction-aware), shared by all producers
  hold · human verdict · TTL expiry        →  approve/reject branches on direction

AUDIT LAYER
  messages row (denormalized verdict) · screening_events (durable log) · events
```

### 4.1 Agent schema changes (the public surface)

New/changed fields on `AgentIdentity`, `UpdateAgentRequest` (pointers), `AgentView`:

```jsonc
{
  // HITL = review-queue mechanism only (kept names; meaning narrowed)
  "hitl_ttl_seconds": 604800,            // applies to ANY held item, in/out
  "hitl_expiration_action": "reject",    // approve | reject on TTL expiry

  // --- inbound row ---
  "inbound_policy": "verified_only",          // open|allowlist|domain|verified_only (unchanged)
  "inbound_allowlist": ["partner.com"],       // unchanged
  "inbound_policy_action": "flag",            // flag|review|block  (default flag — back-compat)
  "inbound_scan": "off",                      // off|on             (detector on/off)
  "inbound_scan_review_threshold": 0.5,       // ≥ → review
  "inbound_scan_block_threshold": 0.9,        // ≥ → block

  // --- outbound row ---
  "outbound_policy": "open",                  // open|allowlist|domain  (NEW recipient gate)
  "outbound_allowlist": [],                   // recipients/domains (NEW)
  "outbound_policy_action": "flag",           // flag|review|block (default flag)
  "outbound_scan": "off",                     // off|on
  "outbound_scan_review_threshold": 0.5,
  "outbound_scan_block_threshold": 0.9
}
```

Notes:
- **Scan modeled as `off|on` + thresholds**, not `off|flag|review|block`. The
  threshold ladder *is* the action selector (below-review = allow, review-band =
  review, ≥ block = block), which is strictly more expressive than a single enum and
  matches the SpamAssassin/Rspamd ladder. `flag`-only behavior is expressed by
  setting `review_threshold == block_threshold == 1.0`-ish, or we expose a
  `*_scan_flag_below_review` boolean later if needed (deferred).
- **`verified_only` is inbound-only** (DMARC is about inbound sender auth). Outbound
  gate = `open|allowlist|domain`.
- **Retire `hitl_enabled` and `hitl_mode`.** Migration maps old behavior forward
  (§4.6). `hitl_*` now names the mechanism (TTL + expiry).
- **Trust ramp** lives in the outbound gate: `outbound_policy: allowlist` with a
  growing `outbound_allowlist` + `outbound_policy_action: review` ⇒ unknown
  recipients held for a human until trusted. This replaces `hitl_mode: all`.

Validation (mirrors `ValidateHITLConfig`): `ValidateScanConfig` checks
`0 ≤ review_threshold ≤ block_threshold ≤ 1`, action ∈ enum, gate ∈ enum, allowlist
≤ 1000 entries (reuse the existing inbound cap). Each field gets its own
`Update*` store method + `Deps` callback, registered in `handleUpdateAgent`.

### 4.2 `internal/piguard` package (the detection seam)

stdlib-friendly leaf package; takes primitives, returns a normalized verdict.

```go
package piguard

type Direction int
const ( DirectionInput Direction = iota; DirectionOutput )

type Segment struct {
    Type    SegmentType // subject|text_plain|html_visible|html_hidden|attachment_text|image_ocr
    Content string
    Ref     string      // source locator for span/offending-segment reporting
}

type DecodedSignals struct {
    UnicodeTags     bool
    ZeroWidth       bool
    HiddenCSSText   bool
    HomoglyphRatio  float64
    FragmentedURL   bool
    PlainHTMLDiverge bool
}

type Request struct {
    Direction Direction
    Segments  []Segment
    Signals   DecodedSignals     // precomputed cheap signals (also drive force-overrides)
    Sender    string
    Auth      *emailauth.Result  // inbound only; nil outbound
    SizeBytes int
}

type Detector interface {
    Inspect(ctx context.Context, req Request) (*Result, error)
    Name() string
}

type Status int
const ( StatusOK Status = iota; StatusTimeout; StatusError; StatusUnsupported )

type Result struct {
    Flagged    bool        // REQUIRED — adapter derives it if provider doesn't
    Score      float64     // REQUIRED — 0..1; enum/bool providers map to fixed points
    Categories []Category  // optional: {Name (normalized), NativeCode, Score}
    Spans      []Span      // optional: {Start,End,Text,Label,Ref} (~only Lakera)
    Status     Status
    Provider   ProviderMeta // {Name, ModelVersion, NativeVerdict, NativeScore, LatencyMS, Raw}
}
```

**Normalized category vocabulary** (mapped to taxonomies for portable audit):
`prompt_injection_direct` (OWASP LLM01 / ATLAS T0051.000),
`prompt_injection_indirect` (T0051.001 / NISTAML.015), `jailbreak` (T0054),
`data_exfiltration`, `obfuscation`, `sensitive_disclosure` (OWASP LLM02). Each
detector maps its native labels onto these; `Category.NativeCode` preserves the
original.

**MIME extraction** (`piguard/extract`): decode the raw RFC-2822 message **once**
into `[]Segment` + `DecodedSignals`. This is the most reusable and most-tested unit
and the thing every future provider depends on. Split visible vs hidden HTML
(strip `display:none`, `font-size:0`, white-on-white, `mso-hide:all`, off-screen),
detect Unicode Tags-block (U+E0000–E007F), zero-width (U+200B–200D, U+FEFF),
homoglyph ratio, fragmented/reassembly URLs, and `text/plain`↔`text/html` divergence.

**Built-in `heuristics` detector** (`piguard/heuristics`): the only registered
detector in v1. Deterministic, no network, near-zero false positives. Inbound:
the obfuscation vectors above. Outbound: egress/exfil signatures — secret/key/PII
regexes, suspicious egress URLs (markdown-image exfil), encoded blobs. Emits
categories + a weighted score.

**Aggregator** (`piguard.Engine`): runs registered detectors **in parallel**,
combines into one `Result`:
- Weighted sum of per-detector `Score` with a per-category cap (no single noisy
  detector dominates).
- **Deterministic force-overrides**: if `Signals.UnicodeTags` (or other
  high-confidence deterministic markers) present ⇒ floor the action at `flag`
  regardless of score.
- **Status handling**: a detector returning `Timeout`/`Error` is **excluded** from
  the weighted sum (NOT counted as benign 0). If fewer than `min_detectors`
  returned `OK` (config; default 1, so a single built-in outage triggers it), the
  engine returns a sentinel `EngineDegraded` verdict → caller maps to **review**
  (fail-to-review). It never silent-allows and never auto-blocks on degradation.

**As-built hardening (Slice 1, post-adversarial-review).** The implemented `Engine`
exposes `Aggregate.Action(reviewTh, blockTh)`; the wiring layer should call this
rather than `ActionForScore(agg.Score, …)` directly, because it folds in the
fail-safe defaults an open-coded call site would miss: a **degraded** aggregate →
review; **truncated/oversize** content → review (via the `MinAction` force-floor,
alongside Unicode-tags → flag); and a **NaN/Inf/negative** detector score is excluded
as `StatusError` (not averaged in — a hostile/buggy adapter must not poison the
aggregate toward allow). The heuristics detector confusable-folds content before the
injection lexicon (one-char homoglyph swaps don't evade), and the extractor routes
HTML comments / `<script>`/`<style>` bodies / unterminated-tag tails into the hidden
segment (content a human never sees but an LLM might). ReDoS, MIME-DoS, and data
races were probed and refuted.

### 4.3 Action evaluation (gate + scan → action)

A small evaluator (in `piguard` or a sibling `screening` package) maps agent config +
verdict → an `Action` and the `screening_events` rows:

```
evaluate(agent, direction, gateDecision, scanResult) -> (Action, []ScreeningEvent)
  gate:  if gateDecision.Flagged  -> action = agent.<dir>_policy_action  (flag|review|block)
  scan:  if scan enabled:
           if engine degraded     -> action = review            (fail-to-review)
           elif score >= block_th  -> action = block
           elif score >= review_th -> action = review
           else                    -> action = allow (no event unless sampling)
  combine: BOTH gate and scan may fire. The APPLIED action is the most severe
           (block > review > flag > allow). Each producer that fired writes its OWN
           screening_events row (so audit shows all reasons); the message lands in
           the queue ONCE with review_reason = the most-severe producer.
```

**Ordering**: gate and scan **both always run** (cheap; gate is in-memory string
match, scan heuristics are local). No short-circuit — we want full audit even when
the gate already blocks. (If a future external scan is expensive, add a
"skip-scan-when-gate-blocks" optimization behind config; not needed for v1.)

### 4.4 HITL generalized to a direction-aware review queue

**One logical queue**, reusing `messages`. No new queue table.

- **Status set** extends the existing constants with review-specific values to keep
  the inbound/outbound lifecycles distinguishable in queries while sharing shape.
  Add: `pending_review`, `review_approved`, `review_rejected`,
  `review_expired_approved`, `review_expired_rejected`. (Rationale for new names vs
  reusing `pending_approval`: outbound approval predates this and has subtly
  different semantics — "edit draft then approve" — and existing `pending_approval`
  rows must not be reinterpreted. New statuses avoid migrating live rows. The
  outbound screening-driven holds use `pending_review` (O3, resolved);
  `pending_approval` stays reserved for the explicit user-send-approval flow.)
- **`review_reason`** column on `messages`: `sender_gate | recipient_gate |
  inbound_scan | outbound_scan | outbound_send`. Plus `scan_score REAL` and
  `scan_action TEXT` denormalized for the queue UI.
- **Inbound holds are lighter**: content already lives in `raw_message`. A
  `review`/`block`(quarantine) inbound message is persisted with
  `status = pending_review` and **webhook/WS delivery suppressed** (do not enqueue
  `email.received`; enqueue `email.injection_detected` instead). No held-body columns
  needed.
- **Release branches on direction**:

  | | approve | reject |
  |---|---|---|
  | inbound  | release to agent (status `review_approved` → inbox-visible; push re-delivery of `email.received` is a follow-up) | drop (terminal `review_rejected`) |
  | outbound | send (existing path) | discard draft (existing path) |

  Inbound does **not** use the outbound-only "edit before approve" path.

  **Reconciliation (as-built):** the inbound reject path does **not** scrub
  `raw_message` (unlike the outbound draft, which is the *user's* content). An
  inbound held payload is an *attacker's* message; it is **retained (hidden) until
  the 30-day message TTL for security forensics**, not scrubbed. This is safe
  because of the read-boundary invariant below.

  **Read-boundary invariant (load-bearing — adversarial-review finding):** a held
  inbound message (`pending_review`, `review_rejected`, `review_expired_rejected`)
  must be unreachable by the agent via **every** read path, not just the inbox list.
  The held-status exclusion is centralized (`identity.heldInboundStatuses`) and
  applied to `GetMessagesByAgent`, `GetMessageWithContent`, `GetInboundMessage`,
  `GetConversationByID`, `ListActivityByAgent`, and `ListConversationsByAgent`.
  The release transitions (`ApproveInboundReview`/`RejectInboundReview`) are
  **agent-scoped** for tenant isolation; the worker (TTL) transitions are
  system-scoped.
- **TTL + expiry**: `hitl_ttl_seconds` / `hitl_expiration_action` govern *any* held
  item. The existing HITL expiry worker generalizes to also process
  `pending_review` rows, applying the direction-correct release on expiry.
- **Block semantics**:
  - Inbound `block` → **accept-then-quarantine**, NOT SMTP 5xx. We persist the row
    as `review_rejected` (or a dedicated `blocked` terminal) with delivery
    suppressed, and emit `email.injection_detected`. Rationale: the relay currently
    returns SMTP 250 regardless of per-recipient outcome (`server.go:478`), the body
    is needed for the audit trail/forensics, and a 5xx leaks detector behavior to
    the attacker and invites MTA retries. (SMTP-reject is a possible future hard
    mode; Open Question O4.)
  - Outbound `block` → **refuse the send** to the API caller with a typed error
    (e.g. `422 screening_blocked` + category), and still write a `screening_events`
    row. Nothing is sent.

### 4.5 Recording / audit

Three layers (none is the others' substitute):

1. **Message row (denormalized)** — `flagged`, `flag_reason` (existing) +
   `review_reason`, `scan_score`, `scan_action`. Fast inbox/queue rendering.
2. **`screening_events` (durable log)** — append-only; system of record.

```sql
-- 038_screening_events.sql  (037 is the agent/message column migration)
CREATE TABLE IF NOT EXISTS screening_events (
    id           TEXT PRIMARY KEY,            -- scr_<hex>
    message_id   TEXT NOT NULL,               -- SOFT ref: NO FK cascade (outlives 30d message TTL)
    agent_id     TEXT NOT NULL,
    direction    TEXT NOT NULL,               -- inbound | outbound
    source       TEXT NOT NULL,               -- gate | scan
    reason       TEXT NOT NULL,               -- sender_gate|recipient_gate|inbound_scan|outbound_scan|outbound_send
    action       TEXT NOT NULL,               -- flag | review | block
    subject_addr TEXT,                         -- sender (in) / recipient (out) that tripped a gate
    detector     TEXT,                         -- scan only
    score        REAL,                         -- scan only
    categories   JSONB,                        -- scan only
    spans        JSONB,                        -- scan only
    raw          JSONB,                        -- scan only: provider raw, forensics
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_screening_agent_time ON screening_events (agent_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_screening_message ON screening_events (message_id);
```

   - **Record violations only** (`action ≠ allow`) by default. "Record passing
     scans too" behind a per-agent/deployment **debug/sampling flag** (config).
   - **Idempotency**: derive `id = "scr_" + sha256(message_id | source | reason)[:n]`
     and `INSERT … ON CONFLICT (id) DO NOTHING`, mirroring `DeterministicEventID`.
     An MTA-retried inbound delivery re-screens and re-inserts the *same* rows
     → no duplicates.
   - **No cascade**: `message_id` is a soft reference. Detection trail survives the
     message's 30-day TTL. (Separate, longer retention/GC for `screening_events` is
     future work; v1 keeps them.)
3. **Events** — `email.injection_detected` (new `webhookpub` type) for review/block;
   continue `email.flagged` for `flag`. Fire-and-forget notification only.

**Feedback loop**: join `screening_events` → message disposition
(`reviewed_by_user_id`, `rejection_reason`, approved/rejected status) to compute
false-positive rate and tune thresholds. Gate-violation rows also power the
**trust-ramp metric** ("% of outbound sends still hitting review for incomplete
allowlist").

### 4.6 Migration of retired HITL fields

The agent scan-config columns ship in `041_scan_config.sql`; the review-queue
columns + `screening_events` table ship in `040_screening.sql` (both idempotent,
non-destructive; renumbered above main's `039` after the rebase).

`041_scan_config.sql`:
- `ADD COLUMN IF NOT EXISTS` for: `inbound_policy_action` (default `flag`),
  `outbound_policy` (default `open`), `outbound_allowlist` (TEXT[]),
  `outbound_policy_action` (default `flag`), `inbound_scan`/`outbound_scan`
  (default `off`), the four threshold columns (REAL, defaults 0.5 / 0.9), plus
  `review_reason`/`scan_score`/`scan_action` on `messages`.
- **Forward-map old HITL semantics** before dropping (data-preserving):
  - `hitl_enabled = true, hitl_mode = 'all'` ⇒ `outbound_policy = 'allowlist'`,
    `outbound_allowlist = '{}'`, `outbound_policy_action = 'review'` (hold every send
    — empty allowlist means every recipient is unknown).
  - `hitl_enabled = true, hitl_mode = 'high_impact'` ⇒ enable `outbound_scan` with
    default thresholds (the heuristic risk signal replaces the coarse high-impact
    gate). *(Approximation — call out in release notes; see O5.)*
  - `hitl_enabled = false` ⇒ outbound left at `open`/`flag` (no holds).
- **Retiring the columns**: do NOT drop `hitl_enabled`/`hitl_mode` in the same
  migration that reads them. Two-step: (037) add new + backfill; (later, after code
  no longer references them) a separate drop migration. Keeps rollback safe.
- **Existing `pending_approval` rows** are untouched (we add new statuses, don't
  reinterpret old ones).

### 4.7 Runtime wiring

- **Inbound** (`internal/relay/server.go`, at the §4.3 evaluation, right after the
  existing `inboundpolicy.EvaluateIngestion` at `:333`): build a `piguard.Request`
  (`Direction=Input`) from the parsed body, run the engine, evaluate action. Thread
  the resulting status/`review_reason`/score into **both** persistence paths
  (`CreateInboundMessageInTx` and `CreateInboundMessage` gain the new fields), and
  gate which event is enqueued (`email.received` vs `email.injection_detected`) and
  whether the row is `pending_review`. Reuse deterministic event IDs.
- **Outbound** (`internal/agent/api.go` `HoldForApprovalCore`/`actionGateHold` +
  `internal/outbound`): after compose, run the engine (`Direction=Output`) and the
  recipient gate. `review` ⇒ existing `pending_approval`/`pending_review` hold;
  `block` ⇒ typed send-refusal; `flag` ⇒ send + annotate + screening row.

### 4.8 Config (deployment-level)

`internal/config` + `config.yaml`: detector enable list, per-detector weights,
timeouts, `min_detectors` for fail-to-review, and the default thresholds (per-agent
thresholds override). Per CLAUDE.md's Mailpit/env pattern, expose
`E2A_SCREENING_*` env overrides. Per-agent fields stay policy-level (action, gate,
thresholds, allowlist); operator knobs (weights, timeouts, which detectors) stay
deployment-level.

## 5. Edge cases and failure handling

- **Huge bodies / multipart bombs / huge attachments.** `piguard/extract` enforces a
  **scan size cap** (config, e.g. 1 MB of extracted text; bounded part count and
  decompression for any future archive handling). On exceeding the cap: extract up
  to the cap, set `Request.SizeBytes`, and mark a `truncated` signal. Action when
  over-limit = **review** (fail-to-review — unscannable ≠ safe), recorded with
  reason `oversize`. Never OOM the relay.
- **Attachments / OCR.** v1 extracts **text from text-like attachments only** up to
  the cap; binary/image attachments are **not** OCR'd (segment type reserved, not
  produced). An agent that wants attachment-content safety in v1 relies on the
  oversize→review fallback. Documented limitation.
- **Detector outage / timeout.** Per-detector timeout (config) → excluded from
  aggregate; `< min_detectors OK` → fail-to-review. With only the built-in
  heuristics in v1, "outage" is effectively a panic/bug — guard the engine with
  recover() so a detector panic becomes `StatusError`, not a relay crash.
- **Both gate and scan fire.** One queue item (`review_reason` = most-severe
  producer); **one `screening_events` row per producer** that fired. Applied action
  = most severe (block > review > flag).
- **MTA retries (inbound).** Re-screening is deterministic; `screening_events` IDs
  are content-derived with `ON CONFLICT DO NOTHING`; the message + withheld event
  use the existing deterministic-ID idempotency. No dupes, no double-hold.
- **Empty allowlist with gate enabled.** Existing fail-closed semantics
  (`inboundpolicy`): everything trips the gate. For outbound this is the intended
  "hold everything" trust-ramp start state — correct, but loud; surfaced in the
  trust-ramp metric.
- **`flag` must not regress today's behavior.** Inbound `flag` = exactly current
  `email.flagged` + deliver. The default `inbound_policy_action = flag` guarantees a
  zero-behavior-change migration for the gate.
- **Reviewer acts after TTL expiry race.** Approve/reject and the expiry worker both
  target the row via a conditional `UPDATE … WHERE status = 'pending_review'`; first
  writer wins, second is a no-op (reuse the existing HITL worker's compare-and-set).
- **Outbound block of a reply mid-conversation.** Send-refusal returns a typed error
  the SDK/CLI surfaces; the draft is not silently dropped — a `screening_events` row
  + (optionally) a `pending_review` hold lets a human override. Default `block` does
  not hold; if operators want override-on-block, they choose `review`.
- **PII false positives outbound.** Heuristic PII/secret regexes are noisy; default
  `outbound_scan = off`. When on, default action band routes to **review**, not
  block, so a human adjudicates — matches the "false positives → human, not block"
  rule.
- **Detector sees attacker-controlled Reply-To.** Gate already evaluates the
  *authenticated* From (`senderEmail`), not Reply-To (`server.go:327-333`); the scan
  inspects content segments, not trust identity. Preserve that split.

## 6. Scalability and extensibility notes

- **Inbound is attacker-floodable.** A spam flood creates many `pending_review` rows
  and `screening_events`. Mitigations: (a) **per-direction queue caps / rate** per
  agent (config) — beyond the cap, fall back to `flag`+deliver or drop-with-audit
  rather than unbounded holds; (b) dashboard shows **separate inbound vs outbound
  lanes** so inbound noise can't bury rarer high-stakes outbound holds; (c)
  `screening_events` is append-only + indexed by `(agent_id, created_at)` for
  bounded queries. Record-only-violations keeps write volume proportional to attacks,
  not traffic.
- **Detector seam is the main extension axis.** Adding Lakera/Bedrock/etc. is a new
  `Detector` impl + an entry in the engine registry + deployment config — no schema
  or API change. `Result` already models score/categories/spans/enum-confidence/raw,
  so no normalization rework. `Request` already carries `Direction` + `Segments` +
  `Docs`-style segmentation for indirect-injection checks.
- **`screening_events.raw` JSONB** absorbs provider-specific output without
  migrations.
- **Reviewer RBAC** later = add a scope/assignee column + filtered queue queries;
  the `direction` + `review_reason` tags already partition the queue.
- **Thresholds are per-agent**, so calibration is per-tenant; the feedback-loop join
  gives the data to auto-tune later.
- **Stay narrow for v1**: one detector, no OCR, no external providers, no RBAC — the
  contract is built to absorb all of them without reshaping.

## 7. Verification strategy

Per CLAUDE.md, schema changes need DB-backed tests for every package writing direct
SQL against `messages` / `agent_identities` (identity store, agent, relay, httpapi).

- **`piguard` unit tests (no DB, no wiring)** — the de-risking core:
  - `extract`: golden MIME fixtures — hidden-CSS, Unicode-tags, zero-width,
    homoglyph, fragmented-URL, plain/HTML divergence, oversize/truncation, malformed
    MIME. Assert segment split + `DecodedSignals`.
  - `heuristics`: known-attack corpus (the documented CVE payloads) → flagged with
    expected categories; benign corpus → not flagged (false-positive budget). Table
    tests for both directions.
  - `Engine`: weighted aggregation, per-category caps, force-overrides,
    timeout/error exclusion, `min_detectors` fail-to-review.
- **Action evaluator unit tests**: gate×scan × action matrix → applied action +
  expected `screening_events` rows; threshold ladder boundaries; both-fire case.
- **Store DB tests**: new columns round-trip on `agent_identities` and `messages`;
  `screening_events` insert + `ON CONFLICT` idempotency + soft-ref survival after
  message delete; new status transitions; the expiry worker on `pending_review`.
- **Migration tests**: 037 idempotent (run twice); HITL forward-map correctness for
  each `(hitl_enabled, hitl_mode)` combo; existing `pending_approval` rows untouched.
- **Relay integration (`make test-integration`)**: inbound `off/flag/review/block`
  end-to-end — flag delivers + `email.flagged`; review/block persist
  `pending_review`, suppress `email.received`, emit `email.injection_detected`;
  MTA-retry produces no dupes (both persistence paths).
- **Outbound (`internal/agent`)**: `review` → `pending_approval` hold; `block` →
  typed refusal, nothing sent; approve/reject release paths per direction.
- **Spec/SDK drift**: `make generate`; assert `TestSpecGoldenNoDrift`, `spec-check`,
  `generate-sdk-check` pass with the new fields.
- **Manual validation**: drive a hidden-instruction email through a local agent
  (Mailpit catch-all per CLAUDE.md) and confirm it lands in review, not the agent;
  confirm the trust-ramp (grow `outbound_allowlist`, watch holds drop).

Most-likely regressions: (1) existing flag-only inbound behavior if the gate default
isn't `flag`; (2) double webhook fire if `email.received` isn't suppressed on hold in
*both* persistence paths; (3) spec drift on the renamed HITL fields; (4) the expiry
worker mishandling the new statuses.

## 8. Slice plan

Each slice independently shippable + testable.

1. **`piguard` core** — `extract` + `heuristics` + `Engine` + `Result`/`Request`
   contract. Fully unit-tested, **zero wiring**. (Highest value, lowest risk.)
2. **Review-queue generalization + `screening_events`** — new statuses, `messages`
   columns, `screening_events` table + store methods (idempotent insert, soft ref),
   generalize the expiry worker. DB-tested. No detector wired yet.
3. **Config + API + migration 037** — agent fields, `Validate*`, `Update*` deps,
   HITL forward-map + retire (two-step), `make generate`. Drift gates green.
4. **Inbound wiring** — `off`/`flag` first (no behavior change), then `review`/`block`
   quarantine + `email.injection_detected`. Both persistence paths.
5. **Outbound wiring** — recipient gate + `outbound_scan`; `review` → existing hold,
   `block` → refusal. Mostly reuse.

(Web review-queue UI — separate doc; consumes the API/data from slices 2–5.)

## 9. Open questions

- **O1 — Scan field shape. RESOLVED (2026-06-20): explicit `off|on` + review/block
  thresholds** (§4.1). Chosen for explicitness over a single overloaded
  `off|flag|review|block` enum; the threshold ladder selects the action.
- **O2 — Flag band for scans.** Do we want a "deliver + annotate" band *below*
  review (score in `[flag_th, review_th)`)? Adds a third threshold. Defer unless
  there's a concrete need.
- **O3 — Outbound hold status. RESOLVED (2026-06-20): screening-driven holds use
  `pending_review`**; `pending_approval` stays reserved for the explicit
  user-send-approval flow (with its edit-then-approve semantics). Cleaner provenance,
  and existing `pending_approval` rows are never reinterpreted.
- **O4 — Inbound block transport.** Accept-then-quarantine (recommended) vs a hard
  SMTP 5xx reject mode for high-confidence blocks. 5xx needs propagating errors up to
  `Data()` (currently always 250) — out of scope unless wanted.
- **O5 — `high_impact` forward-map.** Mapping old `hitl_mode='high_impact'` onto
  `outbound_scan` is an approximation (different signal). Acceptable for pre-GA with
  release-note callout, or do we preserve a literal high-impact gate? Recommend
  approximate + document.
- **O6 — `screening_events` retention/GC.** v1 keeps rows indefinitely (soft ref).
  When do we add retention? (Likely a follow-up once volume is known.)
- **O7 — Reviewer identity for inbound.** Same humans as outbound now; confirm we
  don't need even a coarse `direction` permission at GA.
