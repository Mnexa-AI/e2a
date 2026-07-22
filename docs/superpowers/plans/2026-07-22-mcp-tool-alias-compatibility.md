# MCP Tool Alias Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore every shipped MCP tool name with its historical call vocabulary and make tool-name removal a blocking CI failure.

**Architecture:** Register eight explicit adapters in a dedicated legacy module. Each adapter preserves its old Zod schema and response envelope while delegating through the current `McpClient`; a frozen append-only JSON catalog is checked against the real account-scoped `tools/list` response in the normal MCP suite.

**Tech Stack:** TypeScript, MCP TypeScript SDK, `@e2a/sdk`, Zod, Vitest.

---

## File structure

- Create `mcp/src/tools/legacy.ts`: deprecated tool registrations and legacy-to-current field mapping.
- Create `mcp/tool-names.v1.json`: sorted append-only catalog baseline.
- Modify `mcp/src/server.ts`: register the compatibility module.
- Modify `mcp/src/tools/tiers.ts`: give aliases the same credential scope as their canonical tools.
- Modify `mcp/tests/tools.test.ts`: catalog, scope, description, adapter, and baseline regression tests.

### Task 1: Freeze names and scope before registering aliases

**Files:**
- Create: `mcp/tool-names.v1.json`
- Modify: `mcp/tests/tools.test.ts`

- [ ] **Step 1: Add the append-only catalog baseline**

Create a sorted JSON array containing all 51 canonical names plus:

```json
[
  "approve_message",
  "approve_pending_message",
  "get_attachment_data",
  "get_pending_message",
  "list_pending_messages",
  "reject_message",
  "reject_pending_message",
  "send_email"
]
```

The final file contains 59 unique sorted names; the excerpt above names the
eight additions but is not a separate file.

- [ ] **Step 2: Write failing catalog and scope tests**

In `tools.test.ts`, load the baseline with:

```ts
const frozenToolNames = JSON.parse(
  readFileSync(new URL("../tool-names.v1.json", import.meta.url), "utf8"),
) as string[];
```

Add tests that assert:

```ts
expect(frozenToolNames).toEqual([...new Set(frozenToolNames)].sort());
for (const name of frozenToolNames) expect(accountNames.has(name)).toBe(true);
expect(accountNames.size).toBe(59);
expect(agentNames.size).toBe(15);
expect(agentNames.has("send_email")).toBe(true);
expect(agentNames.has("get_attachment_data")).toBe(true);
for (const name of REVIEW_ALIASES) expect(agentNames.has(name)).toBe(false);
```

Update the exact registered-tool fixture with all eight aliases. Assert every
alias description contains `Deprecated` and its canonical replacement.

- [ ] **Step 3: Run focused tests and verify RED**

```bash
npm exec --workspace @e2a/mcp-server -- vitest run tests/tools.test.ts
```

Expected: FAIL because all eight aliases are absent and the catalog sizes remain
51 account / 13 agent.

### Task 2: Restore legacy message tools

**Files:**
- Create: `mcp/src/tools/legacy.ts`
- Modify: `mcp/src/server.ts`
- Modify: `mcp/src/tools/tiers.ts`
- Modify: `mcp/tests/tools.test.ts`

- [ ] **Step 1: Write failing message-adapter tests**

Add a `send_email` test passing the historical fields:

```ts
{
  to: ["alice@example.com"],
  subject: "Legacy",
  body: "plain",
  html_body: "<p>html</p>",
  agent_email: "bot@example.com",
  idempotency_key: "legacy-send-1"
}
```

Assert `client.send` receives current fields (`text`, `html`), the explicit
agent address, and `{ idempotencyKey: "legacy-send-1" }`.

Add a `get_attachment_data` test that asserts `client.getAttachment` receives
`{ inline: true }` and `agent_email`, then assert the alias returns exactly
`filename`, `content_type`, `size_bytes`, and base64 `data` without the current
download URL fields. Add a negative test where the client returns no `data` and
the tool returns `isError:true`.

- [ ] **Step 2: Run focused tests and verify RED**

Run the focused tools test. Expected: unknown-tool failures for both aliases.

- [ ] **Step 3: Implement and register message aliases**

Create `registerLegacyTools(server, client)` with strict historical schemas.
Use the current attachment mapping and `SendOpts` behavior. Mark titles and
descriptions deprecated and set annotations equal to canonical semantics.

Register the module after canonical tools in `buildServer`. Add `send_email`
and `get_attachment_data` to `RUNTIME_TOOLS`.

- [ ] **Step 4: Run focused tests and verify message aliases GREEN**

Expected: message-adapter tests pass; review alias/catalog tests remain red.

### Task 3: Restore both generations of review aliases

**Files:**
- Modify: `mcp/src/tools/legacy.ts`
- Modify: `mcp/src/tools/tiers.ts`
- Modify: `mcp/tests/tools.test.ts`

- [ ] **Step 1: Write failing review-adapter tests**

Add tests proving:

- `list_pending_messages` walks two pages via `client.listReviews`, retains one
  inbound and one outbound row, and returns `{ messages: [...] }`.
- `get_pending_message` calls `client.getReview(message_id)`.
- `approve_pending_message` maps `body_text`/`body_html`, attachments, and
  `idempotency_key` into `client.approveReview`.
- `approve_message` maps its `text`/`html` vocabulary without renaming it.
- Both reject aliases call `client.rejectReview(message_id, reason)` and carry
  `destructiveHint:true`.
- An agent-scoped client cannot call any of the six review aliases and their
  stub handlers remain untouched.

- [ ] **Step 2: Run focused tests and verify RED**

Expected: the six review aliases are unknown.

- [ ] **Step 3: Implement review aliases**

Register all six with strict historical schemas. `list_pending_messages` loops
over `client.listReviews({ cursor, limit: 100 })` until `next_cursor` is absent
and returns the old domain key `messages`. All adapters use `runTool`, so errors
retain the canonical structured error contract.

Add the six review aliases to `ADMIN_TOOLS`. Do not add any to runtime scope.

- [ ] **Step 4: Run focused tests and verify GREEN**

Expected: every adapter, scope, description, exact-catalog, and frozen-baseline
test passes.

### Task 4: Full verification, review, and stacked publication

**Files:**
- Verify all changed files, the approved design, and this plan.

- [ ] **Step 1: Run full verification**

```bash
npm run build --workspace @e2a/sdk
npm run build --workspace @e2a/mcp-server
npm test --workspace @e2a/mcp-server
git diff --check codex/mcp-account-reviews...HEAD
git status --short
```

Expected: builds, all MCP tests, and type tests pass; diff check is clean.

- [ ] **Step 2: Review compatibility and safety**

Confirm every alias preserves its documented historical fields, all calls flow
through `McpClient`, review aliases are account-only, approval idempotency is
preserved, the baseline is sorted/unique/additive, and no canonical tool changed.

- [ ] **Step 3: Commit and publish**

Commit implementation and tests as:

```bash
git commit -m "fix(mcp): preserve legacy tool aliases"
```

Push `codex/mcp-tool-aliases` and open a draft PR with base
`codex/mcp-account-reviews`, so its diff contains only the second P0. Do not
merge either PR in this workflow.
