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
| 12 | API | `idempotency.go` — idempotency keys | pending | |
| 13 | API | `operations.go` + `errors.go` — views + error envelopes | pending | |
| 14 | API | `ratelimit.go` — rate limiting | pending | |
| 15 | SDK | TS `client.ts` — ergonomic layer (parse/reply) | pending | |
| 16 | SDK | TS `ws.ts` — WebSocket | pending | |
| 17 | SDK | TS `pagination.ts` + `retry.ts` + `errors.ts` | pending | |
| 18 | SDK | Python `client.py` | pending | |
| 19 | SDK | Python `websocket.py` + `pagination.py` + `_retry.py` | pending | |
| 20 | SDK | `webhook-signature` TS↔Python parity | pending | |
| 21 | MCP | `tools/agents.ts` | pending | |
| 22 | MCP | `tools/messages.ts` + `attachments.ts` | pending | |
| 23 | MCP | `tools/hitl.ts` | pending | |
| 24 | MCP | `tools/webhooks.ts` + `events.ts` + `domains.ts` | pending | |
| 25 | MCP | `server.ts` + `session.ts` + `client.ts` — transport/auth/pagination | pending | |
| 26 | MCP | `tools/tiers.ts` + `util.ts` — scope gating | pending | |

---

## Findings

<!-- Each iteration appends a "### N. <area> — <subcomponent>" section here. -->

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
