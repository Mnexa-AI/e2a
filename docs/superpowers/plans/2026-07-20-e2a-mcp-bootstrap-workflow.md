# e2a MCP Bootstrap Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Teach the e2a skill to detect, configure, authenticate, and verify the hosted e2a MCP connection before operating an inbox.

**Architecture:** Add contract-style assertions to the existing plugin agent-guidance test, then replace the skill's ambiguous first-run prose with one compact state-driven bootstrap workflow. Keep client-specific connection essentials inline while retaining `e2a.md` as the long-form reference.

**Tech Stack:** Markdown Agent Skill, Node.js built-in test runner, repository plugin validator

---

### Task 1: Lock the bootstrap guidance contract

**Files:**
- Modify: `scripts/plugin-agent-guidance.test.mjs`
- Test: `scripts/plugin-agent-guidance.test.mjs`

- [x] **Step 1: Add a failing contract test**

Add one test that reads `plugins/e2a/skills/e2a/SKILL.md` and asserts all agreed branches:

```js
test("the e2a skill bootstraps and verifies an MCP connection", async () => {
  const source = await readFile("plugins/e2a/skills/e2a/SKILL.md", "utf8");
  const bootstrap = source.match(
    /### First run:[\s\S]*?(?=\n### |\n## )/,
  )?.[0] ?? "";

  assert.match(bootstrap, /available e2a MCP tools/i);
  assert.match(bootstrap, /e2a MCP [`\"]?whoami/i);
  assert.match(bootstrap, /not.*(?:Unix|shell).*whoami/i);
  assert.match(bootstrap, /claude mcp add --transport http --scope user e2a https:\/\/api\.e2a\.dev\/mcp/);
  assert.match(bootstrap, /codex mcp login e2a/);
  assert.match(bootstrap, /mcpServers/);
  assert.match(bootstrap, /Streamable HTTP/i);
  assert.match(bootstrap, /authentication failure/i);
  assert.match(bootstrap, /network, timeout, or server/i);
  assert.match(bootstrap, /list_messages/);
  assert.match(bootstrap, /resume.*original/i);
  assert.match(bootstrap, /never ask.*API key/i);
});
```

- [x] **Step 2: Run the focused test and verify RED**

Run:

```bash
node --test scripts/plugin-agent-guidance.test.mjs
```

Expected: the new bootstrap test fails because the current first-run section does not contain tool-availability detection, all client branches, or readiness verification.

- [x] **Step 3: Commit the red test separately**

```bash
git add scripts/plugin-agent-guidance.test.mjs
git commit -m "test(plugin): define MCP bootstrap guidance"
```

### Task 2: Implement the bootstrap workflow in the skill

**Files:**
- Modify: `plugins/e2a/skills/e2a/SKILL.md`
- Test: `scripts/plugin-agent-guidance.test.mjs`

- [x] **Step 1: Replace the first-run section**

Replace the existing first-run prose with concise guidance that performs these operations in order:

```markdown
### First run: connect and verify e2a

Before the first e2a operation in a task, inspect the tools available in the
current client for the e2a MCP tool surface.

1. If e2a tools are available, call the e2a MCP `whoami` tool (often exposed as
   `mcp__e2a__whoami`). This never means the Unix or shell `whoami` command.
2. If they are absent, identify the client and give its shortest setup path:
   - Claude Code: `claude mcp add --transport http --scope user e2a
     https://api.e2a.dev/mcp`, then `/mcp`.
   - Codex: configure `[mcp_servers.e2a]` with
     `url = "https://api.e2a.dev/mcp"`, then `codex mcp login e2a`.
   - Cursor/Windsurf: configure
     `{ "mcpServers": { "e2a": { "url": "https://api.e2a.dev/mcp" } } }`.
   - Other remote-MCP clients: configure the same Streamable HTTP endpoint and
     complete OAuth 2.1 authorization.
3. Wait for the user to complete interactive OAuth, check the e2a tools again,
   and call the e2a MCP `whoami` tool.
4. Classify authentication failures separately from network, timeout, or server
   failures; reauthorize only for the former.
5. Resolve an agent-scoped or account-scoped inbox, verify it with
   `list_messages`, then resume the user's original task.
```

The final text must include the exact canonical endpoint and setup commands from
`plugins/e2a/docs/e2a.md`, must not silently edit client configuration, and must
never ask for an API key during interactive OAuth setup.

- [x] **Step 2: Update the skill version**

Change both the YAML `version` and the matching HTML version comment from `18`
to `19` so packaged consumers can identify the behavior revision.

- [x] **Step 3: Run the focused test and verify GREEN**

Run:

```bash
node --test scripts/plugin-agent-guidance.test.mjs
```

Expected: all agent-guidance tests pass.

- [x] **Step 4: Commit the implementation**

```bash
git add plugins/e2a/skills/e2a/SKILL.md
git commit -m "docs(plugin): add MCP bootstrap workflow"
```

### Task 3: Validate the plugin package

**Files:**
- Verify: `plugins/e2a/skills/e2a/SKILL.md`
- Verify: `scripts/plugin-agent-guidance.test.mjs`

- [x] **Step 1: Run plugin and documentation checks**

Run:

```bash
node scripts/validate-plugin.mjs
node scripts/sync-agent-docs.mjs --check
git diff --check
```

Expected: every command exits successfully with no drift or whitespace errors.

- [x] **Step 2: Run the complete agent-guidance test set**

Run:

```bash
node --test scripts/plugin-agent-guidance.test.mjs scripts/sdk-examples-contract.test.mjs scripts/sync-agent-docs.test.mjs
```

Expected: all tests pass with zero failures.

- [x] **Step 3: Inspect the final scoped diff**

Run:

```bash
git diff -- scripts/plugin-agent-guidance.test.mjs plugins/e2a/skills/e2a/SKILL.md
```

Expected: the diff contains only the bootstrap contract test and the approved skill workflow in addition to the already-present HITL-removal edits.
