# Review Hold Reason Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace scattered review-reason fields with one product-oriented `hold_reason` contract and render a clear gate-or-scan explanation in collapsed and expanded review UI.

**Architecture:** PR #389 owns the base response object and collapsed-row experience, derived without extra queries from `messages.review_reason`. PR #390 enriches the detail object from `protection_events`, retains beta technical evidence, and renders category/rationale/confidence after expansion. The two PRs remain stacked.

**Tech Stack:** Go/Huma/OpenAPI, PostgreSQL-backed identity store, React/Next.js, TypeScript/Jest, generated TypeScript and Python SDKs.

---

### Task 1: Base `hold_reason` response on PR #389

**Files:**
- Modify: `internal/httpapi/messages.go`
- Modify: `internal/httpapi/reviews.go`
- Test: `internal/httpapi/reviews_test.go`
- Test: `internal/httpapi/messages_parsed_test.go`

- [ ] **Step 1: Write failing mapper and serialization tests**

Add table cases asserting `sender_gate`, `recipient_gate`, both scan codes, `outbound_send`, unknown codes, and empty codes. Assert the account review detail contains `hold_reason`, while `messageViewFromIdentity` leaves it nil.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test ./internal/httpapi -run 'TestHoldReason|TestReviews|TestMessageViewFromIdentity' -count=1
```

Expected: compile or assertion failure because `HoldReasonView` and `hold_reason` do not exist.

- [ ] **Step 3: Implement the minimal base model**

Add:

```go
type HoldReasonView struct {
    Type       string   `json:"type"`
    Code       string   `json:"code"`
    Summary    string   `json:"summary"`
    Category   string   `json:"category,omitempty"`
    Detail     string   `json:"detail,omitempty"`
    Confidence *float64 `json:"confidence,omitempty"`
}

func baseHoldReason(code string) *HoldReasonView
```

Replace the proposed response-side `review_reason` and `scan_score` fields in `ReviewView` and `MessageView` with `HoldReason *HoldReasonView`. Populate it only in `reviewView` and `handleGetReview`.

- [ ] **Step 4: Run focused Go tests and verify GREEN**

Run the command from Step 2 and `go test ./internal/identity -count=1`.

- [ ] **Step 5: Commit the backend slice**

```bash
git add internal/httpapi
git commit -m "feat(reviews): expose a normalized hold reason"
```

### Task 2: Collapsed-row UI on PR #389

**Files:**
- Modify: `web/src/app/components/onboarding/api.ts`
- Modify: `web/src/app/components/types.ts`
- Modify: `web/src/app/(app)/reviews/_components/PendingRow.tsx`
- Modify: `web/src/app/(app)/reviews/_components/reviewReason.ts`
- Test: `web/src/app/(app)/reviews/_components/reviewReason.test.ts`

- [ ] **Step 1: Write failing projection and copy tests**

Assert the wire `hold_reason` object is preserved and the collapsed helper returns the server-provided summary without appending confidence or humanizing codes.

- [ ] **Step 2: Run Jest and verify RED**

```bash
cd web
npm test -- --runInBand --runTestsByPath 'src/app/(app)/reviews/_components/reviewReason.test.ts'
```

- [ ] **Step 3: Implement minimal UI projection**

Define a shared TypeScript `HoldReason` shape, project it from both list and detail wire responses, and render `summary.hold_reason?.summary` in the collapsed row. Keep the warning flag decorative and the full sentence in the title attribute.

- [ ] **Step 4: Run Jest and TypeScript checks**

Run the Step 2 test and `npm run typecheck` if available, otherwise `npx tsc --noEmit`.

- [ ] **Step 5: Commit the web slice**

```bash
git add web/src/app
git commit -m "feat(web): explain every review hold in the queue"
```

### Task 3: Regenerate and publish PR #389

**Files:**
- Regenerate: `api/openapi.yaml`
- Regenerate: `sdks/typescript/src/v1/generated/`
- Regenerate: `sdks/python/src/e2a/v1/generated/`

- [ ] **Step 1: Regenerate the contract and clients**

```bash
make spec
make generate-sdk
```

- [ ] **Step 2: Verify contract freshness and focused suites**

```bash
make spec-check
go test ./internal/httpapi ./internal/identity -count=1
cd web && npm test -- --runInBand --runTestsByPath 'src/app/(app)/reviews/_components/reviewReason.test.ts'
```

- [ ] **Step 3: Commit generated artifacts and push #389**

```bash
git add api/openapi.yaml sdks
git commit -m "chore(sdks): regenerate hold reason models"
git push origin feat/review-reason-ui
```

### Task 4: Safe detail enrichment on PR #390

**Files:**
- Modify: `internal/httpapi/review_protection.go`
- Modify: `internal/httpapi/reviews.go`
- Test: `internal/httpapi/review_protection_test.go`
- Test: `internal/httpapi/reviews_test.go`

- [ ] **Step 1: Restack #390 onto the updated #389**

Replay only #390 commits using the previous #389 tip as the `--onto` boundary; do not touch the dirty original #390 worktree.

- [ ] **Step 2: Write failing safety and attribution tests**

Add cases proving: only `status=ok && flagged=true` yields a rationale; malformed/error/unflagged results yield none; gate-driven mixed events do not enrich the primary reason; scan-driven detail gets category, rationale, and category confidence.

- [ ] **Step 3: Run focused Go tests and verify RED**

```bash
go test ./internal/httpapi -run 'TestProtection|TestRationale|TestReviews_Detail' -count=1
```

- [ ] **Step 4: Implement enrichment**

Keep `protectionFindings` for beta evidence. Add a helper that mutates or returns an enriched copy of the base `HoldReasonView` only for scan driving codes. Change rationale decoding to require successful flagged results and validate confidence with `math.IsNaN`, `math.IsInf`, and range checks.

- [ ] **Step 5: Verify and commit**

Run the Step 3 suite, then commit:

```bash
git add internal/httpapi
git commit -m "feat(reviews): enrich scan hold reasons safely"
```

### Task 5: Expanded detail UX on PR #390

**Files:**
- Modify: `web/src/app/(app)/reviews/_components/PendingRow.tsx`
- Modify: `web/src/app/(app)/reviews/_components/reviewReason.ts`
- Test: `web/src/app/(app)/reviews/_components/reviewReason.test.ts`

- [ ] **Step 1: Write failing display-model tests**

Assert gate detail shows only the summary; scan detail shows category and rationale; confidence is returned only for expanded screening details; missing enrichment falls back to summary.

- [ ] **Step 2: Run Jest and verify RED**

Run the Task 2 Jest command.

- [ ] **Step 3: Implement the selected Option A hierarchy**

Render `Why this message was held`, the friendly category label or summary, and optional detail. Add a `Screening details` disclosure containing confidence and detector/category evidence. Never render confidence in the collapsed row.

- [ ] **Step 4: Run Jest and TypeScript checks, then commit**

```bash
git add web/src/app
git commit -m "feat(web): add progressive hold evidence"
```

### Task 6: Regenerate, review, and publish PR #390

**Files:**
- Regenerate: `api/openapi.yaml`
- Regenerate: TypeScript and Python generated clients

- [ ] **Step 1: Regenerate and run freshness checks**

Run `make spec`, `make generate-sdk`, and `make spec-check`.

- [ ] **Step 2: Run backend and frontend verification**

Run focused HTTP/identity tests, the review-reason Jest suite, TypeScript compilation, and `git diff --check`.

- [ ] **Step 3: Review against the design**

Confirm API scoping, primary/secondary attribution, rationale privacy, fallback behavior, and confidence placement match the design spec.

- [ ] **Step 4: Commit generated artifacts and force-with-lease push #390**

Push the restacked temporary branch to `feat/review-reason-detail`, then inspect both PR checks.

