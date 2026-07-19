# Per-Agent Managed Unsubscribe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add e2a-managed one-click unsubscribe whose enforcement is scoped to the exact sending agent and recipient.

**Architecture:** Keep account-wide bounce/complaint suppressions unchanged and add separate agent-scoped consent and opaque-token tables. Extend all outbound request shapes with `unsubscribe: {mode:"managed"}`, compose the footer and RFC 8058 headers only after final recipient resolution, and expose a scanner-safe public confirmation flow plus authenticated management APIs. Every outbound gate computes the union of account and exact-agent suppressions, with a final worker check before provider I/O.

**Tech Stack:** Go, chi/Huma HTTP APIs, PostgreSQL/pgx, River async jobs, go-msgauth DKIM, OpenAPI Generator, TypeScript and Python SDKs.

---

### Task 1: Agent suppression and token persistence

**Files:**
- Create: `migrations/068_per_agent_suppressions.sql`
- Create: `internal/identity/agent_suppressions.go`
- Create: `internal/identity/agent_suppressions_test.go`
- Modify: `internal/identity/user_data_rights.go`
- Test: `internal/identity/user_data_rights_test.go`

- [ ] **Step 1: Write failing store tests**

Cover duplicate insertion, account/agent isolation, overlap with account rows, exact-address normalization, keyset pagination, deletion, persistence after agent deletion/recreation, effective lookup, token-hash lookup, and concurrent token mapping insertion. Use these public signatures in the tests:

```go
func (s *Store) AddAgentSuppression(ctx context.Context, userID, agentID, address, reason, source string, onAdded AgentSuppressionTxHook) (AgentSuppression, bool, error)
func (s *Store) ListAgentSuppressions(ctx context.Context, userID, agentID string, limit int, after time.Time, afterAddress string) ([]AgentSuppression, error)
func (s *Store) RemoveAgentSuppression(ctx context.Context, userID, agentID, address string) (bool, error)
func (s *Store) EffectiveSuppressions(ctx context.Context, userID, agentID string, addresses []string) ([]string, error)
func (s *Store) PutUnsubscribeToken(ctx context.Context, tokenHash []byte, userID, agentID, address string) error
func (s *Store) ResolveUnsubscribeToken(ctx context.Context, tokenHash []byte) (*UnsubscribeScope, error)
```

- [ ] **Step 2: Run the failing tests**

Run: `go test ./internal/identity -run 'TestAgentSuppression|TestUnsubscribeToken|TestUserExportAgentSuppression' -count=1`

Expected: FAIL because the migration, types, and methods do not exist.

- [ ] **Step 3: Add the schema and minimal store implementation**

Create the two tables and indexes exactly as specified in `docs/superpowers/specs/2026-07-18-per-agent-suppressions-design.md`. Implement `AgentSuppression` with `AgentEmail`, `Address`, `Reason`, `Source`, and `CreatedAt`; normalize addresses on writes; bind every query to `user_id`; use `ON CONFLICT (user_id,agent_id,address) DO NOTHING`; and implement effective lookup as:

```sql
SELECT address FROM suppressions
 WHERE user_id = $1 AND address = ANY($2)
UNION
SELECT address FROM agent_suppressions
 WHERE user_id = $1 AND agent_id = $3 AND address = ANY($2)
```

Hash mappings use `ON CONFLICT (token_hash) DO NOTHING`. Run `onAdded` inside the insertion transaction only when the row is new, allowing the event outbox write to commit atomically with consent while duplicate requests remain event-free. Export agent rows in the existing `suppressions` collection with optional `agent_email`; never export token mappings.

- [ ] **Step 4: Run store and export tests**

Run: `go test ./internal/identity -run 'TestAgentSuppression|TestUnsubscribeToken|TestUserExport' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the persistence slice**

```bash
git add migrations/068_per_agent_suppressions.sql internal/identity/agent_suppressions.go internal/identity/agent_suppressions_test.go internal/identity/user_data_rights.go internal/identity/user_data_rights_test.go
git commit -m "feat: persist per-agent suppressions"
```

### Task 2: Opaque managed-unsubscribe tokens

**Files:**
- Create: `internal/unsubscribe/token.go`
- Create: `internal/unsubscribe/token_test.go`

- [ ] **Step 1: Write token tests**

Pin deterministic output for the same normalized tuple and secret, distinct output across each tuple component, a `u1_` version prefix, 256-bit entropy, and SHA-256 lookup hashing. The API is:

```go
func Derive(secret, userID, agentID, recipient string) (string, error)
func Hash(token string) []byte
```

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./internal/unsubscribe -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement versioned HMAC derivation**

Use HMAC-SHA256 over length-prefixed UTF-8 fields so concatenation is unambiguous. Require a non-empty secret and normalized, non-empty identifiers. Encode the digest with unpadded base64url and prepend `u1_`. Never log inputs or token values.

- [ ] **Step 4: Verify and commit**

Run: `go test ./internal/unsubscribe -count=1`

Expected: PASS.

```bash
git add internal/unsubscribe
git commit -m "feat: derive opaque unsubscribe tokens"
```

### Task 3: Authenticated agent suppression API

**Files:**
- Create: `internal/httpapi/agent_suppressions.go`
- Create: `internal/httpapi/agent_suppressions_test.go`
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/apiserver/apiserver.go`

- [ ] **Step 1: Write failing HTTP tests**

Cover account-only authorization, owned/missing/foreign agent non-enumeration, normalized idempotent create, list pagination with an agent-bound signed cursor, exact delete with `confirm=DELETE`, and proof that account endpoints do not read or remove agent rows.

- [ ] **Step 2: Run the tests and observe missing routes**

Run: `go test ./internal/httpapi -run 'TestAgentSuppressions' -count=1`

Expected: FAIL with route-not-found or missing dependency errors.

- [ ] **Step 3: Register and implement the operations**

Add account-scoped dependencies matching the Task 1 methods and register:

```text
GET    /v1/agents/{email}/suppressions
POST   /v1/agents/{email}/suppressions
DELETE /v1/agents/{email}/suppressions/{address}?confirm=DELETE
```

The POST body is `{address,reason?}` and always stores `source=manual`. Return a self-describing resource `{agent_email,address,reason?,source,created_at}`. Both a new row and an existing row return 200. The list returns `{items,next_cursor}`; include the normalized agent address in the cursor MAC payload.

- [ ] **Step 4: Run HTTP tests and commit**

Run: `go test ./internal/httpapi -run 'TestAgentSuppressions' -count=1`

Expected: PASS.

```bash
git add internal/httpapi/agent_suppressions.go internal/httpapi/agent_suppressions_test.go internal/httpapi/httpapi.go internal/apiserver/apiserver.go
git commit -m "feat: expose agent suppression management"
```

### Task 4: Public one-click unsubscribe flow

**Files:**
- Create: `internal/httpapi/unsubscribe.go`
- Create: `internal/httpapi/unsubscribe_test.go`
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/apiserver/apiserver.go`

- [ ] **Step 1: Write scanner-safety and idempotency tests**

Test that `GET /u/{token}` renders confirmation without mutation; browser confirmation POST and `application/x-www-form-urlencoded` body `List-Unsubscribe=One-Click` insert the exact agent row; duplicate and concurrent POSTs return 200 without duplicate logical insertion; malformed/unknown tokens return generic 404; and every response sends `Cache-Control: no-store` plus a restrictive CSP.

- [ ] **Step 2: Run the failing public-route tests**

Run: `go test ./internal/httpapi -run 'TestPublicUnsubscribe' -count=1`

Expected: FAIL because `/u/{token}` is unregistered.

- [ ] **Step 3: Implement the raw chi route**

Register the route outside authenticated `/v1`. Hash the path token, resolve its stored scope, and make GET read-only. POST accepts only the RFC body or the server-rendered confirmation form and calls `AddAgentSuppression(...,"","unsubscribe", eventHook)`. Return a minimal escaped HTML success page or an empty 200 for RFC one-click; do not redirect, set cookies, or reveal the recipient in error responses.

- [ ] **Step 4: Run tests and commit**

Run: `go test ./internal/httpapi -run 'TestPublicUnsubscribe' -count=1`

Expected: PASS.

```bash
git add internal/httpapi/unsubscribe.go internal/httpapi/unsubscribe_test.go internal/httpapi/httpapi.go internal/apiserver/apiserver.go
git commit -m "feat: host one-click unsubscribe flow"
```

### Task 5: Outbound request contract and MIME composition

**Files:**
- Modify: `internal/httpapi/outbound.go`
- Modify: `internal/httpapi/outbound_test.go`
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/apiserver/apiserver.go`
- Modify: `internal/outbound/sender.go`
- Modify: `internal/outbound/compose.go`
- Modify: `internal/outbound/sender_test.go`
- Modify: `internal/dkim/dkim.go`
- Modify: `internal/dkim/dkim_test.go`
- Modify: `internal/agent/hitl_api.go`
- Modify: `internal/agent/hitl_magic_api.go`

- [ ] **Step 1: Write contract and composition tests**

Define and test this request type on send, reply, and forward:

```go
type UnsubscribeOptions struct {
    Mode    string `json:"mode" enum:"managed"`
    Present bool   `json:"-"`
}
// Each HTTP request shape uses UnsubscribeOptions `json:"unsubscribe,omitempty"`.
// Its UnmarshalJSON sets Present, rejects null, disallows unknown fields,
// requires mode, and accepts only "managed". The handler maps Present=true
// to an internal *outbound.UnsubscribeOptions.
```

Tests must reject `null`, `{}`, unknown modes, unknown object fields, and managed messages with zero or multiple normalized envelope recipients. Omission must preserve existing raw bytes. Managed literal, template, attachment, reply, forward, and post-HITL messages must include the visible text/HTML footer and exactly these headers:

```text
List-Unsubscribe: <https://api.e2a.dev/u/{token}>
List-Unsubscribe-Post: List-Unsubscribe=One-Click
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./internal/httpapi ./internal/outbound ./internal/dkim ./internal/agent -run 'Test.*Unsubscribe' -count=1`

Expected: FAIL because request propagation and MIME support are absent.

- [ ] **Step 3: Implement final-recipient token binding**

Add the presence-aware value to HTTP requests and `Unsubscribe *UnsubscribeOptions` to internal outbound requests. Inject a narrow token provider through `httpapi.Deps`/`apiserver.Params` that derives and persists the mapping only after templates and final recipient overrides resolve. Append escaped footer content before MIME encoding; inject both headers before signing. Add `List-Unsubscribe` and `List-Unsubscribe-Post` to `dkim.Sign`'s `HeaderKeys`. Persist unsubscribe intent through pending review and regenerate the final request after reviewer overrides; do not mint a token for a held draft before final recipients are known.

- [ ] **Step 4: Run focused tests and commit**

Run: `go test ./internal/httpapi ./internal/outbound ./internal/dkim ./internal/agent -run 'Test.*Unsubscribe' -count=1`

Expected: PASS.

```bash
git add internal/httpapi/outbound.go internal/httpapi/outbound_test.go internal/httpapi/httpapi.go internal/apiserver/apiserver.go internal/outbound/sender.go internal/outbound/compose.go internal/outbound/sender_test.go internal/dkim/dkim.go internal/dkim/dkim_test.go internal/agent/hitl_api.go internal/agent/hitl_magic_api.go
git commit -m "feat: compose managed unsubscribe messages"
```

### Task 6: Effective suppression enforcement and event

**Files:**
- Modify: `internal/agent/suppression.go`
- Modify: `internal/agent/suppression_internal_test.go`
- Modify: `internal/agent/outbound_suppression_guard_test.go`
- Modify: `internal/outboundsend/worker.go`
- Modify: `internal/outboundsend/worker_test.go`
- Modify: `internal/identity/delivery_store.go`
- Modify: `internal/webhookpub/event.go`
- Modify: `internal/eventpayload/payloads.go`
- Modify: `internal/eventpayload/catalog.go`
- Modify: `internal/eventpayload/golden_test.go`
- Modify: `internal/agent/events_api.go`
- Modify: `internal/httpapi/webhooks.go`
- Modify: `internal/httpapi/event_enum_drift_test.go`
- Modify: `internal/httpapi/stability_test.go`
- Create: `internal/eventpayload/testdata/agent.suppression_added.json`

- [ ] **Step 1: Write failing enforcement and event tests**

Cover direct, reply, forward, human approval, magic-link approval, TTL approval, test send, and final worker behavior. Assert same-agent refusal, sibling-agent allowance, account-wide refusal across agents, To/CC/BCC coverage, deduplicated errors, and a suppression inserted after acceptance preventing provider I/O. Assert one beta `agent.suppression_added` event only on the first insertion with `{agent_email,address,source}`.

- [ ] **Step 2: Run the failing tests**

Run: `go test ./internal/agent ./internal/outboundsend ./internal/eventpayload -run 'Test.*(Suppression|Unsubscribe)' -count=1`

Expected: FAIL because enforcement is account-only and the event is absent.

- [ ] **Step 3: Thread the exact agent through every gate**

Change suppression checks to accept `userID, agentID, recipients`. Add `AgentID` to `identity.OutboundSendPayload` and `outboundsend.SendJob`, load it from `messages.agent_id`, and call `EffectiveSuppressions` immediately before provider submission. Preserve current fail-open-at-accept/fail-closed-at-approval-and-worker behavior. Add `EventAgentSuppressionAdded` to `AllEventTypes` and `ExperimentalEventTypes`, define its typed payload/golden fixture, extend event-enum discovery to recognize the `agent.` namespace, and enqueue the event through Task 1's transactional hook only when insertion reports `added=true`.

- [ ] **Step 4: Run enforcement, event, and regression tests**

Run: `go test ./internal/agent ./internal/outboundsend ./internal/eventpayload ./internal/webhookpub -count=1`

Expected: PASS.

- [ ] **Step 5: Commit enforcement**

```bash
git add internal/agent internal/outboundsend internal/identity/delivery_store.go internal/webhookpub/event.go internal/eventpayload internal/httpapi/webhooks.go internal/httpapi/event_enum_drift_test.go internal/httpapi/stability_test.go
git commit -m "feat: enforce agent-scoped unsubscribes"
```

### Task 7: OpenAPI, SDKs, documentation, and full verification

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `sdks/typescript/src/v1/generated/**`
- Modify: `sdks/python/src/e2a/v1/generated/**`
- Modify: `sdks/typescript/README.md`
- Modify: `sdks/python/README.md`
- Modify: `docs/api.md`
- Test: `mcp/tests/sdk-shape.test.ts`

- [ ] **Step 1: Generate and inspect the public contract**

Run: `make spec && make generate-sdk`

Expected: OpenAPI contains `UnsubscribeOptions` with required enum `mode=managed`, the three agent suppression operations, the new webhook event, and regenerated TypeScript/Python methods and models.

- [ ] **Step 2: Add SDK-shape and documentation examples**

Add compile-time/type tests showing:

```ts
unsubscribe: { mode: "managed" }
```

and Python construction with `mode="managed"`. Document omission semantics, single-recipient restriction, automatic footer/headers, `422 recipient_suppressed`, scoped management operations, and the beta webhook. Do not describe omission as proof of transactional content.

- [ ] **Step 3: Run contract and SDK verification**

Run: `make spec-check && make openapi-compat-check && make generate-check`

Expected: PASS with no stale generated files or breaking OpenAPI changes.

- [ ] **Step 4: Run focused and repository verification**

Run: `go test -p 1 ./internal/identity ./internal/unsubscribe ./internal/httpapi ./internal/outbound ./internal/dkim ./internal/agent ./internal/outboundsend ./internal/eventpayload ./internal/webhookpub`

Expected: PASS.

Run: `go test -p 1 ./...`

Expected: PASS.

Run: `npm run build`

Expected: PASS.

- [ ] **Step 5: Commit generated contracts and docs**

```bash
git add api/openapi.yaml sdks docs/api.md mcp/tests/sdk-shape.test.ts
git commit -m "docs: publish managed unsubscribe API"
```
