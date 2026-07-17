# Listen Forward Transport Errors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep listener output and `--once` completion working when the forwarding HTTP request is rejected before receiving a response.

**Architecture:** Normalize transport rejection at the shared `forwardMessage` boundary, where non-2xx failures are already converted to stderr diagnostics. Leave message retrieval and successful response handling unchanged.

**Tech Stack:** TypeScript, Node.js `fetch`, Vitest

---

### Task 1: Lock down transport-failure behavior

**Files:**
- Test: `cli/src/__tests__/listen.test.ts`

- [ ] **Step 1: Add a failing notification regression test**

Add a test that stubs `fetch` to reject with `Error("connection refused")`, calls
`handleNotification` with both `json` and `forward`, and expects JSON on stdout
plus `Forward failed: connection refused` on stderr.

- [ ] **Step 2: Add a failing `--once` regression test**

Mock `createClient` with a one-event async stream, invoke `listen` with `once`,
`json`, and `forward`, and assert one JSON line is written and the stream closes
even though `fetch` rejects.

- [ ] **Step 3: Verify red**

Run: `npm test --workspace @e2a/cli -- --run src/__tests__/listen.test.ts`

Expected: both new tests fail because the rejection escapes before rendering.

### Task 2: Normalize forwarding transport errors

**Files:**
- Modify: `cli/src/commands/listen.ts`

- [ ] **Step 1: Add the minimal catch**

Wrap only the `fetch(forwardUrl, ...)` call in `try/catch`. On rejection, derive
the message with `err instanceof Error ? err.message : String(err)`, write
`Forward failed: ${message}\n` to stderr, and return.

- [ ] **Step 2: Verify green**

Run: `npm test --workspace @e2a/cli -- --run src/__tests__/listen.test.ts`

Expected: the listen test file passes.

- [ ] **Step 3: Verify the complete slice**

Run: `npm test --workspace @e2a/cli`

Expected: all CLI tests pass.

Run: `npm run build --workspace @e2a/cli`

Expected: TypeScript compilation succeeds.

- [ ] **Step 4: Commit**

Stage the command, tests, design, and plan, then commit with:

```text
fix(cli): isolate listen forward transport failures
```
