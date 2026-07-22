# Outbound Human-Review Setup Notice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional inbox-page instruction and matching agent guidance for configuring every outbound email to require human review.

**Architecture:** Extend the existing shared `AgentPromptCard` with optional, data-driven notice content so only the inbox variant renders the new callout. Teach the same opt-in policy through the canonical e2a skill and setup guide, then regenerate the hosted setup-guide mirror.

**Tech Stack:** React 19, TypeScript, Jest/Testing Library, Markdown agent skills/docs, Node.js repository validation scripts.

---

## File structure

- Modify `web/src/app/components/AgentPromptCard.tsx`: define the inbox-only notice data and render an optional semantic notice.
- Modify `web/src/app/components/AgentPromptCard.test.tsx`: cover notice presence, notice absence, and unchanged primary prompt copying.
- Modify `scripts/plugin-agent-guidance.test.mjs`: lock the exact opt-in protection configuration and the warning about `open + review` into agent guidance.
- Modify `plugins/e2a/skills/e2a/SKILL.md`: teach agents when and how to apply the always-review policy; increment the skill content version.
- Modify `plugins/e2a/docs/setup.md`: document the same optional setup instruction for agents without the installed skill.
- Modify generated mirror `web/public/setup.md`: refreshed from the canonical setup guide by `scripts/sync-agent-docs.mjs`.

### Task 1: Render the optional inbox notice

**Files:**
- Modify: `web/src/app/components/AgentPromptCard.test.tsx`
- Modify: `web/src/app/components/AgentPromptCard.tsx`

- [ ] **Step 1: Write failing component tests**

Add these tests after the existing concise-prompts test:

```tsx
test("shows the optional outbound-review instruction for inbox setup", () => {
  render(<AgentPromptCard {...AGENT_PROMPTS.inboxes} />);

  const notice = screen.getByRole("note", {
    name: "Optional outbound review setup",
  });
  expect(notice).toHaveTextContent("Want every outbound email reviewed first?");
  expect(notice).toHaveTextContent(
    "Configure this inbox so every outbound email requires human review.",
  );
});

test("does not show the inbox-only notice on other setup cards", () => {
  const { rerender } = render(
    <AgentPromptCard {...AGENT_PROMPTS.templates} />,
  );
  expect(
    screen.queryByRole("note", { name: "Optional outbound review setup" }),
  ).not.toBeInTheDocument();

  rerender(<AgentPromptCard {...AGENT_PROMPTS.domains} />);
  expect(
    screen.queryByRole("note", { name: "Optional outbound review setup" }),
  ).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run from `web/`:

```bash
npm test -- --runInBand src/app/components/AgentPromptCard.test.tsx
```

Expected: FAIL because `AGENT_PROMPTS.inboxes` has no notice and no element with role `note` is rendered.

- [ ] **Step 3: Add the optional notice data and prop**

Add this property to `AGENT_PROMPTS.inboxes`, after `prompt`:

```tsx
    notice: {
      label: "Want every outbound email reviewed first?",
      instruction:
        "Configure this inbox so every outbound email requires human review.",
    },
```

Extend the props and function signature:

```tsx
export type AgentPromptCardProps = {
  blurb: string;
  prompt: string;
  notice?: {
    label: string;
    instruction: string;
  };
};

export function AgentPromptCard({
  blurb,
  prompt,
  notice,
}: AgentPromptCardProps) {
```

- [ ] **Step 4: Render the notice below the primary prompt**

Immediately after the closing `</pre>`, add:

```tsx
      {notice ? (
        <aside
          role="note"
          aria-label="Optional outbound review setup"
          className="mt-3 rounded-[var(--r-md)] border px-3.5 py-3 text-[12px] leading-[1.6]"
          style={{
            color: "var(--fg-muted)",
            background: "var(--bg)",
            borderColor: "var(--border)",
          }}
        >
          <span className="font-semibold" style={{ color: "var(--fg)" }}>
            {notice.label}
          </span>{" "}
          Ask your agent: “{notice.instruction}”
        </aside>
      ) : null}
```

- [ ] **Step 5: Run the focused test and verify it passes**

Run from `web/`:

```bash
npm test -- --runInBand src/app/components/AgentPromptCard.test.tsx
```

Expected: PASS for all `AgentPromptCard` tests, including the unchanged primary-prompt assertion and clipboard test.

- [ ] **Step 6: Commit the dashboard change**

```bash
git add web/src/app/components/AgentPromptCard.tsx web/src/app/components/AgentPromptCard.test.tsx
git commit -m "feat(web): add optional outbound review setup notice"
```

### Task 2: Lock the agent-guidance contract with tests

**Files:**
- Modify: `scripts/plugin-agent-guidance.test.mjs`

- [ ] **Step 1: Add failing assertions for both canonical guidance files**

Add the following helper and test before the tether test:

```js
const assertAlwaysReviewGuidance = (source, file) => {
  assert.match(source, /update_protection/, file);
  assert.match(source, /outbound_gate_policy["`:\s]+allowlist/, file);
  assert.match(source, /outbound_gate_allowlist["`:\s]+\[\]/, file);
  assert.match(source, /outbound_gate_action["`:\s]+review/, file);
  assert.match(source, /holds_on_expiry["`:\s]+reject/, file);
  assert.match(source, /open.*review.*hold(?:s|ing)? nothing/is, file);
  assert.match(source, /only when the user (?:asks|requests)/i, file);
};

test("agent guidance teaches the opt-in always-review protection policy", async () => {
  for (const file of [
    "plugins/e2a/skills/e2a/SKILL.md",
    "plugins/e2a/docs/setup.md",
  ]) {
    const source = await readFile(file, "utf8");
    assertAlwaysReviewGuidance(source, file);
  }
});
```

- [ ] **Step 2: Run the guidance tests and verify they fail**

Run from the repository root:

```bash
node --test scripts/plugin-agent-guidance.test.mjs
```

Expected: FAIL in `agent guidance teaches the opt-in always-review protection policy` because neither canonical guidance file contains the full configuration.

### Task 3: Teach the installed e2a skill

**Files:**
- Modify: `plugins/e2a/skills/e2a/SKILL.md`

- [ ] **Step 1: Increment the skill content version**

Change both skill-local version markers from 19 to 20:

```yaml
version: 20
```

```markdown
<!-- version: 20 -->
```

Do not change the plugin package version (`0.5.0`); this is a guidance-content revision, not a plugin release bump.

- [ ] **Step 2: Add the opt-in policy workflow**

After the numbered “First run: connect and verify e2a” workflow and before “Triage the inbox,” add:

````markdown
### Optional: require human review for every outbound email

Only configure this when the user asks for every outbound email to require
human review. After selecting the inbox, call `update_protection` for that
inbox with:

```json
{
  "outbound_gate_policy": "allowlist",
  "outbound_gate_allowlist": [],
  "outbound_gate_action": "review",
  "holds_on_expiry": "reject"
}
```

The empty allowlist makes every recipient a gate non-match, `review` holds each
non-match for a human, and `reject` prevents an unreviewed message from being
sent when its hold expires. Do not use `open` with `review` for this outcome:
`open` matches every recipient, so the recipient gate holds nothing. This is
opt-in; never enable it merely because an inbox was created.
````

- [ ] **Step 3: Run the guidance test and confirm the skill half now matches**

Run:

```bash
node --test scripts/plugin-agent-guidance.test.mjs
```

Expected: still FAIL because `plugins/e2a/docs/setup.md` has not yet received the matching guidance; the skill assertions pass.

### Task 4: Update and synchronize the setup guide

**Files:**
- Modify: `plugins/e2a/docs/setup.md`
- Modify: `web/public/setup.md` (generated mirror)

- [ ] **Step 1: Add the optional setup section**

After “## 4. Confirm readiness” and before “## Use the inbox safely,” add:

````markdown
## Optional: require human review for every outbound email

Only when the user asks for this protection, call `update_protection` for the
selected inbox with:

```json
{
  "outbound_gate_policy": "allowlist",
  "outbound_gate_allowlist": [],
  "outbound_gate_action": "review",
  "holds_on_expiry": "reject"
}
```

An empty allowlist makes every recipient a gate non-match, `review` holds every
non-match for a human, and `reject` prevents expiry from sending an unreviewed
message. Do not use `open` with `review`: `open` matches every recipient, so the
recipient gate holds nothing. Inbox creation alone is not permission to enable
this policy.
````

- [ ] **Step 2: Refresh the hosted documentation mirror**

Run from the repository root:

```bash
node scripts/sync-agent-docs.mjs
```

Expected: `web/public/setup.md` becomes byte-identical to `plugins/e2a/docs/setup.md`; no other mirror changes.

- [ ] **Step 3: Run guidance, sync, and plugin validation**

```bash
node --test scripts/plugin-agent-guidance.test.mjs
node scripts/sync-agent-docs.mjs --check
node scripts/validate-plugin.mjs
```

Expected: all guidance tests PASS; sync reports no drift; validator reports the plugin valid with all manifests still at version `0.5.0`.

- [ ] **Step 4: Commit the agent-guidance change**

```bash
git add scripts/plugin-agent-guidance.test.mjs plugins/e2a/skills/e2a/SKILL.md plugins/e2a/docs/setup.md web/public/setup.md
git commit -m "docs(plugin): teach opt-in outbound review policy"
```

### Task 5: Final verification

**Files:**
- Verify all files changed in Tasks 1–4.

- [ ] **Step 1: Run the focused web suite**

Run from `web/`:

```bash
npm test -- --runInBand src/app/components/AgentPromptCard.test.tsx
```

Expected: PASS.

- [ ] **Step 2: Run repository agent-guidance gates**

Run from the repository root:

```bash
node --test scripts/plugin-agent-guidance.test.mjs scripts/sync-agent-docs.test.mjs
node scripts/sync-agent-docs.mjs --check
node scripts/validate-plugin.mjs
```

Expected: all tests and checks PASS.

- [ ] **Step 3: Check formatting and the final diff**

```bash
git diff --check HEAD~2..HEAD
git status --short
```

Expected: no whitespace errors and a clean worktree. The two implementation commits should contain only the UI/test changes and agent-guidance/mirror changes described above.
