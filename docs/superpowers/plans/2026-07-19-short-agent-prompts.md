# Short Agent Prompts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the three long dashboard coding-agent prompts with concise, page-specific MCP prompts.

**Architecture:** Keep `AGENT_PROMPTS` as the single source for card copy. Add exact-value regression assertions before changing the three prompt strings; the shared component and clipboard behavior remain unchanged.

**Tech Stack:** TypeScript, React 19, Jest, Testing Library

---

### Task 1: Shorten the existing agent prompts

**Files:**
- Modify: `web/src/app/components/AgentPromptCard.test.tsx`
- Modify: `web/src/app/components/AgentPromptCard.tsx`

- [x] **Step 1: Write the failing exact-copy test**

Replace the broad published-document test with:

```tsx
test("uses concise page-specific MCP prompts", () => {
  expect(AGENT_PROMPTS.inboxes.prompt).toBe(
    "Help me set up an e2a inbox using https://api.e2a.dev/mcp",
  );
  expect(AGENT_PROMPTS.domains.prompt).toBe(
    "Help me connect a custom domain to e2a using https://api.e2a.dev/mcp",
  );
  expect(AGENT_PROMPTS.templates.prompt).toBe(
    "Help me set up e2a email templates using https://api.e2a.dev/mcp",
  );
});
```

- [x] **Step 2: Run the focused test and verify RED**

Run:

```bash
npm test -- --runInBand src/app/components/AgentPromptCard.test.tsx
```

from `web/`.

Expected: FAIL because `AGENT_PROMPTS` still contains the existing long prompts.

- [x] **Step 3: Replace the prompt values**

Set the three `prompt` fields in `AgentPromptCard.tsx` to:

```tsx
prompt: "Help me set up e2a email templates using https://api.e2a.dev/mcp",
prompt: "Help me set up an e2a inbox using https://api.e2a.dev/mcp",
prompt: "Help me connect a custom domain to e2a using https://api.e2a.dev/mcp",
```

Keep the existing order (`templates`, `inboxes`, `domains`) and do not change
the blurbs, heading, component markup, or copy interaction.

- [x] **Step 4: Run focused verification and verify GREEN**

Run:

```bash
npm test -- --runInBand src/app/components/AgentPromptCard.test.tsx
```

from `web/`.

Expected: PASS for the rendering, exact-copy, and clipboard tests.

- [x] **Step 5: Run the full web test suite**

Run:

```bash
npm test -- --runInBand
```

from `web/`.

Expected: all Jest suites pass with zero failures.

- [x] **Step 6: Commit the completed slice**

```bash
git add web/src/app/components/AgentPromptCard.tsx web/src/app/components/AgentPromptCard.test.tsx docs/superpowers/plans/2026-07-19-short-agent-prompts.md
git commit -m "fix(web): shorten coding agent prompts"
```
