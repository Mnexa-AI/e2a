# Indefinite Message Retention Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retain all live inbound and outbound message data indefinitely while permanently purging messages and inboxes after 30 days in trash by default.

**Architecture:** Make `messages.expires_at` nullable and define `NULL` as no expiry. Remove natural-expiry predicates and cleanup, preserve all outbound content columns through terminal transitions, and keep `deleted_at` plus `TrashRetention` as the only automatic message-deletion clock. Propagate the nullable timestamp through internal models, OpenAPI, generated SDKs, and documentation.

**Tech Stack:** PostgreSQL migrations, Go 1.25/1.26, Huma/OpenAPI 3.1, TypeScript, Python, npm workspaces, Docker-based OpenAPI Generator.

---

### Task 1: Nullable message-expiry schema and model

**Files:**
- Create: `migrations/072_indefinite_message_retention.sql`
- Modify: `internal/identity/store.go`
- Modify: `internal/identity/user_data_rights.go`
- Test: `internal/identity/store_test.go`

- [ ] **Step 1: Write failing database-backed tests**

Add assertions that a newly created inbound message has a null database value and a nil Go field, and that the migrated schema is nullable with no default:

```go
func TestMessageRetentionIsIndefinite(t *testing.T) {
    store, pool := newTestStore(t)
    msg := createTestInbound(t, store)
    if msg.ExpiresAt != nil {
        t.Fatalf("ExpiresAt = %v, want nil", msg.ExpiresAt)
    }
    var expiresAt *time.Time
    if err := pool.QueryRow(context.Background(),
        `SELECT expires_at FROM messages WHERE id = $1`, msg.ID).Scan(&expiresAt); err != nil {
        t.Fatal(err)
    }
    if expiresAt != nil {
        t.Fatalf("database expires_at = %v, want NULL", expiresAt)
    }
}
```

Also query `information_schema.columns` for `is_nullable = 'YES'` and
`column_default IS NULL`.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test -tags integration -p 1 ./internal/identity -run 'TestMessageRetentionIsIndefinite' -count=1
```

Expected: FAIL because `messages.expires_at` is non-null and `Message.ExpiresAt` is a `time.Time`.

- [ ] **Step 3: Add the idempotent forward migration**

Create migration 072 with:

```sql
ALTER TABLE messages ALTER COLUMN expires_at DROP NOT NULL;
ALTER TABLE messages ALTER COLUMN expires_at DROP DEFAULT;
UPDATE messages SET expires_at = NULL WHERE expires_at IS NOT NULL;
DROP INDEX IF EXISTS idx_messages_expires;
```

- [ ] **Step 4: Make internal message expiry nullable**

Change the internal and export message fields to pointers:

```go
ExpiresAt *time.Time `json:"expires_at" nullable:"true"`
```

Change every message constructor to assign `ExpiresAt: nil`, every insert to
pass nil, and every scan target to accept `*time.Time`.

- [ ] **Step 5: Run the focused tests and verify GREEN**

Run the Task 1 command. Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/072_indefinite_message_retention.sql internal/identity/store.go internal/identity/user_data_rights.go internal/identity/store_test.go
git commit -m "feat(identity): retain messages indefinitely"
```

### Task 2: Remove natural-expiry behavior and preserve trash expiry

**Files:**
- Modify: `internal/identity/store.go`
- Modify: `internal/identity/review.go`
- Modify: `internal/identity/local_delivery.go`
- Modify: any other `internal/identity/*.go` file containing a message-level `expires_at > now()` predicate
- Test: `internal/identity/store_test.go`
- Test: `internal/identity/trash_test.go`

- [ ] **Step 1: Write failing retention and trash tests**

Replace natural-expiry deletion expectations with tests proving a legacy row
whose `expires_at` is in the past remains readable and survives
`DeleteExpiredMessages`, while a row with `deleted_at` older than
`TrashRetention` is deleted:

```go
func TestDeleteExpiredMessagesOnlyPurgesTrash(t *testing.T) {
    // Backdate one live row's expires_at and one trashed row's deleted_at.
    // Run DeleteExpiredMessages.
    // Assert live row exists, trashed row does not, and deleted == 1.
}
```

Add restore assertions that `expires_at` remains nil and a pending hold's
`approval_expires_at` shifts by time spent in trash.

- [ ] **Step 2: Run focused tests and verify RED**

```bash
go test -tags integration -p 1 ./internal/identity -run 'TestDeleteExpiredMessages|TestRestore' -count=1
```

Expected: FAIL because the janitor still deletes live expired rows and restore
still performs arithmetic on `expires_at`.

- [ ] **Step 3: Remove expiry predicates and expiry-clock arithmetic**

Remove message-liveness clauses such as:

```sql
AND expires_at > now()
```

Change the janitor delete predicate to only:

```sql
WHERE m.deleted_at IS NOT NULL
  AND m.deleted_at <= now() - make_interval(secs => $2)
```

Change message restore to `SET deleted_at = NULL`. Change agent restore to
shift only `approval_expires_at` for live pending-review messages.

- [ ] **Step 4: Run focused identity and janitor tests**

```bash
go test -tags integration -p 1 ./internal/identity ./internal/janitor -run 'TestDeleteExpiredMessages|TestRestore|TestJanitor' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/identity internal/janitor
git commit -m "feat(identity): make trash the only message expiry"
```

### Task 3: Preserve outbound bodies and attachments

**Files:**
- Modify: `internal/identity/store.go`
- Modify: `internal/identity/local_delivery.go`
- Modify: `internal/hitlworker/worker.go`
- Test: `internal/identity/hitl_approve_test.go`
- Test: `internal/identity/hitl_expire_test.go`
- Test: `internal/identity/approve_accept_test.go`
- Test: `internal/identity/outbound_retention_test.go`
- Test: `internal/hitlworker/worker_test.go`
- Test: `internal/agent/hitl_magic_api_test.go`
- Test: `internal/httpapi/messages_parsed_test.go`

- [ ] **Step 1: Change terminal-transition tests to require preservation**

For approve, reject, TTL approve, TTL reject, local delivery, and async accept,
assert the original values remain:

```go
if bodyText == nil || *bodyText != "retained text" {
    t.Fatalf("body_text = %v, want retained text", bodyText)
}
if len(attachments) == 0 {
    t.Fatal("attachments_json was cleared")
}
```

- [ ] **Step 2: Run focused tests and verify RED**

```bash
go test -tags integration -p 1 ./internal/identity ./internal/hitlworker ./internal/agent ./internal/httpapi -run 'Approve|Reject|Expire|Retention|Parsed' -count=1
```

Expected: FAIL at assertions observing null body/attachment columns.

- [ ] **Step 3: Stop destructive terminal updates**

Remove these assignments from every outbound terminal update:

```sql
body_text = NULL,
body_html = NULL,
attachments_json = NULL
```

Keep status, review attribution, delivery, raw MIME, and send-attempt behavior
unchanged. Update comments to state that draft data remains retained even when
raw MIME also exists.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the Task 3 command. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/identity internal/hitlworker internal/agent internal/httpapi
git commit -m "feat(outbound): preserve terminal message content"
```

### Task 4: Publish nullable expiry in the API contract and clients

**Files:**
- Modify: Go API/export schema sources that expose `identity.Message`
- Modify: `api/openapi.yaml`
- Modify: `sdks/typescript/src/v1/generated/`
- Modify: `sdks/python/src/e2a/v1/generated/`
- Modify: handwritten TS/Python models or tests that assume a required date
- Test: `internal/httpapi/*_test.go`
- Test: `sdks/typescript/test/`
- Test: `sdks/python/tests/`

- [ ] **Step 1: Add failing schema and serialization tests**

Assert account export JSON contains `"expires_at": null`, and the OpenAPI
`Message.expires_at` schema accepts null while retaining the property:

```go
if got := body["expires_at"]; got != nil {
    t.Fatalf("expires_at = %#v, want null", got)
}
```

- [ ] **Step 2: Run focused tests and verify RED**

```bash
go test ./internal/httpapi -run 'Export|SpecGoldenNoDrift' -count=1
```

Expected: FAIL because the generated contract still declares a required date.

- [ ] **Step 3: Update Huma source types and regenerate contract artifacts**

Use a required nullable pointer field where the property is public:

```go
ExpiresAt *time.Time `json:"expires_at" nullable:"true" format:"date-time" doc:"Message expiry. Null means the message is retained indefinitely."`
```

Then run:

```bash
make generate
```

Commit `api/openapi.yaml` and both generated SDK trees. Update handwritten
types so TypeScript uses `Date | null` and Python uses
`Optional[datetime]` without treating null as a validation error.

- [ ] **Step 4: Verify Go, TS, and Python contracts**

```bash
make spec-check
npm run build --workspace @e2a/sdk
npm test --workspace @e2a/sdk
cd sdks/python && pytest tests/ -v && mypy
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add api internal/httpapi sdks/typescript sdks/python
git commit -m "feat(api): expose indefinite message retention"
```

### Task 5: Update documentation and stale contract language

**Files:**
- Modify: `README.md`
- Modify: `docs/data-handling.md`
- Modify: `docs/api.md`
- Modify: `docs/design/trash-soft-delete.md`
- Modify: `internal/httpapi/messages.go`
- Modify: `internal/httpapi/account.go`
- Modify: relevant comments in `internal/identity/`, `internal/webhookpub/`, and migrations only where they describe current behavior

- [ ] **Step 1: Add or update text-integrity assertions**

Update the repository text-integrity test/guard so current documentation cannot
reintroduce claims that message data expires after 10 days or terminal outbound
bodies are scrubbed.

- [ ] **Step 2: Run the guard and verify RED**

```bash
scripts/check-repository-text-integrity.sh
```

Expected: FAIL while stale retention statements remain.

- [ ] **Step 3: Rewrite current documentation**

State:

```text
Live inbound and outbound message data is retained indefinitely. Soft-deleted
messages and inboxes are permanently purged after 30 days by default. Outbound
body and attachment data remains retained through terminal review and delivery
transitions.
```

Historical design documents that are explicitly snapshots may keep historical
facts, but current API descriptions and operational docs must use the new
policy.

- [ ] **Step 4: Run documentation and spec guards**

```bash
scripts/check-repository-text-integrity.sh
make spec-check
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md docs internal scripts
git commit -m "docs: document indefinite message retention"
```

### Task 6: Full verification

**Files:**
- Modify only files needed to fix regressions caused by this feature

- [ ] **Step 1: Format all changed source files**

```bash
gofmt -w $(git diff --name-only --diff-filter=ACM HEAD~5 -- '*.go')
```

- [ ] **Step 2: Run the Go unit suite**

```bash
make test-unit
```

Expected: PASS.

- [ ] **Step 3: Run database-backed affected packages serially**

```bash
go test -tags integration -p 1 ./internal/identity ./internal/janitor ./internal/hitlworker ./internal/agent ./internal/httpapi
```

Expected: PASS against a dedicated Postgres test database.

- [ ] **Step 4: Run generated-code and client checks**

```bash
make spec-check
make generate-sdk-check
npm test --workspace @e2a/sdk
npm test --workspace @e2a/cli
npm test --workspace @e2a/mcp-server
cd sdks/python && pytest tests/ -v && mypy
```

Expected: PASS and no generated-code drift.

- [ ] **Step 5: Inspect final diff and commit any verification fixes**

```bash
git diff --check
git status --short
```

If verification required fixes, commit them with a narrowly scoped message.
