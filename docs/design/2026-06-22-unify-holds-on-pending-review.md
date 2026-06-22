# Unify message holds on the `pending_review` vocabulary (drop `pending_approval`)

**Status:** Proposed · **Date:** 2026-06-22 · **Author:** design pass (Claude)

## 1. Goal

Collapse the two parallel human-in-the-loop hold vocabularies — outbound
`pending_approval` / `approval_*` and inbound `pending_review` / `review_*` —
into **one direction-aware `review` primitive**. One status family, one webhook
event family, one `/approve` + `/reject` surface that branches on direction.

This finishes the trajectory migration `040_screening.sql` explicitly named
("evolve the existing outbound `pending_approval` machine into a direction-aware
queue") and closes the gap that the inbound review approve/reject store methods
(`ApproveInboundReview` / `RejectInboundReview`) have **no wired endpoint** today —
inbound holds currently resolve only by TTL auto-sweep.

**Why now:** the `pending_approval` status and the `approval_*` events freeze at
GA. This is the only cheap window; afterward it is a breaking change with external
subscribers.

## 2. The unifying model

A held message — regardless of direction — is one primitive:

> A message is **held** (`pending_review`). A human (or the TTL sweep) **approves**
> it → it proceeds, or **rejects** it → it is dropped. "Proceeds" is
> direction-dependent: outbound = **send via SES**; inbound = **release to the agent**.

The direction-specific *side effect* of approve is preserved in full (see §6) —
this is a vocabulary + event + dispatch unification, NOT a removal of the outbound
send-approval machinery.

## 3. Status mapping (DB)

Current (`migrations/040`): outbound `sent | pending_approval | rejected |
expired_approved | expired_rejected`; inbound `pending_review | review_approved |
review_rejected | review_expired_approved | review_expired_rejected`.

Target: outbound adopts the review vocabulary.

| Outbound today | → Unified |
|---|---|
| `pending_approval` | `pending_review` |
| `rejected` | `review_rejected` |
| `expired_approved` | `review_expired_approved` |
| `expired_rejected` | `review_expired_rejected` |
| `sent` | `sent` (unchanged — outbound's "approved" terminal is the send itself) |

Note the asymmetry that stays: **outbound approve → `sent`** (the approve triggers
the SES send, there is no "approved-but-unsent" state), **inbound approve →
`review_approved`** (released to inbox). The unified webhook (`email.review_approved`)
fires for both; outbound additionally fires `email.sent`.

### Migration `044_unify_holds.sql` (idempotent, non-destructive)

`messages` is prod-sized — follow CLAUDE.md (no `ALTER COLUMN TYPE`; bounded backfill).

1. **Backfill** (bounded — holds are a tiny, ≤10-day-TTL fraction of rows):
   `UPDATE messages SET status = <map> WHERE direction='outbound' AND status IN
   ('pending_approval','rejected','expired_approved','expired_rejected')`.
2. **Swap CHECK** to the unified set. Add the new constraint `NOT VALID` then
   `VALIDATE CONSTRAINT` separately to avoid a long table-scan lock (migration 040
   did a plain swap; we harden it).
3. **Index:** the existing `idx_messages_pending_review` (status='pending_review')
   now covers both directions. Drop `idx_messages_pending_approval`.

## 4. Webhook event mapping

| Today | → Unified | Notes |
|---|---|---|
| `email.pending_approval` (outbound) | **`email.pending_review`** | already exists (inbound); now fires both directions via `direction` field |
| `email.approval_accepted` | **`email.review_approved`** | direction-aware |
| `email.approval_rejected` | **`email.review_rejected`** | direction-aware |
| `email.pending_review` (inbound) | unchanged | |

**Removed:** `email.pending_approval`, `email.approval_accepted`,
`email.approval_rejected`. **Added:** `email.review_approved`,
`email.review_rejected`. Net catalog **14 → 13**.

The hold-event family becomes a clean trio — `pending_review`, `review_approved`,
`review_rejected` — all carrying `direction`, `reason_source`, and (for
`pending_review`) `approval_expires_at`.

### Decision (Q2): keep the resolution pair

Recommend **keeping both** `review_approved` and `review_rejected` rather than
relying on `email.sent`/`email.received`:
- **inbound-approved has no other signal** — `email.received` is suppressed while
  held and is not re-fired on release, so without `review_approved` an approved
  inbound message is invisible.
- For outbound, `review_approved` (human approved) and `email.sent` (SES accepted)
  are *different facts* — approve can succeed while the send later fails — so both
  are useful, not redundant.

## 5. Endpoint dispatch

`POST /v1/agents/{email}/messages/{id}/approve` and `/reject` branch on the held
message's **direction**:

- **outbound** (`pending_review`, was `pending_approval`) → `ApprovePendingCore`
  (SES send, idempotency, magic-link, self-approval guard) / `RejectPendingCore`.
- **inbound** (`pending_review` from screening) → wire the today-dead
  `ApproveInboundReview` (release to inbox) / `RejectInboundReview` (drop).

This single change closes the "inbound holds have no human resolution surface" gap.
Handler descriptions/OpenAPI updated to say "approve a held message (outbound: send;
inbound: release)".

## 6. Safety machinery to preserve (outbound approve)

The outbound approve path keeps **all** of: idempotent SES send (approve triggers a
real send), magic-link approval (`hitl_magic_api.go`), self-approval guard
(an agent can't approve its own outbound), owner notification (`hitlnotify`),
send-limit check. The dispatch must route outbound approve through exactly this path —
the unification renames the wrapper, not the guarantees.

## 7. Surfaces to update

- **Go:** `internal/identity` (status constants, review.go + store.go approve/reject),
  `internal/agent/hitl_api.go` + `hitl_magic_api.go`, `internal/httpapi/hitl.go`
  (dispatch) + `messages.go`/`outbound.go` (status views), `internal/webhookpub`
  (event consts + `AllEventTypes`), emit sites in `agent/api.go` (outbound) and
  inbound flow, `internal/hitlworker` (the TTL sweep now resolves both directions
  under `pending_review` — dispatch by direction), `hitlnotify`.
- **DB:** migration 044.
- **Spec/SDK:** regenerate `api/openapi.yaml` + TS/Python bases.
- **MCP:** `mcp/src/tools/messages.ts`, `tiers.ts` (status strings + tool docs).
- **Web:** dashboard status labels / approval UI.

## 8. The TTL sweep (hitlworker)

Today: a separate outbound `pending_approval` sweep + inbound `sweepReviews`. After
unification both holds carry `pending_review`; the sweep selects `pending_review`
rows and dispatches by **direction** — outbound expired → send-or-drop per policy
(`review_expired_approved`/`review_expired_rejected`), inbound expired → release-or-drop.
Behavior is preserved; only the selection/dispatch is unified.

## 9. Backward-compat & rollout

- **In-flight prod holds:** the backfill (§3.1) migrates any live `pending_approval`
  rows to `pending_review`, so nothing is stranded. Magic-link emails already sent
  reference an approve URL that still resolves (same message id, dispatch handles it).
- **Subscribers:** the `approval_*` events are removed — breaking, but pre-GA and the
  event surface was never frozen/tagged stable in a release. Acceptable in this window.
- **Stability tag:** mark the whole `review.*` family **beta** during the unification
  (consistent with `pending_review`); promote to stable at GA once settled.

## 10. Testing

- Migration: backfill maps every outbound hold/terminal correctly; CHECK accepts the
  unified set and rejects the old values; idempotent re-run is a no-op.
- Dispatch: outbound approve still sends (idempotency + magic-link + self-approval
  guard intact); inbound approve releases to inbox; reject drops in both directions.
- Events: `pending_review`/`review_approved`/`review_rejected` fire with correct
  `direction` + `reason_source`; old `approval_*`/`pending_approval` gone everywhere;
  drift gates green; SDK parity.
- TTL sweep resolves both directions under `pending_review`.
- E2E: the existing outbound HITL e2e suite, repointed to the review vocabulary, must
  pass unchanged in behavior.

## 11. Slices

1. **DB + status constants** — migration 044 + `internal/identity` status vocabulary, backfill, sweep dispatch.
2. **Events** — webhookpub consts, emit sites, enum/`AllEventTypes`, spec+SDK regen.
3. **Endpoint dispatch** — `/approve`+`/reject` branch on direction; wire inbound review; preserve outbound machinery.
4. **Periphery** — MCP, web, magic-link copy, notifier.
5. **Tests + e2e** across all of the above.

## 12. Open questions / risks

- **Outbound `sent` vs `review_approved` asymmetry** (§3): acceptable, but confirm we
  don't want an explicit outbound `review_approved` status before the send (the send
  is the approval's effect, so no).
- **Magic-link copy** references "approve"/"approval"; keep "approve" as the verb
  (the action is still approve), only the status/event nouns move to "review".
- **Safety-critical path:** outbound approve gates real sends — this slice needs the
  full outbound HITL test suite green before merge; highest-risk part of the change.
</content>
