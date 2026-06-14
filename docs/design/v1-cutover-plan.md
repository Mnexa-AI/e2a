# Slice 1 — `/v1` cutover plan (finish Slice 1)

| | |
|---|---|
| **Status** | Planned |
| **Depends on** | PR #208 (the additive `/v1` surface) |
| **Branch** | `feat/api-v1-cutover` (stacked on `feat/api-v1-slice1-contract`) |
| **Breaking** | YES — removes legacy `/api/v1`, breaks the internal AgentDrive consumer |

PR #208 added the `/v1` surface **alongside** legacy `/api/v1` (strangler). This
plan finishes Slice 1 by flipping `/v1` to the live path and deleting legacy.
Scoping the removal surfaced that #208's additive surface is **~90% complete** —
three legacy bearer capabilities have no `/v1` equivalent yet and must be ported
**before** anything is removed.

## Phase A — close the `/v1` functional gaps (must land before any removal)

### A1. HITL approve / reject (the held-draft decision) — **highest priority**
Without this, a `/v1` HITL agent cannot release a `pending_approval` draft.
- Extract `ApprovePendingCore(ctx, userID, messageID, expectedAgentID, edits) (*identity.Message, *OutboundError)` from `handleApprovePendingMessage` (hitl_api.go): load preview → 404; `expectedAgentID` mismatch → 404; not-pending → 409; load agent + domain-verify → 403; `store.ApproveAndSend(...)` with the existing send callback (`buildSendRequestFromMessage` + `attachReferencesChain` + self-send loopback / `sender.Send`); usage + `publishApproved`. Map `ValidationError`→400.
- Extract `RejectPendingCore(ctx, userID, messageID, expectedAgentID, reason) (*identity.Message, *OutboundError)` from `handleRejectPendingMessage`.
- Rewire both legacy handlers through the cores (idempotency guard stays on the legacy approve).
- Add `POST /v1/agents/{address}/messages/{id}/approve` and `.../reject` (Slice 1 keeps the **separate** verbs; the `approval` sub-resource collapse is Slice 2). Approve runs under `runIdempotent` (it triggers an SES send). Body = the reviewer-override `approveRequest` shape.
- **Verify:** `go test ./internal/agent/` (hitl_api_test, hitl_approval_api_test) green + new httpapi tests (approve→sent, not-pending→409, non-owned→404, reject).

### A2. WebSocket transport — `GET /v1/agents/{address}/ws`
Legacy `api.RegisterWSRoute` registers `/api/v1/agents/{email}/ws` on the mux.
chi can host the same `ws.Handler.Handle` at `/v1/...`. Register it on the chi
root in `httpapi.New` (or keep `RegisterWSRoute` and also mount `/v1/.../ws`).
Token auth via `?token=` is unchanged. **Verify:** the existing ws e2e + a
connect check over `/v1`.

### A3. Magic-link approval (browser HTML, prefetch-safe)
Lower priority (operator/browser flow, `noMcp`). Either keep the legacy
`/approve`/`/reject` HTML handlers on the retained console surface, or port to
`GET /v1/approvals/{token}` (HTML, **no side effect**) + `POST /v1/approvals/{token}`
per design decision 5. Recommended: defer the *redesigned* `/approvals/{token}`
shape to Slice 2; for Slice 1 keep the legacy magic-link handlers on the
retained (non-`/api/v1`) surface so nothing breaks.

## Phase B — parity hardening on `/v1`
- **Rate limiting**: apply `pollLimit` (per-user reads), `sendLimit` (per-agent
  outbound), `regLimit` (per-IP agent create) — inject the `*ratelimit.Limiter`s;
  on block return `429` with code `rate_limited` + `Retry-After` and the IETF
  `RateLimit-Limit/Remaining/Reset` headers (header conventions §). These are
  only live once `/v1` is the path, so they land here, just before the flip.

## Phase C — consumer cutover (cross-repo, coordinated)
- **AgentDrive** (`/Users/joshzhang/Desktop/agentdrive`) calls `/api/v1/...`. Move
  its e2a client to `/v1/...` and the new shapes (cursor pagination
  `{items,next_cursor}`, send/reply/forward responses). This is a **separate repo
  + separate PR**, merged in lockstep with Phase D. **Needs explicit go-ahead.**
- Self-host docs / CLI / SDK consumers: update base path.

## Phase D — destructive removal (last)
- Delete the legacy `/api/v1` **bearer** route registrations from
  `agent.RegisterRoutes` and their handlers. **Keep**: `/api/oauth/*`,
  `/api/auth/*`, `/api/dashboard/*`, `/api/keys*`, `/.well-known/*`,
  `/api/health`, `/api/feedback`, `/api/internal/*`, and `/api/v1/users/me/
  signing-secrets*` (console/session per §5).
- **Tests**: the ~22 agent `*_test.go` files that POST to `/api/v1/...` (≈12k
  lines total) test the *legacy HTTP layer*, now redundant with the httpapi
  tests + the preserved store/core tests. Delete the HTTP-handler tests as each
  handler is removed; **keep** store/core tests (internal/identity, the
  extracted-core tests). Do this resource-by-resource, building green after each.
- Drop the strangler `Legacy` fallback for bearer routes; chi serves `/v1` +
  the retained console/oauth surface.

## Phase E — spec / SDK / CI / host
- **Spec source of truth** flips to the Huma-generated `/v1/openapi.yaml`;
  retire the legacy `swag` annotations for the bearer surface.
- **SDK regen** (TS + Python) from the new spec; commit the regenerated output.
- **Contract-drift CI gate** (§6): emit spec fresh in CI, SDK regen-diff, MCP
  request-validation + coverage + tool↔operationId map.
- **Host/config cutover** (§9a): `E2A_PUBLIC_URL`/`MCP_ALLOWED_HOSTS`/
  `MCP_PUBLIC_URL` → `api.e2a.dev`; Caddy path-routes `/v1`, `/mcp`.

## Sequencing
A (gaps) → B (rate-limit) → C+D in lockstep (AgentDrive + removal) → E (spec/SDK/
CI/host). A and B are non-breaking and can land first. C+D are the breaking flip.
