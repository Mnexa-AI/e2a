# v1 API / SDK / MCP surface review

**Purpose:** a rolling, focused review of the newly designed `/v1` API, the TS/Python SDKs, and the MCP interfaces — one subcomponent per iteration. Each entry records concrete findings (correctness, contract cleanliness, consistency, security, ergonomics) with `file:line` and a suggested fix, plus what was verified safe.

**Branch:** `review/v1-surface-audit` (off `main`). **Cadence:** ~20-min loop.

**How the loop runs:** each iteration picks the **next `pending` row** in the ledger, reviews *only* that subcomponent (keep it tight), appends a findings section below, flips the ledger row to `done` with a one-line headline, and commits. Severity scale: 🔴 high · 🟡 medium · 🔵 low · ✅ verified-clean.

---

## Review ledger

| # | Area | Subcomponent | Status | Headline |
|---|------|--------------|--------|----------|
| 1 | API | `agents_write.go` — agent create/PATCH + config | pending | |
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
