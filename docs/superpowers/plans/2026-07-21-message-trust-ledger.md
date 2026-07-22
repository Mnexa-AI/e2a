# Canonical Message Trust Ledger Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an append-only, idempotent per-message lifecycle ledger and expose its canonical transition contract consistently through REST, existing events, SDKs, CLI, MCP, and dashboard data access.

**Architecture:** A new `internal/messagelifecycle` leaf package owns the closed catalog, validation, append-only PostgreSQL store, reconstruction, and ordered query. Existing transactional producers append transitions beside message/queue/event state; mapped event payloads carry the exact returned transition objects. A Huma subresource exposes an ascending signed-cursor read, then repository generators and handwritten clients propagate it without hand-editing generated code.

**Tech Stack:** Go 1.25+, PostgreSQL/pgx, River jobs, Huma/OpenAPI 3.1, TypeScript/Vitest, Python/httpx/pytest, MCP/Zod, Next.js/Jest.

---

## File map

### New files

- `migrations/073_message_lifecycle.sql` — reason catalog and append-only transition table.
- `internal/messagelifecycle/model.go` — public transition types, input validation, evidence bounds, and deterministic reconstructed IDs.
- `internal/messagelifecycle/catalog.go` — closed stages, outcomes, reason definitions, and producer mapping helpers.
- `internal/messagelifecycle/model_test.go` — exhaustive catalog and validation tests.
- `internal/messagelifecycle/store.go` — transaction-bound idempotent append and message-scoped ordered reads.
- `internal/messagelifecycle/store_integration_test.go` — migration, atomicity, duplicate, and semantic-conflict tests.
- `internal/messagelifecycle/reconstruct.go` — conservative historical reconstruction and persisted/reconstructed merge.
- `internal/messagelifecycle/reconstruct_test.go` — no-fabrication and deterministic-order tests.
- `internal/httpapi/message_lifecycle.go` — Huma models, signed cursor, endpoint registration, and handler.
- `internal/httpapi/message_lifecycle_test.go` — ownership, ordering, pagination, and cursor tests.
- `web/src/lib/messageLifecycle.ts` — dashboard-only API types and fetch helper.
- `web/src/lib/messageLifecycle.test.ts` — dashboard fetch-contract tests.

### Existing files changed by responsibility

- `internal/httpapi/httpapi.go`, `internal/apiserver/apiserver.go` — inject and register lifecycle reads.
- `internal/eventpayload/payloads.go`, catalog/golden tests and `internal/eventpayload/testdata/*.json` — additive canonical lifecycle arrays on stable message events.
- `internal/relay/server.go`, `internal/relay/inbound_process.go` — inbound acceptance/authentication/queue transitions.
- `internal/identity/store.go`, `inbound_intake_store.go`, `review.go`, `local_delivery.go`, `delivery_store.go` — transaction-local transition writes beside durable message state.
- `internal/agent/outbound_async.go`, `selfsend.go`, `hitl_api.go` — outbound acceptance, queue, review, and local-loopback mappings.
- `internal/outboundsend/worker.go`, `terminal_reconcile.go` — submission attempts and terminal mappings.
- `internal/delivery/consumer.go` — per-recipient delivery, complaint, and causal suppression mappings in the state transaction.
- `internal/webhookpub/*`, `internal/ws/handler_test.go` — stored-envelope and cross-channel equality locks.
- `api/openapi.yaml`, `sdks/typescript/src/v1/generated/**`, `sdks/python/src/e2a/v1/generated/**` — generated from Huma using repository commands only.
- `sdks/typescript/src/v1/client.ts`, `sdks/typescript/src/v1/index.ts` and tests — high-level lifecycle method.
- `sdks/python/src/e2a/v1/client.py`, `__init__.py` and async/sync tests — async resource method plus dynamic sync parity.
- `cli/src/commands/messages.ts`, `cli/src/bin/e2a.ts`, `cli/src/__tests__/messages.test.ts` — lifecycle command.
- `mcp/src/client.ts`, `mcp/src/tools/messages.ts`, `mcp/tests/client.test.ts`, `mcp/tests/tools.test.ts` — lifecycle tool.
- `docs/api.md`, `docs/events.md` — contract and compatibility documentation.

## Private integration database

Every DB-backed command in this plan uses only:

```bash
export E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable'
```

Run all DB-backed Go tests with `-p 1`. Do not point another session or
worktree at this database.

### Task 1: Canonical vocabulary and validation

**Files:**
- Create: `internal/messagelifecycle/catalog.go`
- Create: `internal/messagelifecycle/model.go`
- Create: `internal/messagelifecycle/model_test.go`

- [ ] **Step 1: Write the failing exhaustive catalog tests**

Define table-driven tests that enumerate every approved reason exactly once,
assert its fixed stage/outcome/retryability tuple, validate DMARC status and
bounce-type mapping, and reject unknown values:

```go
func TestCatalogIsClosedAndConsistent(t *testing.T) {
	want := map[ReasonCode]Definition{
		ReasonAcceptanceInboundSMTP: {Stage: StageAccepted, Outcome: OutcomeAccepted},
		ReasonAcceptanceOutboundAPI: {Stage: StageAccepted, Outcome: OutcomeAccepted},
		ReasonAcceptanceLocalLoopback: {Stage: StageAccepted, Outcome: OutcomeAccepted},
		ReasonAuthenticationDMARCPass: {Stage: StageAuthentication, Outcome: OutcomePassed},
		ReasonAuthenticationDMARCFail: {Stage: StageAuthentication, Outcome: OutcomeFailed},
		ReasonAuthenticationDMARCNone: {Stage: StageAuthentication, Outcome: OutcomeIndeterminate},
		ReasonAuthenticationDMARCTemporaryError: {Stage: StageAuthentication, Outcome: OutcomeIndeterminate, Retryable: true},
		ReasonAuthenticationDMARCPermanentError: {Stage: StageAuthentication, Outcome: OutcomeIndeterminate},
		ReasonReviewHoldCreated: {Stage: StageReview, Outcome: OutcomePending},
		ReasonReviewApproved: {Stage: StageReview, Outcome: OutcomeApproved},
		ReasonReviewRejected: {Stage: StageReview, Outcome: OutcomeRejected},
		ReasonReviewExpiredApproved: {Stage: StageReview, Outcome: OutcomeApproved},
		ReasonReviewExpiredRejected: {Stage: StageReview, Outcome: OutcomeRejected},
		ReasonSuppressionRecipientBlocked: {Stage: StageSuppression, Outcome: OutcomeBlocked},
		ReasonSuppressionHardBounceApplied: {Stage: StageSuppression, Outcome: OutcomeApplied},
		ReasonSuppressionComplaintApplied: {Stage: StageSuppression, Outcome: OutcomeApplied},
		ReasonQueueInboundProcessing: {Stage: StageQueued, Outcome: OutcomeEnqueued},
		ReasonQueueOutboundSubmission: {Stage: StageQueued, Outcome: OutcomeEnqueued},
		ReasonSubmissionUpstreamAccepted: {Stage: StageSubmission, Outcome: OutcomeAccepted},
		ReasonSubmissionLocalLoopbackAccepted: {Stage: StageSubmission, Outcome: OutcomeAccepted},
		ReasonSubmissionTemporaryFailure: {Stage: StageSubmission, Outcome: OutcomeDeferred, Retryable: true},
		ReasonSubmissionProviderRejected: {Stage: StageSubmission, Outcome: OutcomeFailed},
		ReasonSubmissionLocalRetriesExhausted: {Stage: StageSubmission, Outcome: OutcomeFailed, Retryable: true},
		ReasonSubmissionCancelled: {Stage: StageSubmission, Outcome: OutcomeFailed},
		ReasonDeliveryRecipientServerAccepted: {Stage: StageDelivery, Outcome: OutcomeDelivered},
		ReasonDeliveryTemporaryDelay: {Stage: StageDelivery, Outcome: OutcomeDeferred, Retryable: true},
		ReasonDeliveryPermanentBounce: {Stage: StageDelivery, Outcome: OutcomeBounced},
		ReasonDeliveryTransientBounce: {Stage: StageDelivery, Outcome: OutcomeBounced, Retryable: true},
		ReasonDeliveryUndeterminedBounce: {Stage: StageDelivery, Outcome: OutcomeBounced},
		ReasonComplaintRecipientReported: {Stage: StageComplaint, Outcome: OutcomeReported},
	}
	if len(Catalog()) != len(want) {
		t.Fatalf("Catalog() has %d reasons, want %d", len(Catalog()), len(want))
	}
	for code, definition := range want {
		got, ok := Lookup(code)
		if !ok || got != definition {
			t.Fatalf("Lookup(%q) = %+v, %v; want %+v", code, got, ok, definition)
		}
	}
	if _, ok := Lookup("provider.free_form"); ok {
		t.Fatal("unknown reason unexpectedly accepted")
	}
}
```

Also test evidence rejection for forbidden keys and limits of 2 KiB per string
and 16 KiB serialized, plus direction and recipient invariants.

- [ ] **Step 2: Run the package test and verify the expected failure**

Run:

```bash
go test ./internal/messagelifecycle -run 'TestCatalog|TestNewTransition|TestEvidence' -count=1
```

Expected: FAIL because `internal/messagelifecycle` and its contract types do
not exist.

- [ ] **Step 3: Implement the closed catalog and canonical model**

Implement typed string constants and one immutable definition map:

```go
type Stage string
type Outcome string
type ReasonCode string

type Definition struct {
	Stage     Stage
	Outcome   Outcome
	Retryable bool
}

type MessageLifecycleTransition struct {
	ID             string            `json:"id"`
	MessageID      string            `json:"message_id"`
	Direction      string            `json:"direction" enum:"inbound,outbound"`
	Recipient      string            `json:"recipient,omitempty"`
	Stage          Stage             `json:"stage" enum:"accepted,authentication,review,suppression,queued,submission,delivery,complaint"`
	Outcome        Outcome           `json:"outcome" enum:"accepted,passed,failed,indeterminate,pending,approved,rejected,blocked,applied,enqueued,deferred,delivered,bounced,reported"`
	ReasonCode     ReasonCode        `json:"reason_code" enum:"acceptance.inbound_smtp,acceptance.outbound_api,acceptance.local_loopback,authentication.dmarc_pass,authentication.dmarc_fail,authentication.dmarc_none,authentication.dmarc_temporary_error,authentication.dmarc_permanent_error,review.hold_created,review.approved,review.rejected,review.expired_approved,review.expired_rejected,suppression.recipient_blocked,suppression.hard_bounce_applied,suppression.complaint_applied,queue.inbound_processing,queue.outbound_submission,submission.upstream_accepted,submission.local_loopback_accepted,submission.temporary_failure,submission.provider_rejected,submission.local_retries_exhausted,submission.cancelled,delivery.recipient_server_accepted,delivery.temporary_delay,delivery.permanent_bounce,delivery.transient_bounce,delivery.undetermined_bounce,complaint.recipient_reported"`
	Retryable      bool              `json:"retryable"`
	Evidence       map[string]any    `json:"evidence"`
	CorrelationIDs map[string]string `json:"correlation_ids"`
	OccurredAt     time.Time         `json:"occurred_at"`
	Reconstructed  bool              `json:"reconstructed"`
}

type AppendInput struct {
	MessageID      string
	DedupeKey      string
	Direction      string
	Recipient      string
	ReasonCode     ReasonCode
	Evidence       map[string]any
	CorrelationIDs map[string]string
	OccurredAt     time.Time
}
```

`NewTransition` looks up the definition rather than accepting stage/outcome or
retryability from callers. Permit only the approved evidence and correlation
keys, normalize nil maps to empty objects, force UTC timestamps, and require a
recipient for delivery/complaint and recipient-specific suppression reasons.

- [ ] **Step 4: Run the focused tests and verify they pass**

Run:

```bash
go test ./internal/messagelifecycle -run 'TestCatalog|TestNewTransition|TestEvidence' -count=1
```

Expected: PASS with every approved reason covered and unknown combinations
rejected.

- [ ] **Step 5: Commit the canonical contract**

```bash
git add internal/messagelifecycle/catalog.go internal/messagelifecycle/model.go internal/messagelifecycle/model_test.go
git commit -m "feat(lifecycle): define canonical transition vocabulary"
```

### Task 2: Sequential migration and idempotent append store

**Files:**
- Create: `migrations/073_message_lifecycle.sql`
- Create: `internal/messagelifecycle/store.go`
- Create: `internal/messagelifecycle/store_integration_test.go`
- Modify: `internal/identity/migrate_test.go`

- [ ] **Step 1: Write failing migration and append-store integration tests**

Use `testutil.NewDB` and a real transaction to assert catalog tuples, FK
rejection, append success, exact duplicate replay, mismatched duplicate error,
ascending equal-timestamp ordering, and rollback atomicity:

```go
func TestAppendTxIsLogicallyIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewDB(t)
	msgID := insertLifecycleFixtureMessage(t, pool)
	input := AppendInput{
		MessageID: msgID, DedupeKey: "accept", Direction: "outbound",
		ReasonCode: ReasonAcceptanceOutboundAPI, OccurredAt: time.Now().UTC(),
	}
	first := appendInCommittedTx(t, pool, input)
	second := appendInCommittedTx(t, pool, input)
	if diff := cmp.Diff(first, second); diff != "" {
		t.Fatalf("duplicate changed transition (-first +second):\n%s", diff)
	}
	assertLifecycleCount(t, pool, msgID, 1)
}
```

Run the migration twice in the migration test to prove idempotency.

- [ ] **Step 2: Run the DB-backed tests and verify the expected failure**

Run:

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
go test -tags integration -p 1 ./internal/messagelifecycle ./internal/identity \
  -run 'TestAppendTx|TestMessageLifecycleMigration' -count=1
```

Expected: FAIL because migration 073 and `AppendTx` do not exist.

- [ ] **Step 3: Add the production-safe migration**

Create an idempotent catalog table with a unique composite key and seed every
definition using `INSERT ... ON CONFLICT DO NOTHING`. Create the empty ledger
table and its two indexes:

```sql
CREATE TABLE IF NOT EXISTS message_lifecycle_reason_codes (
    code       TEXT PRIMARY KEY,
    stage      TEXT NOT NULL CHECK (stage IN ('accepted','authentication','review','suppression','queued','submission','delivery','complaint')),
    outcome    TEXT NOT NULL CHECK (outcome IN ('accepted','passed','failed','indeterminate','pending','approved','rejected','blocked','applied','enqueued','deferred','delivered','bounced','reported')),
    retryable  BOOLEAN NOT NULL,
    UNIQUE (code, stage, outcome, retryable)
);

INSERT INTO message_lifecycle_reason_codes(code, stage, outcome, retryable) VALUES
 ('acceptance.inbound_smtp','accepted','accepted',false),
 ('acceptance.outbound_api','accepted','accepted',false),
 ('acceptance.local_loopback','accepted','accepted',false),
 ('authentication.dmarc_pass','authentication','passed',false),
 ('authentication.dmarc_fail','authentication','failed',false),
 ('authentication.dmarc_none','authentication','indeterminate',false),
 ('authentication.dmarc_temporary_error','authentication','indeterminate',true),
 ('authentication.dmarc_permanent_error','authentication','indeterminate',false),
 ('review.hold_created','review','pending',false),
 ('review.approved','review','approved',false),
 ('review.rejected','review','rejected',false),
 ('review.expired_approved','review','approved',false),
 ('review.expired_rejected','review','rejected',false),
 ('suppression.recipient_blocked','suppression','blocked',false),
 ('suppression.hard_bounce_applied','suppression','applied',false),
 ('suppression.complaint_applied','suppression','applied',false),
 ('queue.inbound_processing','queued','enqueued',false),
 ('queue.outbound_submission','queued','enqueued',false),
 ('submission.upstream_accepted','submission','accepted',false),
 ('submission.local_loopback_accepted','submission','accepted',false),
 ('submission.temporary_failure','submission','deferred',true),
 ('submission.provider_rejected','submission','failed',false),
 ('submission.local_retries_exhausted','submission','failed',true),
 ('submission.cancelled','submission','failed',false),
 ('delivery.recipient_server_accepted','delivery','delivered',false),
 ('delivery.temporary_delay','delivery','deferred',true),
 ('delivery.permanent_bounce','delivery','bounced',false),
 ('delivery.transient_bounce','delivery','bounced',true),
 ('delivery.undetermined_bounce','delivery','bounced',false),
 ('complaint.recipient_reported','complaint','reported',false)
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS message_lifecycle_transitions (
    id              TEXT PRIMARY KEY,
    message_id      TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    dedupe_key      TEXT NOT NULL,
    direction       TEXT NOT NULL CHECK (direction IN ('inbound','outbound')),
    recipient       TEXT,
    stage           TEXT NOT NULL,
    outcome         TEXT NOT NULL,
    reason_code     TEXT NOT NULL,
    retryable       BOOLEAN NOT NULL,
    evidence        JSONB NOT NULL DEFAULT '{}'::jsonb,
    correlation_ids JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at     TIMESTAMPTZ NOT NULL,
    reconstructed   BOOLEAN NOT NULL DEFAULT false,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, dedupe_key),
    FOREIGN KEY (reason_code, stage, outcome, retryable)
      REFERENCES message_lifecycle_reason_codes(code, stage, outcome, retryable)
);

CREATE INDEX IF NOT EXISTS message_lifecycle_message_order_idx
    ON message_lifecycle_transitions(message_id, occurred_at, id);
```

The catalog seeds must exactly match `catalog.go`; add a test comparing SQL
rows to `Catalog()` so drift fails.

- [ ] **Step 4: Implement transaction-bound append**

Implement:

```go
func AppendTx(ctx context.Context, tx pgx.Tx, input AppendInput) (MessageLifecycleTransition, error)
```

Generate `mlt_` plus 16 cryptographically random bytes for the first insert.
Use `INSERT ... ON CONFLICT (message_id, dedupe_key) DO NOTHING RETURNING ...`.
On conflict, select the existing row and compare every semantic field except
the generated ID and recorded time. Return `ErrDedupeConflict` on mismatch.

- [ ] **Step 5: Run the DB-backed tests and verify they pass**

Run the command from Step 2.

Expected: PASS; rerunning migrations leaves the same catalog and indexes.

- [ ] **Step 6: Commit migration and store**

```bash
git add migrations/073_message_lifecycle.sql internal/messagelifecycle/store.go internal/messagelifecycle/store_integration_test.go internal/identity/migrate_test.go
git commit -m "feat(lifecycle): persist idempotent transitions"
```

### Task 3: Conservative historical reconstruction

**Files:**
- Create: `internal/messagelifecycle/reconstruct.go`
- Create: `internal/messagelifecycle/reconstruct_test.go`
- Modify: `internal/messagelifecycle/store.go`
- Modify: `internal/messagelifecycle/store_integration_test.go`

- [ ] **Step 1: Write failing reconstruction tests**

Cover inbound creation/authentication, outbound provider acceptance,
per-recipient delivered/deferred/bounced/complained state, retained event
timestamps, omitted ambiguous failures, deterministic `mlt_recon_` IDs, and
persisted-row precedence:

```go
func TestReconstructDoesNotInventIntermediateStages(t *testing.T) {
	snapshot := Snapshot{
		MessageID: "msg_old", Direction: "outbound", CreatedAt: fixed,
		DeliveryStatus: "delivered",
		Recipients: []RecipientSnapshot{{Address: "a@example.com", Status: "delivered", UpdatedAt: fixed.Add(time.Hour)}},
	}
	got := Reconstruct(snapshot)
	assertStages(t, got, StageAccepted, StageDelivery)
	assertNoStage(t, got, StageQueued)
	assertNoStage(t, got, StageSubmission)
}
```

- [ ] **Step 2: Run tests and verify the expected failure**

Run:

```bash
go test ./internal/messagelifecycle -run 'TestReconstruct|TestMergeTransitions' -count=1
```

Expected: FAIL because reconstruction functions do not exist.

- [ ] **Step 3: Implement deterministic reconstruction and merge**

Build reconstructed entries only from source fields with timestamps. Derive
IDs as `mlt_recon_` plus the first 16 bytes of SHA-256 over a canonical
newline-separated tuple of message, source, recipient, reason, and RFC3339Nano
timestamp. Mark evidence with a bounded `source` key and set
`reconstructed=true`.

Implement:

```go
func (s *Store) ListForMessage(ctx context.Context, messageID, agentID string) ([]MessageLifecycleTransition, error)
```

Load the owned message snapshot, recipient rows, retained mapped events, and
persisted transitions. Merge by semantic dedupe key with persisted rows
winning, then sort ascending by `(OccurredAt, ID)`.

- [ ] **Step 4: Run unit and integration tests**

Run:

```bash
go test ./internal/messagelifecycle -run 'TestReconstruct|TestMergeTransitions' -count=1
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
go test -tags integration -p 1 ./internal/messagelifecycle -run 'TestListForMessage' -count=1
```

Expected: PASS with stable ordering and no fabricated queue/submission rows.

- [ ] **Step 5: Commit reconstruction**

```bash
git add internal/messagelifecycle/reconstruct.go internal/messagelifecycle/reconstruct_test.go internal/messagelifecycle/store.go internal/messagelifecycle/store_integration_test.go
git commit -m "feat(lifecycle): reconstruct historical observations"
```

### Task 4: Cursor-safe REST lifecycle endpoint

**Files:**
- Create: `internal/httpapi/message_lifecycle.go`
- Create: `internal/httpapi/message_lifecycle_test.go`
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/apiserver/apiserver.go`
- Modify: `internal/apiserver/apiserver_test.go`

- [ ] **Step 1: Write failing handler and schema tests**

Register a fake lifecycle lister and test default/max limit, ascending equal
timestamps, page boundaries, agent/message-bound cursor rejection, foreign
ownership hiding, empty page shape, canonical JSON fields, and operation ID:

```go
func TestMessageLifecycleCursorIsBoundToMessage(t *testing.T) {
	server := newLifecycleServer(t, fixedTransitions(3))
	first := getLifecycle(t, server, "agent@example.com", "msg_one", "?limit=1")
	res := perform(t, server, http.MethodGet,
		"/v1/agents/agent@example.com/messages/msg_two/lifecycle?cursor="+url.QueryEscape(*first.NextCursor))
	assertAPIError(t, res, http.StatusBadRequest, "invalid_cursor")
}
```

- [ ] **Step 2: Run tests and verify the expected failure**

Run:

```bash
go test ./internal/httpapi ./internal/apiserver -run 'TestMessageLifecycle' -count=1
```

Expected: FAIL because the route, models, and dependency do not exist.

- [ ] **Step 3: Implement the Huma endpoint and dependency wiring**

Add this dependency:

```go
type MessageLifecycleLister func(context.Context, string, string) ([]messagelifecycle.MessageLifecycleTransition, error)
```

Register `GET /v1/agents/{email}/messages/{id}/lifecycle` with operation ID
`get-message-lifecycle`. Resolve the owned agent first, call the lister with
message and agent IDs, decode a signed cursor containing version, agent ID,
message ID, occurred-at, and transition ID, slice `limit+1`, and emit
`Page[messagelifecycle.MessageLifecycleTransition]`.

Wire `messagelifecycle.NewStore(p.Pool).ListForMessage` through
`apiserver.BuildDeps`; `apiserver.Params.Pool` is already shared by production
and the contract-test harness.

- [ ] **Step 4: Run focused endpoint tests**

Run the command from Step 2.

Expected: PASS, including OpenAPI schema assertions for closed direction,
stage, outcome, and reason enums and open evidence/correlation objects.

- [ ] **Step 5: Commit the read endpoint**

```bash
git add internal/httpapi/message_lifecycle.go internal/httpapi/message_lifecycle_test.go internal/httpapi/httpapi.go internal/apiserver/apiserver.go internal/apiserver/apiserver_test.go
git commit -m "feat(api): expose message lifecycle"
```

### Task 5: Inbound acceptance, authentication, queue, and event parity

**Files:**
- Modify: `internal/relay/server.go`
- Modify: `internal/relay/inbound_process.go`
- Modify: `internal/relay/server_outbox_test.go`
- Modify: `internal/relay/inbound_async_test.go`
- Modify: `internal/relay/webhook_payload_test.go`
- Modify: `internal/identity/inbound_intake_store.go`
- Modify: `internal/eventpayload/payloads.go`

- [ ] **Step 1: Write failing inbound atomicity and payload tests**

Extend relay integration tests to assert that persisted inbound SMTP delivery
contains acceptance and DMARC transition rows, async mode additionally
contains queueing, a forced lifecycle insert failure rolls back message/intake/
outbox writes, and duplicate intake execution does not duplicate transitions.
Assert `email.received.data.lifecycle_transitions` equals the stored rows.

- [ ] **Step 2: Run focused tests and verify the expected failure**

Run:

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
go test -tags integration -p 1 ./internal/relay ./internal/identity \
  -run 'Test.*Lifecycle|TestInboundAsync.*Lifecycle' -count=1
```

Expected: FAIL because inbound transactions do not append lifecycle rows or
embed them in events.

- [ ] **Step 3: Append inbound transitions inside the existing transaction**

Before building `email.received`, append fixed dedupe keys:

```go
accepted, err := messagelifecycle.AppendTx(ctx, tx, messagelifecycle.AppendInput{
	MessageID: msg.ID, DedupeKey: "acceptance", Direction: "inbound",
	ReasonCode: messagelifecycle.ReasonAcceptanceInboundSMTP,
	OccurredAt: msg.CreatedAt,
})
```

Map only `authentication.DMARC.Status` to the five approved authentication
reasons and include the existing structured authentication object as evidence.
For async intake, append `queue.inbound_processing` using the durable intake or
River job ID. Build `email.received` after these inserts and assign the exact
returned objects to `LifecycleTransitions`.

- [ ] **Step 4: Run focused inbound tests**

Run the command from Step 2.

Expected: PASS; screening tests remain unchanged and no flagged/blocked event
contains a new lifecycle transition.

- [ ] **Step 5: Commit inbound lifecycle writes**

```bash
git add internal/relay internal/identity/inbound_intake_store.go internal/eventpayload/payloads.go
git commit -m "feat(lifecycle): record inbound observations"
```

### Task 6: Outbound acceptance, review, queue, and local loopback

**Files:**
- Modify: `internal/agent/outbound_async.go`
- Modify: `internal/agent/hitl_api.go`
- Modify: `internal/agent/selfsend.go`
- Modify: `internal/identity/store.go`
- Modify: `internal/identity/review.go`
- Modify: `internal/identity/local_delivery.go`
- Modify: `internal/agent/outbound_async_test.go`
- Modify: `internal/agent/approve_async_test.go`
- Modify: `internal/agent/selfsend_test.go`
- Modify: `internal/identity/approve_accept_test.go`

- [ ] **Step 1: Write failing outbound transaction tests**

For direct send, held send, approval, expiry, and self-send, assert exact
accepted/review/queued/submission transitions and fixed dedupe behavior. Force
transition failure to prove message, job, review state, local receiving copy,
and events roll back together.

- [ ] **Step 2: Run focused tests and verify the expected failure**

Run:

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
go test -tags integration -p 1 ./internal/agent ./internal/identity ./internal/hitlworker \
  -run 'Test.*Lifecycle|TestDeliverOutbound.*Lifecycle|TestSelfSend.*Lifecycle|TestApprove.*Lifecycle' -count=1
```

Expected: FAIL because these transactions do not append lifecycle rows.

- [ ] **Step 3: Add minimal transition writes at transaction boundaries**

Use `acceptance.outbound_api` on initial persistence,
`review.hold_created|approved|rejected|expired_*` on actual review state
changes, and `queue.outbound_submission` only after River returns a durable job
ID. For self-send, append `submission.local_loopback_accepted` to the outbound
row and `acceptance.local_loopback` to the receiving row, embedding those exact
objects in `email.sent` and `email.received`.

Do not append any screening reason or infer a review cause.

- [ ] **Step 4: Run focused outbound acceptance tests**

Run the command from Step 2.

Expected: PASS with duplicate approvals and job re-drives producing one logical
transition each.

- [ ] **Step 5: Commit outbound acceptance lifecycle**

```bash
git add internal/agent internal/identity internal/hitlworker
git commit -m "feat(lifecycle): record outbound acceptance and review"
```

### Task 7: Submission worker and terminal reconciler

**Files:**
- Modify: `internal/outboundsend/worker.go`
- Modify: `internal/outboundsend/terminal_reconcile.go`
- Modify: `internal/agent/outbound_async.go`
- Modify: `internal/outboundsend/worker_test.go`
- Modify: `internal/outboundsend/reconcile_test.go`
- Modify: `internal/agent/webhooks_events_test.go`

- [ ] **Step 1: Write failing worker idempotency and mapping tests**

Test upstream acceptance, temporary attempts, provider rejection, local retry
exhaustion, reconciliation correction, cancellation, and duplicate River
execution. Require distinct dedupe keys for distinct attempt numbers and the
same key for a duplicate execution of one attempt. Assert `email.sent` and
`email.failed` embed the stored terminal transition.

- [ ] **Step 2: Run focused tests and verify the expected failure**

Run:

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
go test -tags integration -p 1 ./internal/outboundsend ./internal/agent \
  -run 'Test.*Lifecycle|TestSendWorker.*Lifecycle|TestReconcile.*Lifecycle' -count=1
```

Expected: FAIL because worker outcomes lack lifecycle persistence.

- [ ] **Step 3: Append observed submission attempts with stable keys**

Use dedupe keys shaped as:

```go
fmt.Sprintf("submission:job:%d:attempt:%d:%s", jobID, attempt, reasonCode)
```

Persist attempt observations in the same transaction that writes terminal or
corrected message state. Replace best-effort event-only transactions with one
post-provider database transaction containing state, lifecycle, metering, and
event outbox changes; failure leaves reconciliation to the existing recovery
path rather than publishing a contradictory event.

- [ ] **Step 4: Run focused worker tests**

Run the command from Step 2.

Expected: PASS; provider acceptance remains semantically `submission`, never
`delivery`.

- [ ] **Step 5: Commit submission lifecycle**

```bash
git add internal/outboundsend internal/agent/outbound_async.go internal/agent/webhooks_events_test.go
git commit -m "feat(lifecycle): record submission outcomes"
```

### Task 8: Delivery feedback, complaint, and causal suppression

**Files:**
- Modify: `internal/delivery/consumer.go`
- Modify: `internal/identity/delivery_store.go`
- Modify: `internal/delivery/consumer_test.go`
- Modify: `internal/delivery/integration_test.go`
- Modify: `internal/delivery/consumer_crosspath_test.go`
- Modify: `internal/eventpayload/payloads.go`

- [ ] **Step 1: Write failing per-recipient feedback tests**

Cover delivered, delay, permanent/transient/undetermined bounce, complaint,
hard-bounce suppression, complaint suppression, duplicate/out-of-order SNS,
and SES Reject. Assert recipient address, retryability, safe evidence,
provider/message/event correlations, and one logical row per provider
observation. Force event insertion failure and prove recipient/message/
suppression/lifecycle updates all roll back.

- [ ] **Step 2: Run focused tests and verify the expected failure**

Run:

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
go test -tags integration -p 1 ./internal/delivery ./internal/identity \
  -run 'Test.*Lifecycle|TestConsumer.*Lifecycle|TestFeedback.*Atomic' -count=1
```

Expected: FAIL because delivery state and event publication currently use
separate transaction semantics and no lifecycle rows.

- [ ] **Step 3: Move mapped feedback into one local transaction**

Extend the delivery-store transaction callback to append the canonical
per-recipient transition, optional causal suppression transition, and mapped
event envelopes before commit. Use provider notification identity plus
recipient/outcome for dedupe. Map SES delivery to
`delivery.recipient_server_accepted`, never inbox placement.

- [ ] **Step 4: Run focused feedback tests**

Run the command from Step 2.

Expected: PASS with duplicate SNS delivery leaving one logical transition and
out-of-order feedback preserving all observations without regressing rollups.

- [ ] **Step 5: Commit delivery lifecycle**

```bash
git add internal/delivery internal/identity/delivery_store.go internal/eventpayload/payloads.go
git commit -m "feat(lifecycle): record provider feedback"
```

### Task 9: Event, webhook, and WebSocket representation lock

**Files:**
- Modify: `internal/eventpayload/catalog.go`
- Modify: `internal/eventpayload/golden_test.go`
- Modify: `internal/eventpayload/testdata/email.received*.json`
- Modify: `internal/eventpayload/testdata/email.sent*.json`
- Modify: `internal/eventpayload/testdata/email.failed*.json`
- Modify: `internal/eventpayload/testdata/email.delivered*.json`
- Modify: `internal/eventpayload/testdata/email.bounced*.json`
- Modify: `internal/eventpayload/testdata/email.complained*.json`
- Modify: `internal/httpapi/eventpayload_schemas_test.go`
- Modify: `internal/ws/handler_test.go`
- Modify: `internal/webhookpub/outbox_tx_integration_test.go`

- [ ] **Step 1: Write failing golden and cross-channel tests**

Require every mapped stable payload schema to expose optional
`lifecycle_transitions`, every fixture transition to validate against the same
OpenAPI component used by the endpoint, stored event REST data to equal webhook
data, and the `email.received` WebSocket frame to byte-match the stored
envelope. Keep unknown/additive evidence fields accepted.

- [ ] **Step 2: Run focused tests and verify the expected failure**

Run:

```bash
go test ./internal/eventpayload ./internal/httpapi ./internal/ws ./internal/webhookpub \
  -run 'Test.*Lifecycle|Test.*Golden|Test.*CrossChannel' -count=1
```

Expected: FAIL because stable payload schemas and fixtures lack the lifecycle
array.

- [ ] **Step 3: Add the canonical array to stable payloads and fixtures**

Add:

```go
LifecycleTransitions []messagelifecycle.MessageLifecycleTransition `json:"lifecycle_transitions,omitempty" nullable:"false"`
```

to the six stable email payload structs. Keep beta review event data as open
maps, but insert the same field at their builders. Update fixtures from actual
canonical serialization; do not create a parallel event-only transition type.

- [ ] **Step 4: Run focused event tests**

Run the command from Step 2.

Expected: PASS with event redelivery reusing the original stored bytes.

- [ ] **Step 5: Commit the cross-channel contract**

```bash
git add internal/eventpayload internal/httpapi/eventpayload_schemas_test.go internal/ws/handler_test.go internal/webhookpub/outbox_tx_integration_test.go
git commit -m "feat(events): attach canonical lifecycle transitions"
```

### Task 10: OpenAPI generation and compatibility gate

**Files:**
- Modify: `api/openapi.yaml` by `make spec`
- Modify: `sdks/typescript/src/v1/generated/**` by `make generate-sdk`
- Modify: `sdks/python/src/e2a/v1/generated/**` by `make generate-sdk`
- Modify: `internal/httpapi/spec_review_test.go`
- Modify: `internal/httpapi/response_enum_stance_test.go`

- [ ] **Step 1: Add failing spec assertions before generation**

Assert the operation/path, ascending-page schema, required transition fields,
closed initial enums, nullable recipient, open evidence/correlation objects,
and optional event lifecycle arrays. Assert no screening reason or stage is
present.

- [ ] **Step 2: Run spec tests and verify drift/failure**

Run:

```bash
go test ./internal/httpapi -run 'Test.*Lifecycle.*Schema|TestSpecGoldenNoDrift' -count=1
```

Expected: FAIL because committed `api/openapi.yaml` is stale.

- [ ] **Step 3: Regenerate spec and both SDK bases using repository commands**

Run:

```bash
make spec
make generate-sdk
```

Do not edit anything under either generated tree by hand.

- [ ] **Step 4: Run freshness and compatibility checks**

Run:

```bash
make spec-check
make generate-sdk-check
make openapi-compat-check
```

Expected: all PASS. Inspect the compatibility report to confirm only additive
path/schema/property changes.

- [ ] **Step 5: Commit generated contract**

```bash
git add api/openapi.yaml sdks/typescript/src/v1/generated sdks/python/src/e2a/v1/generated internal/httpapi/spec_review_test.go internal/httpapi/response_enum_stance_test.go
git commit -m "feat(api): publish lifecycle contract"
```

### Task 11: Handwritten TypeScript and Python SDKs

**Files:**
- Modify: `sdks/typescript/src/v1/client.ts`
- Modify: `sdks/typescript/src/v1/index.ts`
- Modify: `sdks/typescript/test/v1/client.test.ts`
- Modify: `sdks/typescript/test/v1/client.types.ts`
- Modify: `sdks/typescript/test/v1/contract.test.ts`
- Modify: `sdks/python/src/e2a/v1/client.py`
- Modify: `sdks/python/src/e2a/v1/__init__.py`
- Modify: `sdks/python/tests/test_v1_client.py`
- Modify: `sdks/python/tests/test_v1_sync_client.py`
- Modify: `sdks/python/tests/test_contract.py`

- [ ] **Step 1: Write failing high-level client and type tests**

TypeScript must call the generated `MessagesApi.getMessageLifecycle` from the
existing `messages` resource and return the generated page. Python adds the
async `messages` resource method; the dynamic sync facade must expose the same
method and forward email, message ID, cursor, and limit without renaming
errors. Contract tests parse a response
containing optional recipient, evidence, correlations, and reconstructed rows.

- [ ] **Step 2: Run focused SDK tests and verify the expected failure**

Run:

```bash
npm run test:unit --workspace @e2a/sdk
(cd sdks/python && pytest tests/test_v1_client.py tests/test_v1_sync_client.py -q)
```

Expected: FAIL because handwritten lifecycle methods are absent.

- [ ] **Step 3: Add thin handwritten methods over generated APIs**

TypeScript:

```ts
getLifecycle(
  email: string,
  messageId: string,
  params: { cursor?: string; limit?: number } = {},
): Promise<PageMessageLifecycleTransition> {
  return call(() => this.api.getMessageLifecycle(email, messageId, params.cursor, params.limit));
}
```

Python `MessagesResource.get_lifecycle` calls the generated endpoint through
the existing `_read` retry/auth wrapper and returns its generated page model.
No bespoke sync implementation is added: `E2AClient`'s existing dynamic mirror
wraps the async resource, and the sync test proves that behavior.

- [ ] **Step 4: Run SDK unit/type tests**

Run:

```bash
npm test --workspace @e2a/sdk
(cd sdks/python && pytest tests/ -q && mypy)
```

Expected: PASS.

- [ ] **Step 5: Commit handwritten SDK parity**

```bash
git add sdks/typescript/src/v1 sdks/typescript/test/v1 sdks/python/src/e2a/v1 sdks/python/tests
git commit -m "feat(sdk): add message lifecycle clients"
```

### Task 12: CLI, MCP, and dashboard data client

**Files:**
- Modify: `cli/src/commands/messages.ts`
- Modify: `cli/src/bin/e2a.ts`
- Modify: `cli/src/__tests__/messages.test.ts`
- Modify: `mcp/src/client.ts`
- Modify: `mcp/src/tools/messages.ts`
- Modify: `mcp/tests/client.test.ts`
- Modify: `mcp/tests/tools.test.ts`
- Create: `web/src/lib/messageLifecycle.ts`
- Create: `web/src/lib/messageLifecycle.test.ts`

- [x] **Step 1: Write failing surface tests**

CLI tests require `messages lifecycle <id> --agent <email> [--limit] [--cursor]
--json`, usage failures for missing ID/invalid limit, and unchanged exit codes.
MCP tests require a strict `get_message_lifecycle` schema and exact parameter
forwarding. Dashboard tests require URL encoding and response parsing without
rendering UI.

- [x] **Step 2: Run focused tests and verify the expected failure**

Run:

```bash
npm test --workspace @e2a/cli -- messages.test.ts
npm test --workspace @e2a/mcp-server -- tools.test.ts client.test.ts
(cd web && npm test -- --runTestsByPath src/lib/messageLifecycle.test.ts)
```

Expected: FAIL because none of the three surfaces has lifecycle access.

- [x] **Step 3: Implement minimal parity surfaces**

CLI prints the page JSON using the existing camel-case model serialization.
MCP registers a read-only/idempotent tool:

```ts
server.registerTool("get_message_lifecycle", {
  title: "Get message lifecycle",
  annotations: { readOnlyHint: true, idempotentHint: true },
  inputSchema: strictInputSchema({
    message_id: z.string(),
    email: z.string().optional(),
    cursor: z.string().optional(),
    limit: z.number().int().min(1).max(100).optional(),
  }),
}, (args) => runTool(() => client.getMessageLifecycle(args.message_id, args, args.email)));
```

The dashboard helper calls the same `/v1/agents/.../lifecycle` proxy path
and exports only types/fetching; do not modify components or pages.

- [x] **Step 4: Run focused and package-level tests**

Run:

```bash
npm test --workspace @e2a/cli
npm run build --workspace @e2a/cli
npm test --workspace @e2a/mcp-server
npm run build --workspace @e2a/mcp-server
(cd web && npm test && npm run lint && npm run build)
```

Expected: PASS.

- [x] **Step 5: Commit remaining client surfaces**

```bash
git add cli mcp web/src/lib/messageLifecycle.ts web/src/lib/messageLifecycle.test.ts
git commit -m "feat(clients): expose message lifecycle"
```

### Task 13: Documentation and full verification

**Files:**
- Modify: `docs/api.md`
- Modify: `docs/events.md`
- Modify: `docs/superpowers/plans/2026-07-21-message-trust-ledger.md` only to check completed boxes during execution

- [x] **Step 1: Write documentation contract checks or failing text assertions**

Extend existing repository text-integrity/spec-review tests to require the
lifecycle endpoint, closed vocabulary, recipient-server definition, cursor
ordering, reconstruction marker, idempotency statement, additive compatibility
policy, and explicit exclusion of screening/inbox placement.

- [x] **Step 2: Run documentation checks and verify the expected failure**

Run:

```bash
go test ./internal/httpapi -run 'TestSpecReview|Test.*Lifecycle.*Docs' -count=1
```

Expected: FAIL until docs contain the contract.

- [x] **Step 3: Document REST, event, and compatibility semantics**

Add endpoint examples, every stage/reason table, cursor rules, safe evidence,
historical reconstruction, and the event-to-transition mapping. State exactly:
“delivered means the recipient mail server accepted the message; e2a does not
observe or claim inbox placement.” Keep screening events documented in their
existing section but explicitly outside the lifecycle ledger.

- [x] **Step 4: Run focused docs and contract checks**

Run the command from Step 2 plus:

```bash
make spec-check
make generate-sdk-check
```

Expected: PASS.

- [ ] **Step 5: Run complete risk-proportionate verification**

Run exactly:

```bash
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' GOFLAGS=-p=1 make test-unit
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' go test -tags integration -p 1 ./internal/messagelifecycle ./internal/relay ./internal/identity ./internal/agent ./internal/outboundsend ./internal/delivery ./internal/webhookpub ./internal/ws ./internal/httpapi ./internal/apiserver
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' GOFLAGS=-p=1 make cover-check
make spec-check
make generate-sdk-check
make openapi-compat-check
npm test --workspace @e2a/sdk
npm run build --workspace @e2a/sdk
npm test --workspace @e2a/cli
npm run build --workspace @e2a/cli
npm test --workspace @e2a/mcp-server
npm run build --workspace @e2a/mcp-server
(cd sdks/python && pytest tests/ -q && mypy)
(cd web && npm test && npm run lint && npm run build)
```

Expected: every command PASS. If a command fails, diagnose and fix the root
cause, then rerun that command and every downstream freshness check affected by
the fix.

Then run both generated-client contract suites against one private contract
server, following the CI harness:

```bash
go build -o /tmp/e2a-contract-server-lifecycle ./cmd/e2a-contract-server
envfile="$(mktemp)"
logfile="$(mktemp)"
E2A_TEST_DATABASE_URL='postgres://e2a:e2a@localhost:5433/e2a_test_trust_ledger_a75f?sslmode=disable' \
  /tmp/e2a-contract-server-lifecycle -env-file "$envfile" >"$logfile" 2>&1 &
server_pid=$!
for _ in {1..150}; do test -s "$envfile" && break; kill -0 "$server_pid" 2>/dev/null || break; sleep 0.2; done
test -s "$envfile"
set -a
. "$envfile"
set +a
for _ in {1..20}; do curl -fsS "$E2A_TEST_BASE_URL/api/health" >/dev/null && break; sleep 0.2; done
npm run test:contract --workspace @e2a/sdk
(cd sdks/python && pytest tests/test_contract.py -v)
kill "$server_pid"
wait "$server_pid" 2>/dev/null || true
```

Expected: TypeScript and Python contract suites PASS. If either fails, print
`$logfile`, stop the server, fix the contract mismatch, and rerun both suites.

- [ ] **Step 6: Review final diff for generated/manual boundaries and scope**

Run:

```bash
git status --short
git diff --check
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
```

Expected: only lifecycle, mapped event, required client, generated SDK/spec,
and documentation files; no screening implementation, analytics, UI, or
unrelated changes.

- [ ] **Step 7: Commit documentation and verification fixes**

```bash
git add docs/api.md docs/events.md docs/superpowers/plans/2026-07-21-message-trust-ledger.md
git commit -m "docs(lifecycle): publish diagnostic contract"
```

- [ ] **Step 8: Finish the branch**

Use `superpowers:requesting-code-review`, address findings with TDD, then use
`superpowers:verification-before-completion` and
`superpowers:finishing-a-development-branch`. Push
`codex/message-trust-ledger` and create a ready PR only after all required
checks pass. Record the final commit hash and PR URL for handoff.
