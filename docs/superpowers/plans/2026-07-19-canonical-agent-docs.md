# Canonical Agent Docs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Author e2a's agent Markdown in `plugins/e2a/docs/` while publishing byte-identical, automatically refreshed mirrors from `web/public/` with CI drift enforcement.

**Architecture:** A dependency-free Node module owns an explicit canonical-to-hosted file map and provides sync and non-mutating check modes. The web build invokes sync; the existing repository-integrity job invokes check. Canonical and hosted files remain committed and reviewable.

**Tech Stack:** Node.js 22 ES modules, Node built-in test runner, Bash, npm/Next.js

---

### Task 1: Implement deterministic document synchronization

**Files:**
- Create: `scripts/sync-agent-docs.test.mjs`
- Create: `scripts/sync-agent-docs.mjs`

- [ ] **Step 1: Write failing sync behavior tests**

Create tests that import `syncAgentDocs` and `parseArgs`, build canonical and
mirror fixtures beneath `mkdtemp`, and assert:

```js
await assert.rejects(
  syncAgentDocs({ repoRoot, check: true, log: () => {} }),
  /missing hosted agent doc.*web\/public\/e2a\.md/s,
);
await syncAgentDocs({ repoRoot, check: false, log: () => {} });
assert.deepEqual(
  await readFile(join(repoRoot, "web/public/e2a.md")),
  await readFile(join(repoRoot, "plugins/e2a/docs/e2a.md")),
);
await assert.doesNotReject(
  syncAgentDocs({ repoRoot, check: true, log: () => {} }),
);
assert.throws(() => parseArgs(["--wat"]), /unknown option: --wat/);
```

Cover both mapped documents, stale mirrors, a missing canonical source, and
aggregation of multiple check-mode mismatches.

- [ ] **Step 2: Run the tests and verify RED**

Run `node --test scripts/sync-agent-docs.test.mjs`.

Expected: FAIL because `scripts/sync-agent-docs.mjs` does not exist.

- [ ] **Step 3: Implement the synchronization module and CLI**

Define this explicit map:

```js
export const AGENT_DOC_MIRRORS = [
  ["plugins/e2a/docs/e2a.md", "web/public/e2a.md"],
  ["plugins/e2a/docs/templates.md", "web/public/templates.md"],
];
```

Implement `syncAgentDocs({ repoRoot, check, log })` with `readFile`, `mkdir`,
and `writeFile`. Missing canonical files throw immediately. Check mode collects
all missing/stale mirrors and throws one error after visiting the full map.
Write mode creates parent directories and writes only mismatched bytes.
Implement `parseArgs` accepting only no arguments or `--check`, then invoke the
function when the module is the CLI entry point and set `process.exitCode = 1`
on error.

- [ ] **Step 4: Run the tests and verify GREEN**

Run `node --test scripts/sync-agent-docs.test.mjs`.

Expected: all synchronization and error-path tests pass.

- [ ] **Step 5: Commit the synchronization engine**

```bash
git add scripts/sync-agent-docs.mjs scripts/sync-agent-docs.test.mjs docs/superpowers/plans/2026-07-19-canonical-agent-docs.md
git commit -m "feat(docs): add agent doc mirror sync"
```

### Task 2: Relocate canonical docs and wire build/CI enforcement

**Files:**
- Create: `plugins/e2a/docs/e2a.md` from `web/public/e2a.md`
- Create: `plugins/e2a/docs/templates.md` from `web/public/templates.md`
- Regenerate: `web/public/e2a.md`
- Regenerate: `web/public/templates.md`
- Modify: `web/package.json`
- Modify: `scripts/check-repository-text-integrity.sh`

- [ ] **Step 1: Add a failing repository-integrity assertion**

Add this command before the success message in the integrity script:

```bash
node scripts/sync-agent-docs.mjs --check
```

Run `scripts/check-repository-text-integrity.sh` before canonical files exist.

Expected: FAIL naming `plugins/e2a/docs/e2a.md` as missing.

- [ ] **Step 2: Move the authoritative content and regenerate mirrors**

Relocate the current files without changing their bytes, then run:

```bash
node scripts/sync-agent-docs.mjs
node scripts/sync-agent-docs.mjs --check
cmp plugins/e2a/docs/e2a.md web/public/e2a.md
cmp plugins/e2a/docs/templates.md web/public/templates.md
```

Expected: sync and check exit 0; both `cmp` commands report no differences.

- [ ] **Step 3: Wire the production web build**

Add this script to `web/package.json` before `build`:

```json
"prebuild": "node ../scripts/sync-agent-docs.mjs"
```

Do not add dependencies or change the hosted filenames.

- [ ] **Step 4: Verify build and CI entry points**

Run:

```bash
node --test scripts/sync-agent-docs.test.mjs
scripts/check-repository-text-integrity.sh
npm run build
```

Run the build from `web/`. Expected: all commands exit 0 and the Next static
export retains `e2a.md` and `templates.md`.

- [ ] **Step 5: Commit canonical ownership and wiring**

```bash
git add plugins/e2a/docs web/public/e2a.md web/public/templates.md web/package.json scripts/check-repository-text-integrity.sh
git commit -m "refactor(docs): make plugin agent docs canonical"
```

### Task 3: Review every affected agent-facing document and reference

**Files:**
- Review/modify if inaccurate: `plugins/e2a/docs/**`
- Review/modify if inaccurate: `plugins/e2a/skills/**`
- Review/modify if inaccurate: `plugins/e2a/README.md`
- Review/modify if inaccurate: `plugins/e2a/clients/**`
- Review/modify if inaccurate: `web/public/llms.txt`
- Review/modify if inaccurate: `web/src/app/(app)/get-started/**`
- Review/modify if inaccurate: `docs/templates.md`

- [ ] **Step 1: Inventory hosted and repository references**

Run:

```bash
rg -n -S "e2a\.dev/(e2a|templates)\.md|web/public/(e2a|templates)\.md|plugins/e2a/docs|plugins/e2a/skills" plugins web docs README.md
```

Classify every hit: user-facing instructions keep stable e2a.dev URLs; source
ownership and contributor instructions use `plugins/e2a/docs/`.

- [ ] **Step 2: Correct stale ownership references**

Update only references that incorrectly identify `web/public/` as canonical or
tell maintainers to edit a mirror. Do not replace hosted URLs in end-user or
agent setup instructions.

- [ ] **Step 3: Re-sync after doc review and verify all agent docs**

Run:

```bash
node scripts/sync-agent-docs.mjs
node scripts/sync-agent-docs.mjs --check
node --test scripts/sync-agent-docs.test.mjs
scripts/check-repository-text-integrity.sh
npm test -- --runInBand
npm run build
```

Run the npm commands from `web/`. Expected: mirror check, Node tests,
repository integrity, all Jest suites, and the production build pass.

- [ ] **Step 4: Commit reviewed references if any changed**

```bash
git add plugins web/public docs web/src/app
git commit -m "docs(plugin): align canonical agent doc references"
```

Skip this commit only when the inventory proves no reference edits are needed.
