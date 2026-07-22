# Message Lifecycle Beta Markers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mark the complete message-lifecycle feature beta without weakening the stable contract of existing event envelopes or event payload schemas.

**Architecture:** The Huma operation and `applyEvolutionStance` remain the source of truth. The lifecycle operation declares beta at registration, its response-only schema closure inherits beta automatically, and the optional `lifecycle_transitions` properties on otherwise-stable event payload schemas receive explicit property-level markers. Generated OpenAPI and SDK bases are regenerated; handwritten clients and documentation visibly identify the feature as beta.

**Tech Stack:** Go/Huma/OpenAPI 3.1, OpenAPI Generator, TypeScript/Vitest, Python/pytest, Markdown contract tests.

---

### Task 1: Pin and emit authoritative OpenAPI beta metadata

**Files:**
- Modify: `internal/httpapi/stability_test.go`
- Modify: `internal/httpapi/message_lifecycle.go`
- Modify: `internal/httpapi/stability.go`
- Test: `internal/httpapi/stability_test.go`

- [ ] **Step 1: Write the failing contract test**

Add `"get-message-lifecycle"` to `betaOperationIDs`. In `TestSpecBetaMarkers`, assert that the operation summary contains `(beta)`, its description contains `Beta: message lifecycle`, `MessageLifecycleTransition` and `PageMessageLifecycleTransition` carry only `x-stability-level: beta`, every mapped stable event payload's `lifecycle_transitions` property carries only that beta marker, and parent schemas such as `EmailReceivedData` remain unmarked.

```go
for _, name := range []string{"EmailReceivedData", "EmailSentData", "EmailFailedData", "EmailDeliveredData", "EmailBouncedData", "EmailComplainedData", "DomainSuppressionAddedData"} {
	property, _ := schemaProps(t, doc, name)["lifecycle_transitions"].(map[string]any)
	if property == nil || property["x-stability-level"] != "beta" {
		t.Errorf("%s.lifecycle_transitions must carry canonical x-stability-level: beta", name)
	}
	if got := schemaExt(name, "x-stability-level"); got != nil {
		t.Errorf("stable parent schema %s must remain unmarked, got %v", name, got)
	}
}
```

- [ ] **Step 2: Run the test and verify the expected failure**

Run: `go test ./internal/httpapi -run 'TestSpecBetaMarkers' -count=1`

Expected: FAIL because `get-message-lifecycle`, its response schemas, and the event properties do not yet carry the beta marker.

- [ ] **Step 3: Implement the smallest source-of-truth change**

Define one shared lifecycle beta sentence in `message_lifecycle.go`, update the operation summary/description, and add `Extensions: beta()`:

```go
const messageLifecycleBetaDoc = "Beta: message lifecycle may change before it is declared stable."

Summary:     "Get a message's lifecycle (beta)",
Description: "... " + messageLifecycleBetaDoc,
Extensions:  beta(),
```

In `applyEvolutionStance`, mark only the mapped payload properties:

```go
for _, schema := range []string{"EmailReceivedData", "EmailSentData", "EmailFailedData", "EmailDeliveredData", "EmailBouncedData", "EmailComplainedData", "DomainSuppressionAddedData"} {
	markProperty(schemas, schema, "lifecycle_transitions", extStabilityLevel, stabilityBeta)
}
```

Rely on beta-operation reachability to mark `PageMessageLifecycleTransition` and `MessageLifecycleTransition`; do not manually stamp generated YAML or generated SDK files.

- [ ] **Step 4: Run focused Go verification**

Run: `go test ./internal/httpapi -run 'TestSpecBetaMarkers|TestMessageLifecycleOpenAPIOperationAndEnums|TestStableEmailPayloadsUseCanonicalLifecycleComponent' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the authoritative contract slice**

```bash
git add internal/httpapi/stability_test.go internal/httpapi/message_lifecycle.go internal/httpapi/stability.go
git commit -m "feat(api): mark message lifecycle beta"
```

### Task 2: Publish beta status across documentation and client surfaces

**Files:**
- Modify: `docs/api.md`
- Modify: `docs/events.md`
- Modify: `internal/httpapi/message_lifecycle_docs_test.go`
- Modify: `sdks/typescript/src/v1/client.ts`
- Modify: `sdks/python/src/e2a/v1/client.py`
- Modify: `cli/src/bin/e2a.ts`
- Modify: `cli/src/commands/messages.ts`
- Modify: `mcp/src/client.ts`
- Modify: `mcp/src/tools/messages.ts`
- Modify: `web/src/lib/messageLifecycle.ts`
- Regenerate: `api/openapi.yaml`
- Regenerate: `sdks/typescript/src/v1/generated/`
- Regenerate: `sdks/python/src/e2a/v1/generated/`
- Test: `internal/httpapi/message_lifecycle_docs_test.go`

- [ ] **Step 1: Write the failing documentation contract test**

Extend `TestMessageLifecycleDocsPublishClosedDiagnosticContract` to require `**Beta:**`, `x-stability-level: beta`, and an explicit statement that stable event envelopes remain stable while only `lifecycle_transitions` is beta.

```go
requireLifecycleDocText(t, api,
	"**Beta:**", "x-stability-level: beta",
	"existing event envelopes remain stable",
)
```

- [ ] **Step 2: Run the documentation test and verify the expected failure**

Run: `go test ./internal/httpapi -run 'TestMessageLifecycleDocsPublishClosedDiagnosticContract' -count=1`

Expected: FAIL because the lifecycle documentation currently describes the feature as additive but not beta.

- [ ] **Step 3: Add visible beta labels to handwritten surfaces**

Add `get-message-lifecycle` to the complete beta operation table in `docs/api.md`; label the lifecycle section and event-field section beta; state that existing event envelopes and event types remain stable. Add concise `Beta:` JSDoc/docstrings/comments to the TS SDK method, Python method, CLI help/usage, MCP client/tool, and web helper. Do not change wire shapes or runtime behavior.

- [ ] **Step 4: Regenerate contract artifacts**

Run: `make generate`

Expected: `api/openapi.yaml` and generated TypeScript/Python SDK bases update from handler metadata; no generated file is hand-edited.

- [ ] **Step 5: Run focused client and documentation tests**

Run:

```bash
go test ./internal/httpapi -run 'TestSpecBetaMarkers|TestMessageLifecycle' -count=1
npm test --workspace @e2a/sdk -- --run
npm test --workspace @e2a/cli -- --run
npm test --workspace @e2a/mcp-server -- --run
(cd sdks/python && pytest tests/ -q)
(cd web && npm test -- --runInBand)
```

Expected: all PASS.

- [ ] **Step 6: Run freshness and risk-proportionate verification**

Run:

```bash
make test-unit
make spec-check
make generate-sdk-check
make openapi-compat-check
npm run build --workspace @e2a/sdk
npm run build --workspace @e2a/cli
npm run build --workspace @e2a/mcp-server
(cd sdks/python && mypy)
(cd web && npm run lint && npm run build)
```

Expected: all PASS; compatibility check reports no stable breaking changes.

- [ ] **Step 7: Commit and publish the completed beta labeling**

```bash
git add docs internal/httpapi api/openapi.yaml sdks cli mcp web
git commit -m "docs(lifecycle): label beta client surfaces"
git push
```

Update the existing pull request; do not create a second PR.
