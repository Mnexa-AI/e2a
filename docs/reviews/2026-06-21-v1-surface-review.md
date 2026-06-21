# v1 API / SDK / MCP surface review

**Purpose:** a rolling, focused review of the newly designed `/v1` API, the TS/Python SDKs, and the MCP interfaces — one subcomponent per iteration. Each entry records concrete findings (correctness, contract cleanliness, consistency, security, ergonomics) with `file:line` and a suggested fix, plus what was verified safe.

**Branch:** `review/v1-surface-audit` (off `main`). **Cadence:** ~20-min loop.

**How the loop runs:** each iteration picks the **next `pending` row** in the ledger, reviews *only* that subcomponent (keep it tight), appends a findings section below, flips the ledger row to `done` with a one-line headline, and commits. Severity scale: 🔴 high · 🟡 medium · 🔵 low · ✅ verified-clean.

---

## Review ledger

| # | Area | Subcomponent | Status | Headline |
|---|------|--------------|--------|----------|
| 1 | API | `agents_write.go` — agent create/PATCH + config | done | 🟡 updateAgent OpenAPI desc stale (HITL-only) vs full policy/scan PATCH; verify create's account-scope ceiling |
| 2 | API | `messages.go` — message detail/list views + raw/parsed | done | 🟡 read-side label validation duplicates write-side rule (drift); 🔵 hitl_status enum is outbound-only (no inbound review-status field); cursor binding ✅ strong |
| 3 | API | `outbound.go` — send/reply/forward + idempotency wiring | done | 🟡 reply_all bypasses maxRecipients cap; CRLF-in-subject check skipped on reply/forward; idempotency-route pattern inconsistent |
| 4 | API | `conversations.go` — threading/list | done | 🟡 summary aggregates (latest_subject/sender, counts, has_unread) may leak held-message metadata — verify store excludes held; cursor/timestamps ✅ |
| 5 | API | `hitl.go` — approve/reject review queue | done | 🟡 no /v1 approve/reject for the INBOUND review queue (outbound-only); inbound holds are TTL-auto-resolve only; self-approval ceiling ✅ |
| 6 | API | `events.go` — events API + screening_events surface | done | 🟡 events cursor doesn't bind filter identity (drift) + len==limit spurious cursor; screening_events has NO /v1 surface; redeliver idempotency ✅ |
| 7 | API | `webhooks.go` — webhook config/delivery | done | 🟡→🔴 event enum (5 hand-copies) drifted: email.injection_detected MISSING → screening alert unsubscribable (422); SSRF/ownership/secret ✅ |
| 8 | API | `domains.go` — domain verification | done | 🟡 timestamp type inconsistent surface-wide (time.Time here/conversations vs string in messages/webhooks → SDK Date vs string); delete/verify guards ✅ |
| 9 | API | `account.go` — account/limits/usage | done | 🟡 GDPR export omits screening_events (confirms screening-review flag); 🟡 verify requireAccountUser bars agent scope (delete/export keystone → #10) |
| 10 | API | `scope.go` + `middleware.go` — auth/scopes | done | ✅ KEYSTONE: account-scope ceiling holds — #1/#5/#9 cross-refs resolve safe; agent creds barred from account admin + pinned to one agent; 🟡 no Cache-Control: no-store |
| 11 | API | `pagination.go` — cursor contracts | done | 🟡 shared layer doesn't ENFORCE filter-binding (root cause of #6 drift) — add {position,filterSnapshot} helper; unsigned cursor verified safe ✅ |
| 12 | API | `idempotency.go` — idempotency keys | done | 🟡 byte-exact body hash → non-identical retry 422s (SDK retry MUST buffer+resend → verify #17/#19); namespace separation + panic safety ✅ |
| 13 | API | `operations.go` + `errors.go` — views + error envelopes | done | 🟡 AgentView leaks scan THRESHOLDS to agent-scoped creds (injected agent can calibrate evasion); resolveOwnedAgent ✅ (#10 resolved); error envelope best-in-class ✅ |
| 14 | API | `ratelimit.go` — rate limiting | done | 🟡 clientIP trusts leftmost X-Forwarded-For → per-IP limiter spoofable (use trusted hop); layer separation + poll-set fidelity ✅ [API section complete] |
| 15 | SDK | TS `client.ts` — ergonomic layer (parse/reply) | done | ~~double-send~~ WITHDRAWN (mint happens in retry.ts — see #17); surviving: 🟡 no .parse() ergonomic helper; error mapping ✅ |
| 16 | SDK | TS `ws.ts` — WebSocket | done | 🟡 API key in ?token= query (logged) + unbounded buffer (comment promises bound code lacks → OOM); fatal-4xx stop + backoff ✅ |
| 17 | SDK | TS `pagination.ts` + `retry.ts` + `errors.ts` | done | ✅ retry layer best-in-class — RESOLVES #15 (mints idem key in retry.ts; double-send withdrawn) + #12 (byte-identical retry); pager cycle guard ✅ |
| 18 | SDK | Python `client.py` | done | ✅ strong TS parity (explicit per-op retry helpers, account-delete excluded) — cleaner than TS stub-inference; 🟡 no .parse() (both SDKs); 🔵 2 mechanisms could diverge |
| 19 | SDK | Python `websocket.py` + `pagination.py` + `_retry.py` | done | ✅ full TS parity (mints key once → resolves #12/#18); Python MORE defensive (WS generator backpressure, pagination max_pages); 🟡 same ?token= (server-side) |
| 20 | SDK | `webhook-signature` TS↔Python parity | done | ✅ strong security parity (HMAC + constant-time + NaN-replay-trap both handle); 🔵 `now` unit differs (ms vs s) + TS utf8 round-trips body [SDK section complete] |
| 21 | MCP | `tools/agents.ts` | done | 🔴 update_agent exposes RETIRED hitl_enabled/hitl_mode (silent no-op server-side) + NO new screening config → screening unconfigurable via MCP; tool hygiene ✅ |
| 22 | MCP | `tools/messages.ts` + `attachments.ts` | done | ✅ held-message read boundary HOLDS through MCP (get_message/labels inherit server guard); attachments multipart-bomb-safe; 🔵 stale 'HITL enabled' framing (outcome still correct) |
| 23 | MCP | `tools/hitl.ts` | done | 🟡 outbound-only (no inbound review release — mirrors #5); 🟡 approve_message destructiveHint:false despite gating an irreversible send (LLM could self-release → collapse HITL); best-in-surface idempotency doc ✅ |
| 24 | MCP | `tools/webhooks.ts` + `events.ts` + `domains.ts` | pending | |
| 25 | MCP | `server.ts` + `session.ts` + `client.ts` — transport/auth/pagination | pending | |
| 26 | MCP | `tools/tiers.ts` + `util.ts` — scope gating | pending | |

---

## Findings

<!-- Each iteration appends a "### N. <area> — <subcomponent>" section here. -->

### 23. MCP — `tools/hitl.ts` (review queue)

The idempotency guidance here is the best on the whole surface. Two findings: the inbound-release gap (mirrors #5) and an annotation that's in tension with HITL integrity.

**🟡 Review-queue tools are outbound-only — no inbound review release (mirrors #5 on the MCP surface).** All four tools (`list_pending_messages`/`get_pending_message`/`approve_message`/`reject_message`) operate on the **outbound** `pending_approval` queue (`hitl.ts:11,26,66,104`). There is no list/approve/reject for the **inbound** `pending_review` queue the screening framework fills (inbound_scan / inbound_policy `review`). So an inbound message held for review can only be resolved via the dashboard or TTL auto-expiry — no programmatic path exists at MCP *or* `/v1` (#5). When #5's inbound-release API lands, mirror it here.

**🟡 `approve_message` is annotated `destructiveHint:false`, but it releases a human-review-gated, irreversible send.** Approving fires a real SES send of a message that was deliberately held for a human (`hitl.ts:67`). An autonomous LLM client that reads `destructiveHint:false` as "safe to call freely" could **self-release held drafts, collapsing HITL into no-review**. The real protection is the server-side self-approval ceiling (#5 ✅: an agent-scoped credential can't approve), so the exposure is specifically a deployment that drives this MCP with an **account-scoped** credential under an LLM. *Fix:* add a `confirm:true` guard (as `delete_agent` does) and/or annotate the consequence truthfully — don't signal "non-destructive" on the one action whose entire purpose is to gate an outward send.

**🔵 Stale "HITL-enabled agents" framing.** `list_pending_messages` says it lists holds "from HITL-enabled agents" (`hitl.ts:14`); post-Slice-5 the holds come from `outbound_policy`/`outbound_scan` review. Doc-only (same theme as #21/#22).

**✅ Best-in-surface idempotency guidance** (`hitl.ts:79–84): correctly warns that approve "fires a real SES send, so a retried call without this header could double-send," suggests the pending `message_id` as the stable key for approve-as-is, **and** explains the #12 interaction precisely — "if you change overrides between attempts, pick a fresh key per attempt: same key + different body returns 422." This is the clearest idempotency doc anywhere in the API/SDK/MCP surface.

**✅ Also clean:**
- **Override semantics**: omit-to-keep / pass-to-override, with an explicit `attachments: []` preserved as a strip via key-presence mapping (`mapOverrides`, `hitl.ts:37–61`) — the subtle "empty array means strip, absent means keep" distinction is handled correctly.
- **Body scrub on terminal**: documented that draft content is present only while `pending_approval` and scrubbed after a terminal transition (`hitl.ts:26,107`).
- **Minimal surface**: the client discovers the owning agent from the pending queue, so the caller passes only `message_id` (no agent address threading).

### 22. MCP — `tools/messages.ts` + `attachments.ts`

Much healthier than #21 — the one screening-staleness here is doc-only (the *outcome* guidance is still correct), and two important security properties are verified to hold through the MCP layer.

**🔵 Send/reply/forward descriptions still explain holds as "if the agent has HITL enabled" — stale framing, but the operational guidance is correct.** `send_message` (`messages.ts:55`: "If the agent has HITL enabled, the response is `{ status: pending_approval … }` … do not retry"), and reply/forward echo it (`messages.ts:115,170`). Post-Slice-5, there is no `hitl_enabled` toggle — holds now come from `outbound_policy`/`outbound_scan` review. **Crucially, unlike #21 this isn't a functional break**: outbound review still yields `status=pending_approval`, so the "pending_approval is success, don't retry" guidance an LLM acts on is *right*; only the *cause* clause is outdated. *Fix:* reword the trigger ("if the agent's outbound policy or content scan holds it for review"). Reinforces the #7/#21 hand-written-MCP-staleness theme at the lowest severity.

**✅ The held-message read boundary holds through MCP (important).** An agent learns a held message's ID from the `email.injection_detected` webhook — but `get_message` (`messages.ts:412`) and `update_message_labels` (`messages.ts:250`) both call the same `/v1` API, which enforces the `heldInboundStatuses` guard server-side. So a `pending_review` inbound message **404s** on read and **can't be mutated/oracled** via labels through the MCP — the `TestHeldMessage_MutationGuard` protection is *inherited, not bypassed*. The MCP adds no read path around it.

**✅ Idempotency docs are accurate to the SDK reality (#15/#17).** `send_message`'s `idempotency_key` note (`messages.ts:72`: "When omitted the SDK mints a fresh UUIDv4 per call — protects against network-layer retries only, not user-driven retries") correctly describes the `ensureIdempotencyKey` behavior — the MCP layer documents the network-retry-only semantics honestly, and suggests the inbound `message_id` as a stable key for reply/forward.

**✅ Attachment handling is excellent + multipart-bomb-safe:**
- `get_message` **omits** attachment bytes and `raw_message` (context-budget protection), returning only `{index, filename, content_type, size_bytes}`; `get_attachment` enforces a **2 MB** inline-fetch cap with an out-of-band-handling directive; indexes are 0-based/stable; the returned `{filename, content_type, data}` is verbatim-usable in `send`/`reply` attachments (clean forward workflow); base64 is decoded *before* `simpleParser` (a real correctness trap avoided).
- `attachments.ts` input caps are robust: **5 MB/attachment**, **20/message**, filename 1–255, and a base64 **decode + round-trip + length%4** validation that rejects context-truncated data — so the send path can't be used for a multipart bomb (backend request cap is the final backstop).

**🔵 Minor:** `get_attachment` re-fetches + fully re-parses the whole MIME per call (fine for the 1–2-attachment inline-share case); reply_all/forward inherit the server-side #3 gaps (reply_all vs `maxRecipients`, CRLF-in-subject) — fix those at the API, not here.

### 21. MCP — `tools/agents.ts`

The tool hygiene is excellent, but the agent-config surface is **badly stale against the screening retirement** — the single most impactful finding of the review so far, and the MCP twin of the #7 webhook-enum drift.

**🔴 `update_agent` exposes the RETIRED `hitl_enabled`/`hitl_mode` (now a silent no-op) and exposes NONE of the new screening config.** The tool's input schema still has `hitl_enabled` (`agents.ts:92–97`) and `hitl_mode` (`agents.ts:108–113`), the description calls itself "**The path to enable HITL approval gates** … set `hitl_enabled: true` … `hitl_mode`" (`agents.ts:82–83`), and the patch builder sets `hitlEnabled`/`hitlMode` (`agents.ts:136,141`). But Slice 5b/5c **removed those fields from the `/v1` PATCH and dropped the DB columns**. Huma ignores unknown body fields, so an MCP agent calling `update_agent({hitl_enabled: true})` gets a **success response while HITL is silently NOT enabled** — a dead control that *looks* like it worked. Worse, the tool exposes **none** of the screening config that replaced it — no `outbound_policy`/`outbound_allowlist`/`outbound_policy_action`, no `inbound_policy_action`, no `inbound_scan`/`outbound_scan` + thresholds. **Net: an MCP agent cannot configure screening at all, and the one control it *can* "set" does nothing.** Root cause (same as #7): Slice 5b/5c regenerated the *generated* surfaces (`/v1` spec, SDK bases) but the **hand-written** MCP tools weren't updated. *Fix:* replace the `hitl_enabled`/`hitl_mode` inputs with the surviving `hitl_ttl_seconds`/`hitl_expiration_action` + the new `outbound_policy`/`outbound_scan`/`inbound_*` fields; rewrite the description around the gate/scan model. (`get_agent`'s description also lists the dead `hitl_enabled/hitl_mode`, `agents.ts:25`.)

**🔵 If thresholds are added, mind #13.** When wiring the new scan config into `update_agent`/`get_agent`, don't echo the scan *thresholds* back to an agent-scoped credential (the #13 disclosure) — the MCP `get_agent` is agent-reachable.

**✅ Verified clean (tool hygiene is genuinely good):**
- **Annotations**: `readOnlyHint` on list/get/whoami; `destructiveHint:true` + `idempotentHint` on delete; `destructiveHint:false` on create/update — correct MCP semantics for an LLM client.
- **Delete safety**: `confirm: z.literal(true)` schema guard **plus** a server-side `confirm !== true` throw (`agents.ts:166–179`) — double-guarded against a hallucinated delete.
- **Scope ergonomics**: `email` is optional and "defaults to the credential's bound agent (agent-scoped)" — matches the API's pinning; `whoami`'s description is exemplary ("identity, not an agent — never guesses a 'default' agent").
- **`strictInputSchema`** everywhere (rejects unknown args — no silent typo'd params).

### 20. SDK — `webhook-signature` TS↔Python parity — *SDK section complete*

The security-critical bits are correct and matched in both languages — including a subtle replay-bypass guard both got right. Two minor cross-language consistency nits.

**🔵 The `now` override parameter has different UNITS across the SDKs.** TS `now?: () => number` defaults to `Date.now()` (**milliseconds**) and divides by 1000 internally (`webhook-signature.ts: Math.abs(nowMs/1000 - ts)`); Python `now: Optional[float]` defaults to `time.time()` (**seconds**) and compares directly (`abs(now - ts)`). The runtime defaults are each correct, but the *public test-override* is ms in TS and seconds in Python — a developer porting test fixtures between SDKs will silently break the tolerance check. *Fix:* unify the unit (both seconds is the natural choice) or document the divergence loudly.

**🔵 TS HMACs over a UTF-8 round-trip of the body; Python over raw bytes.** TS does `body = rawBody.toString("utf8")` then `hmac.update(`${t}.${body}`)` (`webhook-signature.ts`), while Python signs `t.encode() + b"." + raw_body_bytes`. For valid-UTF-8 JSON (the only real case) they agree, but a body with non-UTF-8 bytes gets *lossily re-encoded* in TS → TS would reject a delivery Python (and the server, which signs the raw bytes) accept. Low impact (webhook bodies are UTF-8 JSON), but TS should `hmac.update()` the raw `Buffer` directly to be byte-exact with the server.

**✅ Verified clean — strong security parity:**
- **Scheme**: HMAC-SHA256 over `{t}.{rawBody}`, Stripe-style `t=…,v1=…[,v1=…]`; multiple `v1=` pairs accepted during the 24h rotation grace (any-match); RAW body required (re-stringified JSON won't match) — documented identically in both.
- **Replay guard incl. the NaN trap** ✅: both reject a **non-finite** timestamp (`Number.isFinite`/`math.isfinite`) *before* the tolerance check — without it, `abs(now − NaN) > tol` is `false`, which would silently **disable** the replay guard for a crafted `t=nan` delivery. Both SDKs explicitly handle this subtle bypass (Python even comments it). Default tolerance 300s.
- **Constant-time compare**: TS `timingSafeEqual` (length-guarded, hex→bytes), Python `hmac.compare_digest` (hex strings) — no timing oracle; an odd/invalid-hex candidate is length-rejected, not mis-decoded.
- **Robustness**: missing/non-string header → clean `false` (never throws); `verify…` returns bool, `constructEvent`/`construct_event` verifies + parses + throws a typed `E2AWebhookSignatureError` with matching codes (`webhook_signature_invalid`/`webhook_body_invalid`); `data` is `unknown`/`Any` pending per-type schemas (same tracked follow-up as the events `data` / error `details`).

---

> **SDK section complete (#15–20).** Verdict: **the SDKs are in excellent shape.** The retry/idempotency/pagination/signature cores are best-in-class and consistent across TS↔Python (the scary #15 double-send was a false alarm; signature verification is correct incl. the NaN replay-trap). Action items are small and mostly polish: bound the TS WS buffer (#16; Python's generator is the reference), ship `.parse()` in both or drop the promise (#15/#18), unify the `now` unit + TS raw-byte HMAC (#20), a cross-SDK conformance test for the retried-op set (#17/#18), and the server-side WS header/ticket auth (#16/#19).

### 19. SDK — Python `_retry.py` + `websocket.py` + `pagination.py`

Full TS parity, the minting cross-ref resolved, and Python is actually *more* defensive than TS in two places. One finding (the `?token=` exposure) is confirmed as a both-SDK / server-side issue.

**✅ `_retry.py` resolves #12/#18 minting in Python too.** When `idempotency=True` and the caller omits a key, it mints `uuid.uuid4().hex` **once** and passes it via `_headers` on every attempt, reusing the body bytes (`_retry.py:113–115`, header doc lines 4–11) — same contract as TS `ensureIdempotencyKey`. Retry gating (`is_retryable_status` = 429/5xx/connection; non-transport httpx errors not retried) and backoff (Retry-After capped at 60s, else exp + **full jitter** `0.5+0.5·rand`) match TS.

**🟡 `websocket.py`: same `?token=` credential exposure as TS (#16) — confirms it's server-side, not per-SDK.** The handshake passes auth as `?token=` (`websocket.py:111–112`, documented identically: "can land in server/proxy access logs"). Both SDKs do this because the WS endpoint *requires* it — so the fix is the planned server-side header/ticket auth (#16), and both SDKs update in lockstep when it lands. Reinforces, doesn't add.

**✅ Python AVOIDS the TS unbounded-buffer bug (#16).** `websocket.py` is a pure async generator (`async for … yield`) — the consumer's iteration *is* the backpressure; there's no internal buffer to balloon. So #16's unbounded-buffer is **TS-specific** (the EventEmitter↔AsyncIterable bridge in `WSStream`), and Python is the reference for the correct shape. Strengthens the case that TS `ws.ts` should bound its buffer.

**✅ `pagination.py` parity + an extra backstop.** Cycle guard (`seen` set + non-advancing-cursor → `RuntimeError`, `pagination.py:53,69–71`) matches TS, **plus** a hard `max_pages=10_000` ceiling (`pagination.py:42,57`) that TS lacks — a second safety net against a pathological cursor stream. `to_list` requires a positive `limit` (memory cap), like TS `toArray`.

**🔵 `received_at` string + backoff reset.** WS `received_at` is a string (folds into #8); backoff resets to 1s on a successful message (parity with TS) — both fine.

> **Net: the SDK retry/idempotency/pagination core is excellent and consistent across both languages** — the only real SDK action items are (a) bound the TS WS buffer (#16, Python shows the fix), (b) ship `.parse()` in both or drop the promise (#15/#18), (c) the server-side WS-auth move (#16/#19), and (d) a cross-SDK conformance test pinning the retried/keyed op set (#17/#18).

### 18. SDK — Python `client.py` (cross-language parity)

Excellent parity with the TS SDK — and the per-op retry classification is *cleaner* than TS's. Two findings: a confirmed cross-SDK gap and a maintenance/divergence risk.

**✅ Retry/idempotency classification matches TS exactly — via explicit, more robust helpers.** Python routes every op through one of four executors (`client.py:158–172`): `_read` (GET, retryable, no key), `_write_keyed` (send/reply/forward/approve → retryable + idempotent), `_write_idempotent` (PUT/PATCH/DELETE), `_write_unsafe` (create/reject/verify/redeliver/test → **not** retried). The op→helper mapping is identical to TS's `isRetrySafe`, **including the account-deletion exclusion** (`client.py:540`: "Deliberately NOT retried, unlike the other DELETEs… account deletion is irreversible"). Unlike TS — which *infers* POST retry-safety from the generated `Idempotency-Key` stub (the #17 🔵) — Python classifies each op **explicitly**, which is harder to get wrong.

**🔵 Two SDKs classify retry-safety by two different mechanisms → divergence risk.** TS = generated-stub inference; Python = explicit per-method helper choice. A new op could be classified differently in each (e.g. someone picks `_write_unsafe` in Python while TS auto-retries it because the stub is emitted, or vice-versa). They agree *today*, but nothing enforces it. *Fix:* a shared conformance fixture (a table of `op → {retried?, keyed?}`) both SDK test suites assert against — also closes the #17 🔵.

**🟡 No `.parse()` ergonomic helper here either — confirms it's a both-SDK gap.** `client.py` has no `parse` method (grep clean); like TS (#15), the agent-native "raw MIME → clean text to feed a model" helper the design promised is absent in *both* SDKs. So it's a deliberate omission or a genuine missing feature across the board, not a one-language miss — worth a product decision: ship `messages.parse()` in both, or drop the promise from the docs.

**✅ Verified clean:**
- **Typed input validation**: `_coerce` (`client.py:84–99`) runs pydantic `model_validate` and raises a typed `E2AValidationError(invalid_request_body)` rather than leaking a raw pydantic error — clean caller-error surface.
- **Lifecycle**: async context manager (`__aenter__`/`__aexit__` → `aclose`) so connections close deterministically; `api_key`/`base_url` via arg or `E2A_API_KEY`/`E2A_BASE_URL`; missing key → typed `no_api_key`.
- **Key minting (cross-ref #19)**: `_write_keyed` passes `idempotency=True` with an optional caller key; the actual mint-when-omitted + retry handling lives in `request_with_retry` — verified next in `_retry.py`. (TS parity: there it's `ensureIdempotencyKey`.)

### 17. SDK — TS `retry.ts` + `pagination.ts` + `errors.ts` — *resolves #15 & #12*

Best-in-class retry layer. **It corrects #15 (the "double-send" was a false alarm) and resolves #12 (byte-exact retry) — both SAFE.** This is the most reassuring SDK result.

**✅ CORRECTION to #15 — the SDK *does* mint the idempotency key; the double-send risk does not exist.** I flagged #15 from `client.ts` alone, which passes `undefined` — but the minting happens one layer down. `RetryHttpLibrary.ensureIdempotencyKey` (`retry.ts:165–178`) detects the generated layer's *present-but-empty* `Idempotency-Key` stub (emitted for send/reply/forward/approve/rotate-secret) and **mints a `crypto.randomUUID()`** onto the shared `RequestContext`, so every retry reuses the same key. `client.ts`'s docstring ("the SDK mints one and reuses it across retries") is therefore **accurate** — just implemented in the transport, not the resource layer. **#15's 🟡→🔴 is withdrawn**; the only surviving #15 item is the missing `.parse()` ergonomic helper.

**✅ Resolves #12 — retries are byte-identical.** `sendWithRetry` re-sends the **same `RequestContext`** (`retry.ts:64–102`), so the already-serialized body bytes + the minted key are reused verbatim across attempts — exactly what the server's raw-byte hash needs. No 422-on-retry. The module header documents this precisely.

**✅ Per-method retry gating is exactly right** (`isRetrySafe`, `retry.ts:121–138`): retries GET/HEAD/OPTIONS (no side effects) and PUT/PATCH (HTTP-idempotent); DELETE **except account deletion** (irreversible, would surface a spurious 404); POST **only** when an `Idempotency-Key` is present — so the *non-keyed* POSTs (create agent/domain/webhook, reject, verify, redeliver, test) are **never retried**, preventing double-create. Mirrors the Python gating.

**🔵 POST retry-safety is coupled to the generated stub.** The "is this a server-deduped POST" decision depends on the generated layer emitting the `Idempotency-Key` stub for *exactly* the right ops (`retry.ts:140–149`). If the OpenAPI ever marks a new op with the header (or drops it), retry behavior silently changes. *Fix:* a unit test that pins the retried set to exactly `{send, reply, forward, approve, rotateSecret}` and asserts the others aren't — so a generation change can't quietly alter retry semantics.

**✅ Also verified clean:**
- **Backoff** (`retry.ts:184–209`): honors `Retry-After` (seconds *or* HTTP-date) capped at `maxRetryAfterMs` (so a hostile upstream can't wedge a request for years), else exponential with **full jitter**; `maxElapsedMs` total-deadline guard; backoff sleep races the `AbortSignal` for prompt cancellation.
- **`errors.ts`**: complete typed hierarchy (auth/permission/not-found/conflict/validation/idempotency/rate-limit/server/connection/webhook-signature) mapped from the envelope `code`, with a **status-based fallback** so a *new* server code still maps to a sane class (no drift). `isRetryableStatus` = 408/429/5xx — consistent with the retry layer.
- **`pagination.ts`**: `AutoPager` has a **cycle guard** (`seen` set + non-advancing-cursor check → throws a clear error instead of looping, `pagination.ts:37–56`) and `toArray` requires an explicit `limit` (memory cap). Correctly handles the single-page "looks-paginated-but-isn't" endpoints.

### 16. SDK — TS `ws.ts` (WebSocket listener)

Well-engineered reconnect/iteration logic with good Python parity. Two real 🟡s: a credential-in-URL exposure (acknowledged) and a comment-vs-code memory bug.

**🟡 API key rides in the `?token=` query string → credential logging exposure.** The handshake URL embeds the key: `…/ws?token=${apiKey}` (`ws.ts:90`). The docstring is honest about it (`ws.ts:67–71`: "Query strings can leak into access logs and proxy traces… a known logged-credential limitation; moving auth to a header or short-lived ticket is planned server-side"). It's a real exposure for **long-lived** `e2a_agt_`/`e2a_acct_` keys — they land in proxy/LB/access logs verbatim. Notably the Node `ws` library *does* support handshake `headers`, so the SDK could send `Authorization: Bearer` today *if the server accepted it* — the blocker is server-side. *Action:* prioritize the planned header/connect-ticket auth; until then, consider a short-lived WS-connect token instead of the raw long-lived key, so a logged value expires quickly.

**🟡 Unbounded notification buffer (comment promises a bound the code doesn't implement).** `WSStream.buffer` is documented as "Modest bound; if a consumer is far behind we'd rather log loudly than balloon memory" (`ws.ts:186–188`), but `deliver` just does `this.buffer.push(notif)` with **no cap and no log** (`ws.ts:252–258`). A consumer that stalls its `for await` (or only uses the EventEmitter without iterating) makes the buffer grow without limit — OOM on a busy inbox. *Fix:* implement the documented behavior — cap the buffer (drop-oldest or emit a typed `backpressure` error) and log loudly when exceeded.

**🔵 `received_at` is a `string`.** `WSNotification.received_at` (`ws.ts:32`) is a string, consistent with the message-view string timestamps — folds into the #8 timestamp-type split (some Date, some string).

**✅ Verified clean:**
- **Fatal-handshake handling** (`ws.ts:9–14,118–149`): a 4xx handshake rejection maps to a typed `E2AAuthError`/`E2APermissionError` and **stops** (no reconnect), so bad credentials don't loop forever — F6 parity with Python. The noisy transport error alongside a fatal handshake is suppressed.
- **Backoff**: exponential 1s→…→`maxBackoffMs` (30s) with **reset on successful open** so flapping doesn't ratchet the delay; matches Python's shape.
- **Hybrid iteration**: `WSStream` resolves/buffers correctly and `drainWaitersWithError` makes a `for await` **throw the typed error** on a fatal disconnect rather than hang — the right ergonomics.
- **Light protocol**: notification-only (no body); fetch via REST — keeps the socket cheap and the body behind the held-message read boundary.

### 15. SDK — TS `client.ts` (ergonomic layer)

Clean typed wrapper with good error mapping. But the idempotency story has a docstring-vs-code contradiction that, combined with the auto-retry layer, risks double-sends — the most serious SDK finding so far.

**🟡 (🔴 if `retry.ts` retries POST) — auto-retried sends are NOT idempotent; the docstring claims minting the code doesn't do.** `RequestOptions.idempotencyKey` is documented as "Omit and the SDK mints one (and reuses it across retries)" (`client.ts:80–83`). But `send`/`reply`/`forward`/`approve` pass `opts.idempotencyKey` **straight through** (`client.ts:231,234,237,240`) — when the caller omits it, **`undefined`** reaches the server, so `runIdempotent` runs with no key (idempotency off). Meanwhile every client is wrapped in `RetryHttpLibrary` that retries on "429/5xx/connection" (`client.ts:72,129`). So a `send` that commits at SES but returns a 5xx (e.g. the post-send DB write fails) or whose response is lost to a connection drop gets **retried with no idempotency key → a duplicate email**. The docstring is simply false as written. *Fix:* actually mint a key when omitted (e.g. `opts.idempotencyKey ?? crypto.randomUUID()`) and thread the *same* value through retries — the docstring already describes the correct behavior; the code needs to implement it. **Confirm in #17 (`retry.ts`) whether POST is retried** — if yes, this is 🔴 (silent double-send on a transient failure, exactly what idempotency exists to prevent). Ties to #12 (the server is ready; the SDK isn't using it).

**🟡 No `.parse()` ergonomic helper.** The design's agent-native value-add was `client.messages.parse()`/`.reply()` (raw MIME → clean text for feeding a model). `.reply()` exists but is just the typed API call; there is **no** `.parse()` here (`client.ts` is a thin resource wrapper). For the headline "feed the model by default" use case, the consumer is left to parse `raw_message`/`parsed` themselves. Ergonomic gap vs the stated SDK promise — confirm `parse` isn't living elsewhere; if not, it's a missing feature.

**✅ Verified clean:**
- **Typed error mapping** (`call()`, `client.ts:94–102`): `ApiException` → envelope-mapped `E2AError`, `E2AError` passes through, transport throws → `connectionError` — one typed hierarchy.
- **Pager correctness**: `agents.list` (and other non-cursor lists) deliberately omit `next_cursor` so `AutoPager` stops after one page instead of re-fetching page 1 and tripping the cycle guard (`client.ts:176–180`) — correct handling of the "looks-paginated-but-isn't" endpoints (#7/#11).
- **Ergonomic delete**: `.delete()` auto-sends `?confirm=DELETE` (the typed call *is* the confirmation; the guard is for raw/curl callers).
- **Config**: `apiKey`/`baseUrl` via constructor or `E2A_API_KEY`/`E2A_BASE_URL`; missing key throws a typed `no_api_key` before any request.

### 14. API — `ratelimit.go` (rate limiting) — *API section complete*

Thoughtful layering (poll vs registration vs in-handler send) with the legacy set replicated exactly. One real anti-abuse weakness in client-IP derivation.

**🟡 `clientIP` trusts the client-supplied `X-Forwarded-For` (leftmost hop) → per-IP limiter is spoofable.** `clientIP` (`ratelimit.go:138–147`) takes the **first** value of `X-Forwarded-For`, which is the most attacker-controllable field in the request — if the app is ever directly reachable, or sits behind a proxy that *appends* (rather than overwrites) XFF, a caller rotates the header per request and gets a fresh rate-limit key each time, defeating the per-IP `createAgent` registration limiter. (Impact here is bounded by the authenticated per-user agent cap that also gates `createAgent`, so it's defense-in-depth on this op — but the same `clientIP` pattern keys any per-IP limiter, where it may be the *primary* control.) *Fix:* derive the client IP from a *trusted* hop — a configured trusted-proxy depth (take the Nth-from-right), or fall back to `RemoteAddr` when no trusted proxy is configured — rather than the spoofable leftmost value. At minimum, document that the edge MUST overwrite `X-Forwarded-For`.

**🔵 `RateLimit-*` headers only on the middleware-enforced limits.** The poll + registration limiters set IETF `RateLimit-Limit/Remaining/Reset` (+ `Retry-After`), but the **send** limiter runs inside the outbound handlers (`checkSendLimit`) where a Huma error can't set response headers — so a send-rate 429 carries `retry_after_seconds` in the body but **no** `RateLimit-*` headers. Inconsistent 429 shape across limiters; already noted as a follow-up in #3.

**✅ Verified clean:**
- **Layer separation**: the per-agent **send** limiter is correctly enforced *in* the handler (its key is the resolved-owned agent, which needs the ownership check this middleware doesn't do) — documented (`ratelimit.go:39–45`).
- **Poll set fidelity**: `pollLimitedOps` mirrors the legacy surface exactly (verified against `origin/main`) and deliberately excludes the events/reconciliation reads so they don't compete for the 60/min message-read budget.
- **Auth precedence**: an unauthenticated request is passed through so the handler emits the canonical 401 rather than masking a missing credential as a rate-limit decision (`ratelimit.go:62–67`).
- **Principal reuse**: the middleware stashes the resolved principal so the handler skips a second auth on the hot read path; the middleware error envelope is request-id-stamped to match the handler path.

---

> **API section complete (#1–14).** The auth/scope foundation is solid (#10/#13). The open work clusters into two themes: **(A) drift from hand-maintained duplicates** — webhook event enum (#7, *breaks injection alerts*), timestamps (#8), cursor filter-binding (#6/#11), label rules (#2/#7); and **(B) screening under-exposure** — no inbound-review release API (#5), `screening_events` absent from `/v1` + GDPR export (#6/#9), thresholds leaked to agents (#13). A consolidated summary will follow the SDK/MCP rows.

### 13. API — `operations.go` + `errors.go` (views, `resolveOwnedAgent`, error envelope)

The error envelope is a model of "spec-as-source-of-truth," and `resolveOwnedAgent` resolves the #10 companion cleanly. One subtle screening-related disclosure is worth a decision.

**🟡 `AgentView` exposes the scan thresholds to agent-scoped credentials.** `getAgent` goes through `resolveOwnedAgent` (any scope, ownership+pinning) and returns the full `AgentView` including `inbound/outbound_scan_review_threshold` and `…_block_threshold` (`operations.go:106–111`). So an **agent-scoped credential — the very entity being screened, and the one a prompt injection would compromise — can read its own detection thresholds.** An injected agent can GET itself, learn `outbound_scan_block_threshold`, and calibrate exfil content to score just under it, undermining the egress firewall. The *write* path is account-only (#1), but the *read* isn't gated. *Fix:* omit the scan thresholds (and arguably the gate config) from the `AgentView` returned to agent-scoped callers — the agent doesn't need to know its own detection tuning; the operator sets it. (Account scope still sees everything.)

**🔵 `details` is schema-less (`any`).** `ErrorBody.Details any` (`errors.go:49`) varies by code — a `{resource,limit,current}` map for `limit_exceeded`, an array of field errors for validation — so the OpenAPI types it as untyped and the SDKs surface it as `unknown`/`object`. Clients must know the per-code shape out-of-band. Inherent to a polymorphic field; worth a doc note mapping each error `code` to its `details` shape.

**✅ #10 cross-ref RESOLVED — `resolveOwnedAgent` is the sound per-agent choke point** (`operations.go:181–202`): it enforces **ownership** (`ag.UserID != p.User.ID → 403`, the thing #10 needed) **and** agent-scope **pinning** (`p.Scope==agent && p.AgentID != ag.ID → 403`), and reports missing-vs-non-owned identically (403 "agent not found") so there's **no existence oracle**. Combined with #10, per-agent authz is fully closed: account creds can't touch un-owned agents, agent creds can't pivot to siblings.

**✅ The error envelope is best-in-class:**
- **Single shape, drift-proof**: `humaErrorConstructor` is installed as the global `huma.NewError` (`errors.go:160`), so *Huma's own* validation/content-negotiation errors render in the same `{error:{code,message,details,request_id}}` envelope — the error contract literally cannot diverge.
- **Always-branchable code**: `defaultCodeForStatus` (`errors.go:82`) guarantees even a status-only error carries a stable `code`; field-level validation detail is preserved into `details` (`huma.ErrorDetailer`).
- **Correlation**: `stampRequestID` copies the per-request id into the error body to match the `X-Request-Id` header.
- **AgentView uniformity**: one shape across create/get/update/list.

### 12. API — `idempotency.go` (idempotency keys)

Carefully built and honestly documented (the at-least-once degradation is stated, not hidden). The one finding is a cross-language ergonomic hazard that the SDK retry layers must absorb.

**🟡 Byte-exact body hash → a logically-identical retry can 422 instead of replaying.** The dedup hash is over the **raw request bytes** (`route + "\n" + body`, `idempotency.go:37–40,70`), not canonicalized JSON. So a retry with the same `Idempotency-Key` must resend **byte-identical** JSON; any reserialization difference (map/object key ordering, whitespace, a re-`JSON.stringify` on retry) is treated as a key-reuse-with-different-body and returns **422 `idempotency_key_reuse`** — the opposite of what a retrying caller wants. The comment names this ("A retry must therefore resend byte-identical JSON or it 422s"). It's safe and simple, but it pushes a hard requirement onto clients: **the SDK retry path MUST buffer the original bytes and resend them verbatim, never rebuild the body.** A hand-rolled client that reconstructs the body on retry will intermittently 422 on a legitimate retry. *Action:* this is the load-bearing thing to verify in the SDK retry reviews (#17 TS `retry.ts`, #19 Python `_retry.py`) — confirm both buffer-and-resend; if either re-serializes, it's a real bug. Optionally document the byte-exact requirement on the `Idempotency-Key` header in the spec.

**🔵 Marshal-failure caches `{}`.** If the success response fails to marshal (`idempotency.go:111–114`), an empty `{}` body is cached (status preserved) rather than risk a replay re-running the side effect. Correct priority (no double-send) — but a replayed request then gets `{}` instead of the real payload. Rare; acceptable.

**✅ Verified clean:**
- **Namespace separation** (`idemUserNS "u:"` vs `idemAutoNS "s:"`, `idempotency.go:24–27`): caller-supplied and server-minted keys occupy disjoint key spaces, so a crafted `Idempotency-Key: replay:evt_x:` can't 422-poison a later genuine auto-redelivery. This is the mechanism behind #6's ✅.
- **Load-bearing body hash**: same key + different body → 422, never a silent replay of the first response — the strict, correct semantics.
- **Crash/panic safety**: `defer recover()` releases the claim so a panic doesn't 409-lock retries; the guarantee is documented as at-least-once (a panic strictly after the committed side effect can re-run on retry) — honest, not overclaimed.
- **Opt-in + byte-faithful replay**: no key / no store → just runs `fn`; replay unmarshals the cached bytes back into `T` and returns the original status.

### 11. API — `pagination.go` (shared cursor machinery)

The envelope is clean and the unsigned cursor is (correctly) *not* a security boundary. The one architectural finding is the root cause of the #6 cursor drift: the shared layer serializes but doesn't *enforce* the filter-binding invariant.

**🟡 The cursor layer doesn't enforce filter-identity binding — so each resource re-implements it, and #6 forgot.** `EncodeCursor`/`DecodeCursor` (`pagination.go:52–76`) marshal/unmarshal an arbitrary payload; the "snapshot the filters + reject a changed-filter continuation" logic is hand-rolled per handler (`messages.go` binds 10 fields, `conversations.go` 3, **`events.go` zero**). Because the shared machinery makes *position-only* the path of least resistance, drift is inevitable — `events.go` is the proof. *Fix:* add a shared helper that bundles `{position, filterSnapshot}` and, on decode, compares `filterSnapshot` against the request's current filters → `ErrInvalidCursor` on mismatch. Then filter-binding is the default and a resource *can't* silently ship a position-only cursor. This single change closes the #6 class at the source.

**🔵 `PageParams` isn't applied uniformly — limit bounds drift.** The comment (`pagination.go:36–38`) says `cursor`+`limit` are "declared, typed, and validated identically across the surface," but `events` (max 200), `webhook deliveries` (max 500), and `conversations` (default 100) declare their *own* `Limit` field instead of embedding `PageParams` (max 100/default 50). So the per-endpoint caps are 50/100/200/500 — not identical. Either embed `PageParams` everywhere (and parameterize the cap) or drop the "identical" claim.

**✅ Verified clean — incl. the unsigned-cursor question (important):**
- **The plain `base64(JSON)` cursor is NOT forgeable into an escalation.** It carries no load-bearing authz: `AgentID` in the cursor is re-validated against the path agent (which comes from `resolveOwnedAgent`, not the cursor), the filter snapshot is re-validated against the request, and the `(created_at, id)` position only resumes *within already-authorized data*. A tampered cursor either fails the filter-identity check or just reorders the client's own rows — no cross-tenant reach. So skipping an HMAC here is a justified choice, not a hole. (A one-line code comment stating this would pre-empt the reviewer reflex to "sign the cursor.")
- **Uniform envelope**: `Page[T]` = `items` (always `[]`, never `null`) + `next_cursor` (`null` on last page) — one shape across every collection.
- **Stable error**: a malformed cursor → `ErrInvalidCursor` sentinel → clean 400 `invalid_cursor`; empty cursor = start-from-beginning. `DecodeCursor` into a fixed per-resource struct bounds what an oversized cursor can do.

### 10. API — `scope.go` + `middleware.go` (auth/scopes) — KEYSTONE

**The account-admin scope ceiling holds — the accumulated cross-refs from #1/#5/#9 resolve to ✅.** This is the most important positive result of the review so far. The findings are minor by comparison.

**✅ KEYSTONE — agent-scoped credentials are correctly barred from account administration.** `requireAccountScope` (`scope.go:26–36`) authenticates, then rejects any `p.Scope != ScopeAccount` with a 403 `forbidden`; `requireAccountUser` (`scope.go:41–47`) is a thin wrapper over it. So **every** handler that gates on `requireAccountUser`/`requireAccountScope` — agent create/delete (#1), config PATCH, approve/reject (#5), account delete + export + suppressions (#9) — structurally cannot be reached by an agent-scoped token. A leaked agent credential **cannot** delete the account, export all data, mint agents, or self-approve. The three iterations that deferred their headline security question to here are all **resolved safe**.

**✅ Agent-scoped pinning.** `requireAgentAccess` (`scope.go:54–64`) pins an agent-scoped credential to its *one* bound agent (`p.AgentID != agentID → 403`) even when the same owner owns the target — so a leaked agent token can't pivot to a sibling agent. Clean 401 (no/invalid credential) vs 403 (valid-but-insufficient-scope) separation throughout.

**🟡 No `Cache-Control: no-store` on authenticated responses.** `securityHeaders` (`middleware.go:142–147`) sets only `X-Content-Type-Options: nosniff`. Several responses carry secrets — `signing_secret` on webhook create/rotate, `verification_token`, and `raw_message`/auth headers on message detail — with no cache-control directive. For a Bearer API the practical risk is low (intermediaries shouldn't cache `Authorization`-bearing requests), but `Cache-Control: no-store` on authenticated responses is the defense-in-depth standard and cheap to add at this choke point. *Fix:* set `no-store` for non-public ops (leave the public `getInfo` cacheable).

**🔵 `resolveOwnedAgent` lives elsewhere (companion to this file).** The per-agent ownership+pinning helper the message/outbound/conversation handlers use isn't in these two files — it's the runtime-tier analog of `requireAgentAccess` and is reviewed with `operations.go` (#13). Flagging so the pair stays linked: `requireAgentAccess` covers scope; `resolveOwnedAgent` must cover *ownership* (an account-scoped creds acting on an agent it doesn't own).

**✅ Verified clean (middleware):**
- **WWW-Authenticate on 401** (`middleware.go:73–106`): RFC 6750 challenge set from one place keyed on the 401 status (incl. OAuth `error` params so MCP clients trigger the re-flow); 2xx/public responses untouched.
- **WS upgrade preserved**: `challengeWriter.Hijack()` passthrough (`middleware.go:119–124`) keeps the WebSocket upgrader's `http.Hijacker` assertion working — a subtle break avoided.
- **Request id**: honors a caller `X-Request-Id` (cross-service trace) else mints a `crypto/rand` id; on every response + echoed into the error envelope.

### 9. API — `account.go` (whoami / limits / export / delete / suppressions)

Well-built scope-aware account resource. Two findings connect to earlier threads: a confirmed GDPR-export gap, and the load-bearing account-admin-scope cross-ref.

**🟡 GDPR export omits `screening_events` (confirms the screening review's flag).** `handleExportUserData` dumps `Domains/Agents/APIKeys/Messages/UsageEvents/OAuthConnections` (`account.go:192–197`) — but **not** `screening_events`. Those rows are the agent's personal data (the flagged sender/recipient addresses in `subject_addr`, scan `spans`/`categories`) and a right-of-access export should include them. The outbound-retention/screening review already flagged `screening_events` as missing from `ExportUserData`/`DeleteUserData`; this is the same gap surfacing at the API layer. *Fix:* add `ScreeningEvents` to `UserExport` (and confirm the matching erasure in `DeleteUserData`, since the table is a soft-ref that outlives the message TTL and must still be erasable on account delete).

**🟡 Cross-ref (the security keystone): does `requireAccountUser` bar agent-scoped credentials?** Delete-account (`account.go:213`), export (`account.go:178`), and suppressions all gate on `requireAccountUser`. If that helper does **not** reject an agent-scoped token, an agent credential could **delete the entire account** or export all account data — catastrophic escalation. Strong signal it's safe: `handleGetMyLimits` (whoami) deliberately uses `requirePrincipal` *instead* (`account.go:235`) precisely because whoami must work for both scopes — implying `requireAccountUser` is the scope ceiling. **Must confirm in #10 (`scope.go`)** — this is the single most important auth invariant on the surface, and #1's create-scope question folds into the same check.

**🔵 Inconsistent DELETE semantics.** `deleteAccount` returns **200 + body** (`DeleteUserDataResult`, `account.go:209–228`) while agent/domain/suppression deletes return **204 No Content**. The informative body is reasonable, but the inconsistency means a client's delete-handling can't be uniform.

**✅ Verified clean:**
- **whoami dual-scope** (`account.go:231–256`): authenticates any principal; `agent_address` populated only for agent scope (the credential *is* one agent), omitted for account scope. Correct.
- **Export hygiene**: empty collections render `[]` not `null` (A-3, `orEmpty`); `Content-Disposition` filename uses server-controlled `user.ID` (no header injection).
- **Suppressions**: cursor `(created_at, address)` is complete (no filters to bind); un-suppress releases cached idempotency keys so a previously-blocked send then succeeds (`account.go:84`) — thoughtful.
- **Graceful degradation**: every optional dep (`ListSuppressions`/`ExportUserData`/`GetLimits`…) returns 501/503 rather than panicking when unwired.
- **`confirm=DELETE`** required on account delete.

### 8. API — `domains.go` (registration / verify / sending identity)

A clean, well-guarded resource (409 on taken, confirm+has-agents on delete, 412-with-diagnostic on verify). The one cross-cutting finding: this file exposes the timestamp inconsistency the whole surface carries.

**🟡 Timestamp representation is inconsistent across the API (cross-language ergonomics).** `DomainView` serializes timestamps as typed `time.Time` (`domains.go:37–40`, `CreatedAt`/`VerifiedAt`/`LastCheckedAt`) — as does `conversations.go`. But `messages.go` (`messages.go:68`, `122`) and `webhooks.go` (`webhooks.go:45–46`, `314–316`) serialize them as **preformatted RFC3339 `string` + `format:"date-time"`**. Same wire value, but the generated SDKs type the former as a real `Date`/`datetime` and the latter as a plain `string` — so a consumer does `domain.created_at.getTime()` but `message.created_at` is a string they must parse. The `conversations.go:12–15` comment documents *this exact bug* ("plain strings generated an untyped `string` in the SDKs and risked a `.getTime()` crash") being fixed there — but the migration to `time.Time` was never applied to `messages.go`/`webhooks.go`. *Fix:* standardize on typed `time.Time` everywhere (let Huma emit `date-time`), or at minimum document the split; pick one so the SDK timestamp type is uniform.

**🔵 No explicit rate limit on `POST /verify`.** Each call runs a live DNS probe (`VerifyProbe`, `domains.go:207`). Bounded to owned domains and DNS is cached, so low risk, but a hot loop issues unbounded resolver queries — worth a light per-user limit like the send path has.

**🔵 `is_primary` PATCH is promotion-only.** `handleUpdateDomain` rejects `is_primary:false` with a 400 ("promote the other domain instead", `domains.go:336–338`). Documented, but unusual REST semantics — a client setting `false` gets an error rather than a no-op.

**✅ Verified clean:**
- **Claim conflict**: `ClaimDomain` → `ErrDomainTaken` → 409 `domain_taken`, declared in the operation's `Responses` (`domains.go:157–160`) so it's in the spec.
- **Delete safety**: `?confirm=DELETE` + `HasAgentsOnDomain` guard, both **after** ownership (`domains.go:367–380`) — a not-owned domain 404s before any confirmation/agent oracle.
- **Verify UX**: 412-with-diagnostic when the TXT isn't published (documented response, `domains.go:180–183`); already-verified re-verify is idempotent and doubles as a forced sending-identity re-check (`domains.go:212–213`).
- **Probe scoping**: `VerifyProbe` only runs after `LookupDomain` confirms ownership, so it can't be pointed at an arbitrary DNS name.

### 7. API — `webhooks.go` (config / delivery / rotate / test)

Strong security hygiene (SSRF, agent-ownership, once-shown secret), but the event-type enum is hand-duplicated and has **already drifted** — making the screening framework's injection alert unsubscribable. This is the most concrete defect found so far.

**🟡 (effectively 🔴 for the screening feature) — `email.injection_detected` cannot be subscribed to.** The webhook event enum is hardcoded as an OpenAPI struct-tag in **5 separate places** (`webhooks.go:41,185,252,309,372`) and is **out of sync** with the canonical `webhookpub.AllEventTypes`. Verified: `email.injection_detected` is a defined, emitted event and *is* in `AllEventTypes` (`webhookpub/event.go:58,` in the slice), so runtime `IsValidEventType` accepts it — but it is **absent from every struct-tag enum** (`grep` count = 0). Huma validates the request body against the struct-tag enum, so `POST /v1/webhooks {events:["email.injection_detected"]}` is rejected with **422 before the handler runs**. Net effect: the screening engine fires injection-detection alerts that **no agent can subscribe to via the typed API**, defeating the alert's purpose. *Fix:* generate the enum from `webhookpub.AllEventTypes` (Huma supports a registry/`huma.Schema` enum from a slice) instead of 5 hand-copied tags. The comment at `webhooks.go:181–182` ("keep in sync with `webhookpub.AllEventTypes`") names exactly the drift that has now occurred.

**🔵 Label charset rule duplicated a third time.** `filters.labels` validation (`webhooks.go:136–145`) inlines the `[a-z0-9:_-]` rule again — now a *third* copy (after `messages.go:normalizeLabel` and `agent.NormalizeAndValidateLabelList`). Reinforces the #2 label-drift theme; all three should call one shared validator.

**🔵 `Page[T]` envelope that never paginates.** `listWebhooks` and `listWebhookDeliveries` always return `NewPage(items, "")` (`webhooks.go:365,527`) — the cursor is permanently null (documented WH-7). The shape *looks* paginated; a one-line "single-page" note on these ops avoids a client building cursor-loop logic that never advances.

**✅ Verified clean:**
- **SSRF**: `agent.ValidateWebhookURL` (`webhooks.go:86`) — the canonical check, reused not reimplemented.
- **Filter ownership**: `assertAgentsOwned` (`webhooks.go:152`) — `filters.agent_ids` must reference agents the caller owns (can't subscribe to another tenant's agent's events).
- **Secret hygiene**: `WebhookView` carries no secret; it's shown once on create + rotate; rotate is `runIdempotent`-wrapped so a retried rotate replays the same secret (route-hashed, no body) rather than minting+dropping a second.
- **Merge-then-validate on PATCH** (`webhooks.go:396–419`): the effective post-patch state is validated against create-time rules; cleared events/url rejected; auto-disable cooldown → 409.

### 6. API — `events.go` (webhook delivery log + redeliver)

The redeliver design is genuinely thoughtful (server-minted idempotency, matched-subscriber guard). But two cursor-contract inconsistencies break the pattern the other list endpoints set, and the *screening* audit log turns out to have no surface here at all.

**🟡 Events list cursor does NOT bind the filter identity.** `eventsCursor` is just `{C, I}` — the last row's created_at + id (`events.go:26–29`) — and `handleListEvents` decodes it without checking it against the current filters (`events.go:201–211`). So a client can paginate with a cursor minted under `type=email.received`, then flip to `type=email.bounced`, and the keyset position is silently applied to the new filter → **result-set drift**. This is exactly the bug `messages.go`/`conversations.go` prevent by binding the full filter set and rejecting mismatches with `invalid_cursor`. Events is the lone list endpoint missing it. *Fix:* add the filter identity (type/agent_id/conversation_id/message_id/since/until) to `eventsCursor` and reject changed-filter continuations, mirroring #2/#4.

**🟡 `hasMore` via `len(events)==limit` instead of `limit+1`.** `events.go:216` emits a `next_cursor` whenever the page is exactly full — so a query returning exactly `limit` rows hands back a cursor that fetches an **empty** next page. The other list endpoints fetch `limit+1` and only emit a cursor when a further row actually exists (no spurious empty page). Contract inconsistency + one wasted round-trip per exactly-full page. *Fix:* adopt the `limit+1` detection, or document that the events cursor may yield a final empty page.

**🟡 The `screening_events` audit log has no `/v1` surface.** This file is the **webhook delivery** log (`agent.EventJSON`); the screening framework's `screening_events` table (gate/scan violations — the injection-detection audit) is **not exposed by any `/v1` endpoint**. The screening review's "feedback loop" goal (measure false-positive rate by joining `screening_events` to human dispositions) is unreachable via the API — it requires direct DB/dashboard access. *Action:* decide whether `GET /v1/screening-events` (or a filter on this endpoint) is in scope; at minimum note that the security audit log is API-invisible in v1.

**🔵 Three different retention windows, undocumented together.** Events expire at **30 days** (`events.go:139,245` → 410 Gone), messages at **10 days** (TTL), and `screening_events` are kept indefinitely (no cascade). Operators reconciling these will be surprised; a one-line retention table in the docs would help.

**✅ Verified clean:**
- **Redeliver auto-idempotency** (`events.go:121–132`): a **server-minted** key `replay:event:webhook`, namespaced apart from caller `Idempotency-Key` headers so a crafted header can't 422-poison a later genuine redelivery. Well-reasoned.
- **Matched-subscriber guard** (`events.go:144–147`): a targeted replay 409s if the webhook wasn't among the originally-matched subscribers — can't replay to an arbitrary endpoint.
- **Account scope** on all three handlers (`requireAccountUser`) — correct, since the delivery log spans all the account's agents.
- **Partial-failure transparency**: bulk fan-out marks each subscriber `pending`/`skipped`+reason rather than failing the whole call.

### 5. API — `hitl.go` (approve / reject review queue)

The critical self-approval ceiling is correctly enforced. The headline finding is a coverage gap: this surface only handles **outbound** holds — the screening framework's **inbound** review queue has no manual-release endpoint here.

**🟡 No `/v1` approve/reject for the inbound review queue.** Both handlers operate on outbound `pending_approval` drafts: `handleApprove` → `deps.ApprovePending` (`hitl.go:86`), `handleReject` → `deps.RejectPending` (`hitl.go:117`), and the descriptions say "Approve a **pending_approval** draft." But the screening work added an *inbound* review queue (`pending_review`, released via `ApproveInboundReview`/`RejectInboundReview` in `identity/review.go`). There is **no `/v1` endpoint to manually approve/reject a held inbound message** — so a quarantined inbound message can only be resolved by the `hitlworker` TTL expiry (`hitl_expiration_action`), never by a human/programmatic decision through the public API. For a feature literally named *human-in-the-loop review*, "hold then auto-decide on a timer" is a thin version. *Action:* confirm whether inbound release is intentionally dashboard-only (legacy `/api`) for v1, and if so document it; otherwise add `POST /v1/agents/{email}/messages/{id}/review:{approve,reject}` (or a `direction`-aware variant of these handlers) so the inbound queue is releasable via the same surface.

**🔵 Reject has no idempotency / `Idempotency-Key`.** `handleApprove` wraps the SES send in `runIdempotent` (`hitl.go:85`) and accepts the header; `handleReject` does neither (`rejectInput`, `hitl.go:38–42`). Defensible — reject is a naturally-idempotent state discard (double-reject is a harmless no-op) — but the asymmetry is undocumented. A one-line note on the reject op ("idempotent; no key needed") would close it.

**🔵 Approve idempotency route is msgID-based** (`"/v1/approve/"+in.ID`, `hitl.go:85`) — same pattern (and same latent fragility) flagged for reply/forward in #3. Safe because a held message belongs to one agent, but inconsistent with `send`'s agent-id-folded route. Folds into the #3 "unify the idempotency route" fix.

**✅ Verified clean:**
- **Self-approval ceiling** (`hitl.go:70`, `105`): both approve and reject require **account scope** — an agent-scoped credential gets 403, so an agent can't approve its own held outbound and defeat the gate. This is the load-bearing HITL security property; the comment documents it and the inbound adversarial review proved it.
- **Expected-agent-email guard**: `ag.Email` is passed to `ApprovePending`/`RejectPending` (`hitl.go:86,117`) so the held message must belong to the path agent — ownership double-check beyond `resolveOwnedAgent`.
- **Send-limit on approve only** (`hitl.go:79`): approve triggers a send (rate-limited); reject doesn't (correctly skipped).
- **Unified result shape**: approve returns `SendResultView` with `edited` set (MSG-9), so approve/send/reply/forward share one response type.

### 4. API — `conversations.go` (threading list + detail)

Tight handler — typed timestamps, complete cursor binding, ownership-scoped. One real concern is a cross-surface leak risk in the *summary aggregates* that the prior inbound review may not have covered.

**🟡 Conversation summary aggregates may leak held-message metadata (cross-ref to verify).** `ConversationSummaryView` carries `message_count`/`inbound_count`/`has_unread`/`latest_subject`/`latest_sender` (`conversations.go:16–26`), computed by `deps.GetConversation`/`ListConversations` in the store. The inbound review proved the *message list* (`detail.Messages`) excludes held inbound rows — but the **summary aggregates are a separate computation**. If the store counts or "latest"-picks held (`pending_review`/quarantined) inbound rows, then `latest_subject`/`latest_sender` can surface a **quarantined attacker message's subject/sender**, and the counts/`has_unread` misreport — even though the message list correctly hides it. *Fix:* confirm the store's conversation aggregation applies the same `heldInboundStatuses` exclusion to the count/latest/has_unread rollups, not just the member-message query. This is exactly the read-boundary class the screening review flagged, on a surface it didn't explicitly test.

**🔵 No participant/subject filter on list.** `ListConversationsInput` (`conversations.go:57–63`) filters only by `since`/`until` — no `participant`/`subject_contains` that `messages.go` offers. Ergonomic gap, not a bug; fine for v1.

**✅ Verified clean:**
- **Cursor binding is complete** (`conversations.go:67–73`, `138–141`): the cursor captures agent + since + until, which is the *entire* filter set here, so no silent window drift (stronger position than `messages.go` only because there are fewer filters).
- **Typed timestamps** (`time.Time` + `format:"date-time"`, `conversations.go:18–19`) — the comment documents a real prior bug (plain-string timestamps generated an untyped `string` in the SDKs); now consistent with the rest of the surface.
- **Path validation**: `conversation_id` length + CR/LF checked (`conversations.go:191–195`); `since < until` enforced; `limit+1` has-more.
- **Embedded summary in detail** (`conversations.go:45–50`) flattens cleanly to the documented top-level layout.
- **Held message-list exclusion** (cross-ref inbound review): `detail.Messages` relies on `GetConversation` being held-filtered — proven REFUTED-safe for the message list.

### 3. API — `outbound.go` (send / reply / forward + idempotency)

Clean sender-is-the-path-agent model (no `from` spoofing) and a nicely registered 202 schema. Three real gaps, all in the reply/forward paths diverging from the hardened send path.

**🟡 `reply_all` bypasses the `maxRecipients` blast cap.** `handleReply` checks `recipientCountError(b.CC, b.BCC)` — only the *user-supplied* CC/BCC (`outbound.go:238`) — then expands the actual recipients via `ParseReplyRecipients(..., b.ReplyAll, ...)` into `rr.To`/`rr.CC` (`outbound.go:255–262`), which are **never counted**. A `reply_all` to a 200-recipient thread sends to all 200, sailing past the 50-cap whose stated purpose is "keep a single send from becoming a blast" (`outbound.go:59–63`). *Fix:* run `recipientCountError(rr.To, rr.CC, b.BCC)` on the *effective* recipients after expansion, not just the user-supplied ones. (Forward is fine — its recipients are all user-supplied and counted, `outbound.go:302`.)

**🟡 CRLF-in-subject check is send-only; reply/forward skip it.** `validateOutboundBody` rejects CR/LF in the subject (`outbound.go:332`), but reply/forward *derive* the subject from the stored inbound (`"Re: "+inbound.Subject`, `outbound.go:249–254`; `BuildForwardSubject`, `outbound.go:311`) without that check. If a stored inbound subject can carry CR/LF (i.e. wasn't sanitized at ingest), the derived outbound subject is a header-injection vector. *Fix:* verify the outbound composer strips CR/LF from the subject unconditionally (defense-in-depth), or apply the same check to the derived subject.

**🟡 Idempotency-route pattern is inconsistent (works, but fragile).** `send` deliberately folds the agent id into the route to avoid same-user cross-agent collisions (`outbound.go:426–430`), but `reply`/`forward` use `"/v1/reply/"+id` / `"/v1/forward/"+id` (`outbound.go:271,324`) — safe only because an inbound `id` belongs to exactly one agent (`loadInbound` pins `in.AgentID == ag.ID`). And `handleTestSend` has **no** idempotency wiring or `Idempotency-Key` header at all (`outbound.go:151`). It holds today, but the differing patterns are a latent footgun. *Fix:* fold `ag.ID` into all three routes uniformly so the invariant doesn't depend on `id`-uniqueness reasoning.

**✅ Verified clean:**
- **Sender identity**: `from` is the path agent (`outbound.go:420–423`), auth-scoped — no body-level spoofing.
- **Send/forward recipient cap + validation**: `recipientCountError` + `ValidateRecipients` + self-alias stripping (`StripAgentSelfAliases`) on CC/BCC.
- **Pre-send gating order**: `checkSendLimit` (429 + retry-after) → `domain_verified` (403) → `EnforceMessageSend` quota (402) → deliver — consistent across `deliver` and `handleTestSend`.
- **202 hold**: schema registered via the component registry (`jsonResponse`, `outbound.go:22`) so the OpenAPI 202 stays in lockstep with `SendResultView`. Idempotency handshake wraps the actual `DeliverOutbound` call (`outbound.go:370`).

### 2. API — `messages.go` (detail/list views, raw/parsed, labels PATCH)

The four-status-axis model (`read_status`/`hitl_status`/`delivery_status`/`webhook_status`) is clean and well-documented, and the cursor handling is genuinely strong. Findings are mostly drift/consistency risks.

**🟡 Label-rule duplication → drift risk.** The read-side filter validates labels with a *local* reimplementation, `normalizeLabel` (`messages.go:573`), while the write-side PATCH uses `agent.NormalizeAndValidateLabelList` (`messages.go:382`). Same charset/length/`e2a:`-prefix rule, **two separate codebases**. The comment (`messages.go:554–556`) acknowledges the intent ("can't drift") but the implementations genuinely can — a charset change on one side silently diverges read-filtering from write-validation (and the GIN-index guard). *Fix:* have `normalizeLabelFilter` call the same shared validator (with an `allowSystem` flag) instead of a parallel copy.

**🔵 `hitl_status` enum models only the outbound lifecycle.** `MessageView.HITLStatus` (`messages.go:43`) enumerates `pending_approval,sent,rejected,expired_*` and is set **outbound-only** (`messages.go:137–143`). The screening work added an *inbound* review lifecycle (`pending_review`/`review_rejected`/`review_*`). While held, those rows are correctly filtered out of all reads, so they never need a field — but a **released** inbound message carries no review-status indicator anywhere in the view. Consistency gap worth a deliberate decision (add an inbound review-status field, or document that release erases the review trace from the message view).

**🔵 Substring filters are sequential-scan-shaped.** `from` and `subject_contains` (`messages.go:265–266`) are case-insensitive substring matches — bounded to 200 chars (good for safety) but inherently un-indexable (`ILIKE '%x%'`). A perf/scale note, not a correctness bug; fine at current volumes, worth a trigram index if these get hot.

**🔵 `raw_message` always-present-but-nullable.** `MessageView.RawMessage []byte` has no `omitempty` (`messages.go:77`), so held outbound drafts (which use `body` instead) render `"raw_message": null`. Intentional "always present" shape, but the doc comment "raw_message is always present" reads as non-null; clarify it can be null for held drafts.

**✅ Verified clean:**
- **Cursor filter-identity binding** (`messages.go:282–295`, `485–492`): the cursor captures the *full* filter set (agent, status, direction, sort, from, subject, conversation, since/until, labels) and rejects reuse under changed filters → no silent result-set drift. This is the right, thorough design.
- **Half-open time window** (`since` inclusive, `until` exclusive; `since < until` enforced), **limit+1 has-more** detection, **outbound `status` filter rejection** (clear 400), all correct.
- **Scope**: get/list/label-PATCH go through `resolveOwnedAgent` (per-agent, so an agent-scoped credential reads/labels *its own* mail) — correct, and distinct from the account-scope ceiling on config writes.
- **Held-content read boundary** (cross-ref #1/inbound review): `getMessage` exposes `raw_message`/`parsed` unconditionally in the view, but relies on `deps.GetMessage` being held-status-filtered — the inbound adversarial review proved the detail path REFUTED-safe. Keep them linked: any new `GetMessage` wiring must preserve that filter.

### 1. API — `agents_write.go` (agent create / PATCH / delete + config)

Create/update/delete handlers + the additive-PATCH config merge. Auth model and the merge-then-validate pattern are sound; the findings are mostly contract-accuracy drift.

**🟡 Stale OpenAPI description on `updateAgent`.** `agents_write.go:89` — `Description: "Patch an agent's HITL settings."` But the PATCH body now also accepts `inbound_policy`/`inbound_allowlist`, `outbound_policy`/`outbound_allowlist`, the gate actions, and the full inbound/outbound scan config (`agents_write.go:117–132`). The description is the source for the generated `/v1` spec and SDK docstrings, so every SDK consumer sees a wrong summary. *Fix:* "Patch an agent's HITL, inbound/outbound policy, and content-screening settings."

**🟡 Cross-ref to check (privilege escalation surface).** `handleCreateAgent` gates on `requireAccountUser` (`agents_write.go:295`) while update/delete use `requireAccountScope` (which the comments say bars agent-scoped credentials per the "Slice 5a hard ceiling"). Need to confirm `requireAccountUser` *also* bars an agent-scoped token — otherwise an agent-scoped credential could mint **new** agents, an escalation the update/delete ceiling explicitly prevents. → verify in the `scope.go` review (#10).

**🔵 Fragile duplicate detection.** `agents_write.go:358` — `if strings.Contains(err.Error(), "duplicate")` to map to 409. String-matching the store's error text; if the wording changes, a duplicate silently becomes a 500. *Fix:* a typed sentinel (`identity.ErrAgentExists`) + `errors.Is`.

**🔵 Stale struct/field comments.** `agents_write.go:106–108` ("only HITL settings remain mutable") contradicts the policy/scan fields right below it. Minor doc drift; refresh alongside the description fix.

**🔵 Over-built error type.** `slugError`/`errSlug` (`agents_write.go:66–70`) only carry a string message and are never type-asserted — a plain `errors.New` would do.

**✅ Verified clean:**
- **Additive-PATCH scan config** (`agents_write.go:197–245`): merges provided fields over current config, then validates the *effective* posture in the store — so a partial threshold update can't bypass the `review < block` ladder. Correct pattern.
- **Auth ceiling**: update + delete require account scope (agent-scoped creds can't change their own security posture); delete also requires `?confirm=DELETE` after ownership resolution (no confirmation oracle for non-owned agents).
- **Create authorization**: custom-domain agents gated on owned-AND-verified domain; shared-domain local-part validated as a slug (reserved-name blocklist). 402 limit envelope is structured and ordered after auth/domain checks.
- **Empty PATCH** → 400 `invalid_request` (no silent no-op).
