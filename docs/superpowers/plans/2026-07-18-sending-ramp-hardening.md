# Sending Ramp Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden PR #545 so daily caps remain pre-send while ramp progression requires meaningful provider-accepted volume, retries and deferrals are bounded, and state is operationally maintainable.

**Architecture:** `sendramp.Store` owns a one-row-per-message reservation lifecycle and separate reserved/confirmed daily counts. The outbound worker reserves before SMTP, confirms after durable `sent`, releases definitive failures, and repairs confirmation on re-drive. A River maintenance registrar prunes historical rows, while the public API remains read-only.

**Tech Stack:** Go 1.26, PostgreSQL 16/pgx, River jobs, GitHub Actions, existing e2a migration/test helpers.

---

## File map

- Modify `migrations/067_domain_sending_ramp.sql`: hardened counters, reservation lifecycle, validation, and prune indexes.
- Modify `internal/sendramp/schedule.go`: production-safe minimum and qualification helper.
- Modify `internal/sendramp/store.go`: reserve/move/confirm/release/sweep state machine and permanent error type.
- Modify `internal/sendramp/store_test.go`: database lifecycle, migration, retention, and concurrency regressions.
- Modify `internal/sendramp/schedule_test.go`: minimum and threshold behavior.
- Create `internal/sendramp/maintenance.go`: daily River janitor registrar.
- Create `internal/sendramp/maintenance_test.go`: worker and queue-registration tests.
- Modify `internal/outboundsend/worker.go`: ramp lifecycle interface, permanent errors, horizon, and repair flow.
- Modify `internal/outboundsend/worker_test.go`: worker red/green regressions.
- Modify `internal/agent/outbound_async.go`: adapter lifecycle and injectable UTC clock.
- Create `internal/agent/outbound_ramp_test.go`: real adapter integration tests.
- Modify `cmd/e2a/main.go`: register sendramp maintenance.
- Modify `Makefile`, `.testcoverage.yml`, `.github/workflows/test.yml`: integration, coverage, and targeted race gates.
- Create `docs/runbooks/sending-ramp.md`: rollout, monitoring, and operator-only reset transaction.
- Modify `config.example.yaml`: clarify provider-accepted utilization semantics.

### Task 1: Harden schedule and schema invariants

**Files:**
- Modify: `internal/sendramp/schedule_test.go`
- Modify: `internal/sendramp/schedule.go`
- Modify: `migrations/067_domain_sending_ramp.sql`

- [ ] **Step 1: Write failing schedule tests**

Add assertions that invalid schedules normalize to `50/50/1` and that
`Qualifies(24, 50)` is false while `Qualifies(25, 50)` is true.

```go
func TestNewScheduleEnforcesProductionMinimum(t *testing.T) {
    got := sendramp.NewSchedule(0, -1, 0)
    if got != (sendramp.Schedule{StartDaily: 50, TargetDaily: 50, RampDays: 1}) {
        t.Fatalf("schedule = %+v", got)
    }
}

func TestQualifiesRequiresHalfRoundedUp(t *testing.T) {
    if sendramp.Qualifies(24, 50) || !sendramp.Qualifies(25, 50) ||
        sendramp.Qualifies(25, 51) || !sendramp.Qualifies(26, 51) {
        t.Fatal("qualification threshold is not ceil(limit/2)")
    }
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test ./internal/sendramp -run 'Test(NewScheduleEnforcesProductionMinimum|QualifiesRequiresHalfRoundedUp)' -count=1`

Expected: FAIL because `Qualifies` is undefined and the old minimum is one.

- [ ] **Step 3: Implement the schedule minimum and helper**

```go
const MinimumStartDaily = 50

func NewSchedule(startDaily, targetDaily, rampDays int) Schedule {
    if startDaily < MinimumStartDaily { startDaily = MinimumStartDaily }
    if targetDaily < startDaily { targetDaily = startDaily }
    if rampDays < 1 { rampDays = 1 }
    return Schedule{StartDaily: startDaily, TargetDaily: targetDaily, RampDays: rampDays}
}

func Qualifies(confirmed, limit int) bool {
    return limit >= MinimumStartDaily && confirmed >= (limit+1)/2
}
```

Update migration 067 so scopes require `start_daily >= 50`; counters contain
`reserved_count`, `confirmed_count`, and `daily_limit`; reservations use
`PRIMARY KEY(message_id)`, a checked lifecycle state, `day`, `units`, and
`updated_at`. Add `domain_send_counters(day)` and
`sending_ramp_reservations(state, updated_at)` indexes; remove the unused
scope/day index.

- [ ] **Step 4: Run focused tests and schema checks**

Run: `go test ./internal/sendramp -run 'Test(NewSchedule|Schedule|Qualifies)' -count=1 && git diff --check`

Expected: PASS and no whitespace errors.

- [ ] **Step 5: Commit**

```bash
git add internal/sendramp/schedule.go internal/sendramp/schedule_test.go migrations/067_domain_sending_ramp.sql
git commit -m "fix: harden sending ramp schema invariants"
```

### Task 2: Implement reservation, confirmation, and release lifecycle

**Files:**
- Modify: `internal/sendramp/store_test.go`
- Modify: `internal/sendramp/store.go`

- [ ] **Step 1: Replace presence-based tests with failing lifecycle tests**

Add tests named:

```go
func TestReserveCrossDayMovesPendingCapacity(t *testing.T) {}
func TestConfirmQualifiesOnlyAtHalfAcceptedVolume(t *testing.T) {}
func TestReleaseReturnsPendingCapacityWithoutProgress(t *testing.T) {}
func TestFinalQualifiedDayCompletesOnlyOnLaterDay(t *testing.T) {}
func TestConcurrentSiblingReservationsNeverExceedSharedCap(t *testing.T) {}
func TestPermanentValidationErrors(t *testing.T) {}
```

Each test must query both tables. The cross-day test asserts the old counter's
reserved count returns to zero and only one reservation row exists. The
qualification test reserves 25 one-recipient messages at a 50 cap, confirms 24
without progression, then confirms the 25th and observes one qualified day.
The release test confirms that a released message no longer consumes capacity
and cannot advance the scope.

- [ ] **Step 2: Run lifecycle tests and verify RED**

Run with a fresh database:

```bash
E2A_TEST_DATABASE_URL="$E2A_RAMP_TEST_URL" go test ./internal/sendramp -run 'Test(ReserveCrossDay|ConfirmQualifies|ReleaseReturns|FinalQualified|ConcurrentSibling|PermanentValidation)' -count=1
```

Expected: FAIL because `Confirm`, `Release`, the new columns, and permanent error classification do not exist.

- [ ] **Step 3: Implement typed permanent errors and lifecycle methods**

Expose this narrow contract:

```go
type PermanentError struct{ Err error }
func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }
func (e *PermanentError) Permanent() bool { return true }

func (s *Store) Reserve(ctx context.Context, req ReserveRequest) (Decision, error)
func (s *Store) Confirm(ctx context.Context, messageID string) error
func (s *Store) Release(ctx context.Context, messageID string) error
```

`Reserve` locks the domain and shared scope, promotes a completed scope only on
a UTC day after `last_qualified_day`, moves stale pending reservations by
debiting the old counter, and uses the daily counter's `reserved_count` for the
cap. `Confirm` locks the reservation and scope, increments `confirmed_count`
once, and advances `active_days` only when `Qualifies` first becomes true for
that day. `Release` decrements `reserved_count` only for a `reserved` row and
marks it released. All transitions are transactional and idempotent.

- [ ] **Step 4: Run focused and package tests**

Run: `E2A_TEST_DATABASE_URL="$E2A_RAMP_TEST_URL" go test ./internal/sendramp -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sendramp/store.go internal/sendramp/store_test.go
git commit -m "fix: progress sending ramp from confirmed volume"
```

### Task 3: Integrate the lifecycle with the outbound worker

**Files:**
- Modify: `internal/outboundsend/worker_test.go`
- Modify: `internal/outboundsend/worker.go`
- Modify: `internal/agent/outbound_async.go`
- Create: `internal/agent/outbound_ramp_test.go`

- [ ] **Step 1: Write failing worker tests**

Extend the fake ramp with calls for `Confirm` and `Release`, then add tests that
assert:

```go
func TestWorkConfirmsRampAfterMarkSent(t *testing.T) {}
func TestWorkRepairsRampConfirmationForAlreadySentMessage(t *testing.T) {}
func TestWorkReleasesRampOnPermanentProviderFailure(t *testing.T) {}
func TestWorkRetainsRampOnAmbiguousFailure(t *testing.T) {}
func TestWorkFailsPermanentRampInvariant(t *testing.T) {}
func TestWorkFailsRampDeferredMessagePastHorizon(t *testing.T) {}
```

The fake store records call order so success must be `MarkSent`, then `Confirm`.
The already-sent test requires `Confirm` without a deliverer call. The timeout
test uses `AcceptedAt = time.Now().Add(-73*time.Hour)` and expects a local failed
outcome plus reservation release.

- [ ] **Step 2: Run worker tests and verify RED**

Run: `go test ./internal/outboundsend -run 'TestWork(ConfirmsRamp|RepairsRamp|ReleasesRamp|RetainsRamp|FailsPermanentRamp|FailsRampDeferred)' -count=1`

Expected: FAIL because the `RampGate` lifecycle methods and horizon branches are absent.

- [ ] **Step 3: Implement worker lifecycle and adapter**

Change the interface to:

```go
type RampGate interface {
    Reserve(context.Context, RampRequest) (RampDecision, error)
    Confirm(context.Context, string) error
    Release(context.Context, string) error
}
```

Add `eligibleForRamp`, `confirmRamp`, and `releaseRamp` helpers. Confirm after
`MarkSent`; on `alreadyDone` confirm before returning. Release on suppression,
permanent provider failure, terminal ramp timeout, and other definitive local
terminal branches. Detect errors implementing `interface{ Permanent() bool }`
and terminally fail them. Apply the existing 72-hour horizon to denied and
transient-error ramp snoozes.

In `outboundRampGate`, delegate Confirm/Release to the store. Replace direct
`time.Now()` with a `now func() time.Time` field initialized to `time.Now` and a
package-private test constructor.

- [ ] **Step 4: Add and run real adapter tests**

`internal/agent/outbound_ramp_test.go` must use Postgres and assert that disabled
reserve persists exemption, enabled reserve uses the injected UTC day, and
Confirm/Release reach the real store.

Run: `E2A_TEST_DATABASE_URL="$E2A_RAMP_TEST_URL" go test ./internal/outboundsend ./internal/agent -run 'Ramp|ramp' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/outboundsend/worker.go internal/outboundsend/worker_test.go internal/agent/outbound_async.go internal/agent/outbound_ramp_test.go
git commit -m "fix: bound and confirm ramped outbound sends"
```

### Task 4: Add retention maintenance and migration regression coverage

**Files:**
- Create: `internal/sendramp/maintenance.go`
- Create: `internal/sendramp/maintenance_test.go`
- Modify: `internal/sendramp/store_test.go`
- Modify: `cmd/e2a/main.go`

- [ ] **Step 1: Write failing retention, registrar, and migration tests**

Add `TestSweepPrunesHistoricalRowsOnly`,
`TestMaintenanceJobsRegistersDailyMaintenancePeriodic`, and
`TestMigration067ExemptsOnlyPreexistingVerifiedDomains`. The migration test
loads `migrations/067_domain_sending_ramp.sql`, executes it inside a transaction
after seeding verified and unverified rows, asserts only the verified row is
exempt, and rolls the transaction back.

- [ ] **Step 2: Run tests and verify RED**

Run: `E2A_TEST_DATABASE_URL="$E2A_RAMP_TEST_URL" go test ./internal/sendramp -run 'Test(Sweep|Maintenance|Migration067)' -count=1`

Expected: FAIL because sweep and registrar types are absent.

- [ ] **Step 3: Implement sweep and River registration**

```go
const maintenanceInterval = 24 * time.Hour

func (s *Store) Sweep(ctx context.Context, now time.Time) error

type MaintenanceArgs struct{}
func (MaintenanceArgs) Kind() string { return "sending_ramp_maintenance" }

type MaintenanceJobs struct{ store *Store }
func NewMaintenanceJobs(store *Store) *MaintenanceJobs
func (m *MaintenanceJobs) RegisterJobs(*river.Workers) []*river.PeriodicJob
```

`Sweep` deletes counters with `day < utcDay(now).AddDate(0,0,-35)` and terminal
reservations with `updated_at < now.Add(-7*24*time.Hour)`. Register the worker on
`jobs.QueueMaintenance`, `RunOnStart:false`. Construct one shared ramp store in
`main`, use it for the gate, and append its maintenance registrar.

- [ ] **Step 4: Run package and command tests**

Run: `E2A_TEST_DATABASE_URL="$E2A_RAMP_TEST_URL" go test ./internal/sendramp ./cmd/e2a -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sendramp/maintenance.go internal/sendramp/maintenance_test.go internal/sendramp/store_test.go cmd/e2a/main.go
git commit -m "feat: prune historical sending ramp state"
```

### Task 5: Document operator rollout and reset

**Files:**
- Create: `docs/runbooks/sending-ramp.md`
- Modify: `config.example.yaml`

- [ ] **Step 1: Write the runbook**

Document provider-accepted 50% progression, 72-hour terminal timeout, metrics/log
signals, persistent exemption semantics, and a transaction that first previews
tenant/scope rows, then deletes reservations and counters, deletes the scope,
and changes only matching tenant domains from `exempt` to `inactive`. Require an
explicit organizational-domain scope and include `ROLLBACK` before the operator
chooses `COMMIT`.

- [ ] **Step 2: Update example config language**

Replace “days where that domain actually sends” with “UTC days reaching at
least 50% provider-accepted recipient utilization; higher limits begin on the
next UTC day.” State that the exemption reset is operator-only and link the
runbook.

- [ ] **Step 3: Verify docs and commit**

Run: `scripts/check-repository-text-integrity.sh && git diff --check`

Expected: PASS.

```bash
git add docs/runbooks/sending-ramp.md config.example.yaml
git commit -m "docs: add sending ramp operations runbook"
```

### Task 6: Strengthen CI ownership

**Files:**
- Modify: `Makefile`
- Modify: `.testcoverage.yml`
- Modify: `.github/workflows/test.yml`

- [ ] **Step 1: Add sendramp to the integration target**

Append `./internal/sendramp/` to `test-integration`.

- [ ] **Step 2: Measure and set a non-regressive coverage floor**

Run `make cover`, inspect `go tool cover -func=cover.out | rg 'internal/sendramp'`,
and add an override rounded down to the nearest whole percentage point, never
below 70.

- [ ] **Step 3: Add targeted race execution**

In the Postgres-backed coverage job, add:

```yaml
- name: Race-check ramp and outbound worker
  env:
    E2A_TEST_DATABASE_URL: postgres://e2a:e2a@localhost:5433/e2a_test?sslmode=disable
  run: go test -race -p 1 ./internal/sendramp ./internal/outboundsend
```

- [ ] **Step 4: Run local equivalents and commit**

Run: `E2A_TEST_DATABASE_URL="$E2A_RAMP_TEST_URL" go test -race -p 1 ./internal/sendramp ./internal/outboundsend -count=1`

Expected: PASS with no race reports.

```bash
git add Makefile .testcoverage.yml .github/workflows/test.yml
git commit -m "ci: gate sending ramp integration and races"
```

### Task 7: Full review, verification, and PR update

**Files:** all files changed above.

- [ ] **Step 1: Review against the approved spec**

Check every design requirement, run `git diff origin/main...HEAD`, and correct
design drift or incomplete error paths test-first.

- [ ] **Step 2: Run fresh full verification**

Run, against an isolated PostgreSQL database:

```bash
go test -p 1 -count=1 ./...
go test -race -p 1 ./internal/sendramp ./internal/outboundsend
go build ./cmd/e2a
make spec-check
make openapi-compat-check
make generate-sdk-check
```

Then run TypeScript build/tests/type tests and Python mypy/tests using the
repository's package commands. Expected: all affected checks pass; any unrelated
existing failure is reproduced on `origin/main` and documented rather than
hidden.

- [ ] **Step 3: Confirm clean generation and worktree**

Run: `git diff --check && git status --short && git log --oneline origin/main..HEAD`

Expected: no uncommitted files and coherent slice commits.

- [ ] **Step 4: Push and update PR #545**

```bash
git push origin codex/domain-sending-ramp
gh pr view 545 --json url,state,isDraft,mergeable,mergeStateStatus,statusCheckRollup
```

Expected: PR remains open, draft, and mergeable; CI starts for the new head.
