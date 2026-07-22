# MCP P1 Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct MCP side-effect metadata, report the deployed build version, and authenticate requests before accepting large JSON bodies.

**Architecture:** Keep tool metadata local to registration, isolate product/deployment version resolution in a small module, and split the HTTP POST path into authentication middleware followed by route-local JSON parsing and stateless dispatch. Preserve the current SDK transport and 40 MB authenticated attachment allowance.

**Tech Stack:** TypeScript, Express, MCP TypeScript SDK, Vitest, Docker BuildKit, GitHub Actions

---

### Task 1: Correct `get_message` metadata

**Files:**
- Modify: `mcp/src/tools/messages.ts`
- Test: `mcp/tests/tools.test.ts`

- [ ] **Step 1: Write the failing catalog assertion**

Move `get_message` out of the read-only list and assert it is non-read-only:

```ts
for (const n of ["get_message", "create_agent", "send_message", "approve_review", "create_webhook", "create_template", "create_api_key"]) {
  expect(byName.get(n)?.readOnlyHint ?? false, `${n} not read-only`).toBe(false);
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run: `npm test --workspace @e2a/mcp-server -- --run mcp/tests/tools.test.ts -t "every tool carries"`

Expected: FAIL because `get_message.readOnlyHint` is `true`.

- [ ] **Step 3: Remove the false hint**

Keep an empty annotations object so the catalog-wide annotations invariant remains true:

```ts
annotations: {},
```

- [ ] **Step 4: Run the focused test and verify it passes**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit the metadata slice**

```bash
git add mcp/src/tools/messages.ts mcp/tests/tools.test.ts
git commit -m "fix(mcp): mark get_message as stateful"
```

### Task 2: Report the product/deployment version

**Files:**
- Create: `mcp/src/version.ts`
- Modify: `mcp/src/server.ts`
- Modify: `mcp/server.json`
- Modify: `mcp/Dockerfile`
- Modify: `.github/workflows/publish-mcp-http.yml`
- Test: `mcp/tests/version.test.ts`

- [ ] **Step 1: Write failing resolver and drift tests**

Add tests that expect environment override, root `VERSION` fallback, manifest
agreement with root `VERSION`, and the live handshake to report root `VERSION`:

```ts
expect(resolveServerVersion({ MCP_SERVER_VERSION: "1.0.0+sha.abcdef123456" })).toBe("1.0.0+sha.abcdef123456");
expect(resolveServerVersion({})).toBe(readFileSync(new URL("../../VERSION", import.meta.url), "utf8").trim());
expect(manifest.version).toBe(productVersion);
expect(client.getServerVersion()).toEqual({ name: "e2a", version: productVersion });
```

- [ ] **Step 2: Run the version test and verify it fails**

Run: `npm test --workspace @e2a/mcp-server -- --run mcp/tests/version.test.ts`

Expected: FAIL because the resolver does not exist and the manifest/live server still use `0.5.0`.

- [ ] **Step 3: Implement the resolver and server wiring**

Create a resolver that trims `MCP_SERVER_VERSION`, otherwise reads root `VERSION` relative to `import.meta.url`, and throws if either selected value is empty. Replace `PACKAGE_VERSION` in `server.ts` with `resolveServerVersion()` while retaining the existing injectable `version` test seam.

- [ ] **Step 4: Wire image metadata**

Update `mcp/server.json` to `1.0.0`. Add Docker `ARG MCP_SERVER_VERSION`, runtime `ENV MCP_SERVER_VERSION=${MCP_SERVER_VERSION}`, and copy root `VERSION`. Add a workflow step that derives exact tag versions or `<VERSION>+sha.<12 SHA>` and passes it through `build-args`.

- [ ] **Step 5: Run version tests and build**

Run:

```bash
npm test --workspace @e2a/mcp-server -- --run mcp/tests/version.test.ts
npm run build --workspace @e2a/mcp-server
```

Expected: both commands PASS.

- [ ] **Step 6: Commit the version slice**

```bash
git add mcp/src/version.ts mcp/src/server.ts mcp/tests/version.test.ts mcp/server.json mcp/Dockerfile .github/workflows/publish-mcp-http.yml
git commit -m "fix(mcp): report deployed server version"
```

### Task 3: Authenticate before JSON parsing

**Files:**
- Modify: `mcp/src/http-server.ts`
- Test: `mcp/tests/http.test.ts`

- [ ] **Step 1: Write failing pre-parse rejection tests**

Send malformed JSON with no bearer and with a bearer whose `whoami` returns
401. Expect both responses to be 401 JSON-RPC errors with `id: null`, proving
the parser was not reached. Change the large-body test to use a valid bearer
and expect the MCP handler response rather than a pre-auth 401.

- [ ] **Step 2: Run focused HTTP tests and verify they fail**

Run: `npm test --workspace @e2a/mcp-server -- --run mcp/tests/http.test.ts -t "bearer|larger than 1 MB"`

Expected: malformed JSON is rejected before the current auth path, and the new valid-bearer large-body expectation fails.

- [ ] **Step 3: Split authentication from dispatch**

Remove global `app.use(express.json(...))`. Add a route chain:

```ts
app.post(
  "/mcp",
  authenticateClient(cache, opts),
  express.json({ limit: "40mb" }),
  async (req, res) => handleAuthenticatedClientRequest(req, res, opts),
);
```

The authentication middleware resolves and caches the principal, stores
`{ bearer, principal }` in `res.locals`, and returns missing/invalid-token 401s
with `jsonRpcError(null, ...)`. The dispatch function reads those locals and
retains the existing transport lifecycle.

- [ ] **Step 4: Run focused and full MCP tests**

Run:

```bash
npm test --workspace @e2a/mcp-server -- --run mcp/tests/http.test.ts
npm test --workspace @e2a/mcp-server
```

Expected: all tests PASS.

- [ ] **Step 5: Commit the transport slice**

```bash
git add mcp/src/http-server.ts mcp/tests/http.test.ts
git commit -m "fix(mcp): authenticate before parsing request bodies"
```

### Task 4: Final verification and PR

**Files:**
- Verify all files changed by Tasks 1-3

- [ ] **Step 1: Run final gates**

```bash
npm test --workspace @e2a/mcp-server
npm run build --workspace @e2a/mcp-server
git diff --check
```

Expected: all MCP tests pass, TypeScript builds, and `git diff --check` prints no errors.

- [ ] **Step 2: Review the branch diff**

Run: `git diff --stat origin/main...HEAD && git diff origin/main...HEAD`

Expected: only the three approved P1 fixes plus their design/plan and tests.

- [ ] **Step 3: Push and open one ready PR**

```bash
git push -u origin codex/mcp-p1-hardening
gh pr create --base main --head codex/mcp-p1-hardening --title "fix(mcp): harden P1 interface behavior" --body-file <prepared-body>
```

Expected: GitHub returns the new PR URL.
