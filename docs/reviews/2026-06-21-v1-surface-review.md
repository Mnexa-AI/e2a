# v1 API / SDK / MCP surface review

**Purpose:** a rolling, focused review of the newly designed `/v1` API, the TS/Python SDKs, and the MCP interfaces — one subcomponent per iteration. Each entry records concrete findings (correctness, contract cleanliness, consistency, security, ergonomics) with `file:line` and a suggested fix, plus what was verified safe.

**Branch:** `review/v1-surface-audit` (off `main`). **Cadence:** ~20-min loop.

**How the loop runs:** each iteration picks the **next `pending` row** in the ledger, reviews *only* that subcomponent (keep it tight), appends a findings section below, flips the ledger row to `done` with a one-line headline, and commits. Severity scale: 🔴 high · 🟡 medium · 🔵 low · ✅ verified-clean.

---

## Review ledger

| # | Area | Subcomponent | Status | Headline |
|---|------|--------------|--------|----------|
| 1 | API | `agents_write.go` — agent create/PATCH + config | done | 🟡 updateAgent OpenAPI desc stale (HITL-only) vs full policy/scan PATCH; verify create's account-scope ceiling |
| 2 | API | `messages.go` — message detail/list views + raw/parsed | pending | |
| 3 | API | `outbound.go` — send/reply/forward + idempotency wiring | pending | |
| 4 | API | `conversations.go` — threading/list | pending | |
| 5 | API | `hitl.go` — approve/reject review queue | pending | |
| 6 | API | `events.go` — events API + screening_events surface | pending | |
| 7 | API | `webhooks.go` — webhook config/delivery | pending | |
| 8 | API | `domains.go` — domain verification | pending | |
| 9 | API | `account.go` — account/limits/usage | pending | |
| 10 | API | `scope.go` + `middleware.go` — auth/scopes | pending | |
| 11 | API | `pagination.go` — cursor contracts | pending | |
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
