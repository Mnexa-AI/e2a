# Minimal Agent Webhook Examples Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the eight-integration runtime matrix with one minimal Python OpenAI webhook, one minimal TypeScript OpenAI webhook, and three concise alternative-provider README snippets.

**Architecture:** Each language keeps its verified delivery handler, bounded HTTP host, OpenAI production agent, deterministic fake dry run, and focused tests. Provider selection and adapter directories disappear; Anthropic, LangChain, and ADK become documentation-only substitutions, with the existing expanded ADK tutorial retained for stateful session behavior.

**Tech Stack:** Python 3.10+, FastAPI, OpenAI Agents SDK, pytest, mypy; Node 24.13+, TypeScript, Express, OpenAI Agents SDK, Vitest; e2a ergonomic inbound SDK.

---

### Task 1: Collapse the Python framework matrix to one OpenAI example

**Files:**
- Create: `examples/agent-framework-webhooks/python/agent_webhooks/agent.py`
- Modify: `examples/agent-framework-webhooks/python/agent_webhooks/app.py`
- Modify: `examples/agent-framework-webhooks/python/agent_webhooks/contracts.py`
- Modify: `examples/agent-framework-webhooks/python/agent_webhooks/dry_run.py`
- Modify: `examples/agent-framework-webhooks/python/pyproject.toml`
- Modify: `examples/agent-framework-webhooks/python/.env.example`
- Modify: `examples/agent-framework-webhooks/python/tests/test_app.py`
- Modify: `examples/agent-framework-webhooks/python/tests/test_dry_run.py`
- Create: `examples/agent-framework-webhooks/python/tests/test_agent.py`
- Delete: `examples/agent-framework-webhooks/python/agent_webhooks/adapters/`
- Delete: `examples/agent-framework-webhooks/python/tests/test_adapters.py`

- [ ] **Step 1: Replace matrix tests with the minimal OpenAI contract**

Add tests that construct `OpenAIReplyAgent` with an injected runner, assert the
safe prompt reaches it, assert `final_output` becomes reply text, and assert
provider exceptions propagate. Change app tests so startup requires only
`OPENAI_API_KEY`, creates only the OpenAI agent, and has no `AGENT_FRAMEWORK`
branches.

```python
async def test_openai_agent_uses_safe_prompt(email: AsyncInboundEmail) -> None:
    prompts: list[str] = []

    async def run(prompt: str) -> object:
        prompts.append(prompt)
        return SimpleNamespace(final_output="OpenAI reply")

    reply = await OpenAIReplyAgent(run).reply(email, "conv_evt_full")
    assert reply == "OpenAI reply"
    assert prompts == [email_prompt(email)]
    assert "raw_message" not in prompts[0]
```

- [ ] **Step 2: Run focused Python tests and verify RED**

Run:

```bash
cd examples/agent-framework-webhooks/python
.venv/bin/pytest tests/test_agent.py tests/test_app.py tests/test_dry_run.py -q
```

Expected: failures because `agent_webhooks.agent` does not exist and the app
still selects four frameworks.

- [ ] **Step 3: Implement one production agent and remove selection**

Move the OpenAI factory into `agent.py`, retaining runner injection for no-key
tests. Keep the safe prompt projection in `prompt.py` and the common
`ReplyAgent` protocol. The production factory is the only provider factory:

```python
class OpenAIReplyAgent:
    def __init__(self, run: Callable[[str], Awaitable[Any]]) -> None:
        self._run = run

    @classmethod
    def from_env(cls) -> "OpenAIReplyAgent":
        from agents import Agent, Runner

        agent = Agent(
            name="Email assistant",
            instructions=REPLY_INSTRUCTIONS,
            model=os.getenv("OPENAI_MODEL", "gpt-5.6"),
        )

        async def run(prompt: str) -> Any:
            return await Runner.run(agent, prompt)

        return cls(run)
```

In `app.py`, validate `E2A_API_KEY`, `E2A_WEBHOOK_SECRET`, and
`OPENAI_API_KEY`, then construct `OpenAIReplyAgent.from_env()` directly. Delete
the adapter package and remove Anthropic, LangChain, and ADK dependencies from
`pyproject.toml`. Remove `AGENT_FRAMEWORK` and non-OpenAI variables from
`.env.example`. Keep the fake dry-run agent local to `dry_run.py`.

- [ ] **Step 4: Run the complete Python example verification**

Run:

```bash
cd examples/agent-framework-webhooks/python
.venv/bin/pip install -e ../../../sdks/python -e '.[dev]'
.venv/bin/pytest -q
.venv/bin/mypy agent_webhooks tests
.venv/bin/python -m agent_webhooks.dry_run
```

Expected: all tests and mypy pass; dry run prints one replied result and one
duplicate result without provider credentials.

- [ ] **Step 5: Commit the Python reduction**

```bash
git add examples/agent-framework-webhooks/python
git commit -m "refactor(examples): simplify Python agent webhook"
```

### Task 2: Collapse the TypeScript framework matrix to one OpenAI example

**Files:**
- Create: `examples/agent-framework-webhooks/typescript/src/agent.ts`
- Modify: `examples/agent-framework-webhooks/typescript/src/app.ts`
- Modify: `examples/agent-framework-webhooks/typescript/src/contracts.ts`
- Modify: `examples/agent-framework-webhooks/typescript/src/dry-run.ts`
- Modify: `examples/agent-framework-webhooks/typescript/package.json`
- Modify: `examples/agent-framework-webhooks/typescript/package-lock.json`
- Modify: `examples/agent-framework-webhooks/typescript/.env.example`
- Modify: `examples/agent-framework-webhooks/typescript/test/app.test.ts`
- Modify: `examples/agent-framework-webhooks/typescript/test/dry-run.test.ts`
- Create: `examples/agent-framework-webhooks/typescript/test/agent.test.ts`
- Delete: `examples/agent-framework-webhooks/typescript/src/adapters/`
- Delete: `examples/agent-framework-webhooks/typescript/test/adapters.test.ts`

- [ ] **Step 1: Replace adapter matrix tests with the OpenAI contract**

Test an injected `run` function, safe prompt projection, final output
extraction, null output, and propagated errors. Update app tests so there is no
framework selector and startup readiness depends only on OpenAI credentials.

```ts
it("projects a safe prompt into OpenAI", async () => {
  const prompts: string[] = [];
  const agent = new OpenAIReplyAgent(async (prompt) => {
    prompts.push(prompt);
    return { finalOutput: "OpenAI reply" };
  });

  await expect(agent.reply(email, "conv_evt_full")).resolves.toBe("OpenAI reply");
  expect(prompts).toEqual([emailPrompt(email)]);
  expect(prompts[0]).not.toContain("raw MIME sentinel");
});
```

- [ ] **Step 2: Run focused TypeScript tests and verify RED**

Run:

```bash
cd examples/agent-framework-webhooks/typescript
npm test -- --run test/agent.test.ts test/app.test.ts test/dry-run.test.ts
```

Expected: failures because `src/agent.ts` does not exist and the app still
contains four provider branches.

- [ ] **Step 3: Implement one statically checked OpenAI agent**

Move the OpenAI integration into `src/agent.ts` and import the official SDK
directly:

```ts
import { Agent, run } from "@openai/agents";

export function createOpenAIReplyAgent(
  env: Record<string, string | undefined> = process.env,
): OpenAIReplyAgent {
  const agent = new Agent({
    name: "Email assistant",
    instructions: REPLY_INSTRUCTIONS,
    model: env.OPENAI_MODEL ?? "gpt-5.6",
  });
  return new OpenAIReplyAgent((prompt) => run(agent, prompt));
}
```

Construct this agent directly in `app.ts`. Delete the adapter directory and
remove Anthropic, LangChain, ADK, Zod, and their transitive direct dependencies
from `package.json`; regenerate the lockfile with `npm install`. Lower the
example engine to the repository-supported Node floor if OpenAI/Express allow
it, otherwise retain the highest actual dependency minimum and document it.
Remove `AGENT_FRAMEWORK` and non-OpenAI variables from `.env.example`. Keep the
fake agent local to `dry-run.ts`.

- [ ] **Step 4: Run the complete TypeScript example verification**

Run:

```bash
npm run build --workspace @e2a/sdk
cd examples/agent-framework-webhooks/typescript
npm install
npm test -- --run
npm run typecheck
npm run build
npm run dry-run
```

Expected: all tests, typecheck, and build pass; dry run prints one replied
result and one duplicate result without provider credentials.

- [ ] **Step 5: Commit the TypeScript reduction**

```bash
git add examples/agent-framework-webhooks/typescript
git commit -m "refactor(examples): simplify TypeScript agent webhook"
```

### Task 3: Replace the framework matrix with compact provider snippets

**Files:**
- Modify: `examples/agent-framework-webhooks/README.md`
- Modify: `README.md`
- Modify: `sdks/python/README.md`
- Modify: `sdks/typescript/README.md`
- Modify: `scripts/check-sdk-example-contracts.mjs`
- Modify: `.github/workflows/test.yml`

- [ ] **Step 1: Make repository guards describe the minimal shape**

Replace the eight adapter-file assertions with requirements for the two OpenAI
agent files, both handler facade spellings, both bound reply calls, both dry-run
commands, and README headings/snippets for Anthropic, LangChain, and Google ADK.
Keep legacy low-level calls forbidden in executable example source.

```js
const requiredFiles = [
  "examples/agent-framework-webhooks/python/agent_webhooks/agent.py",
  "examples/agent-framework-webhooks/typescript/src/agent.ts",
];

const readmePatterns = [
  /## Anthropic/,
  /## LangChain/,
  /## Google ADK/,
  /python -m agent_webhooks\.dry_run/,
  /npm run dry-run/,
];
```

- [ ] **Step 2: Run the guard and verify RED**

Run:

```bash
node scripts/check-sdk-example-contracts.mjs
```

Expected: failure until the new files and compact README structure are present.

- [ ] **Step 3: Rewrite the tutorial around two runnable examples**

The README starts with the two runnable OpenAI examples and their shared
signature-first lifecycle. Keep install, environment, run, subscription,
delivery status, trust-boundary, first-contact, and production-state caveats.
Replace the matrix and provider selector with three compact sections:

```markdown
## Anthropic

Replace the OpenAI agent body with a `messages.create(...)` call and join only
returned text blocks. Keep the webhook handler unchanged.

## LangChain

Create one agent and invoke it with the safe normalized prompt. Return the last
assistant message text. Keep the webhook handler unchanged.

## Google ADK

Use the effective conversation ID as `sessionId` and an inbox-scoped sender
identity as `userId`. See the expanded ADK webhook tutorial for the complete
session implementation.
```

Include concise official Python and TypeScript links and short code snippets,
but no additional projects, dependencies, selection variables, or runnable
commands. Update root and SDK README link text from “framework integrations”
to “minimal OpenAI webhook examples with provider snippets.”

- [ ] **Step 4: Simplify CI without weakening execution coverage**

Keep the `agent-framework-examples` job, but let its package manifests install
only OpenAI and web/test dependencies. Keep all Python tests/mypy/dry-run and
TypeScript tests/typecheck/build/dry-run commands. Keep the isolated expanded
ADK 1.x job unchanged.

- [ ] **Step 5: Run documentation and repository guards**

Run:

```bash
node scripts/check-sdk-example-contracts.mjs
bash scripts/check-repository-text-integrity.sh
node scripts/check-sdk-version-sync.mjs
git diff --check
```

Expected: all guards pass and no removed adapter path remains required.

- [ ] **Step 6: Commit the documentation and guard reduction**

```bash
git add examples/agent-framework-webhooks/README.md README.md \
  sdks/python/README.md sdks/typescript/README.md \
  scripts/check-sdk-example-contracts.mjs .github/workflows/test.yml
git commit -m "docs(examples): focus agent webhooks on minimal references"
```

### Task 4: Verify, review, and update PR #617

**Files:**
- Modify only files required to fix failures within the approved reduction.

- [ ] **Step 1: Run both minimal example suites**

Run the complete Python and TypeScript commands from Tasks 1 and 2.

Expected: both production integrations compile; both signed fake dry runs reply
once and deduplicate the second delivery without provider keys.

- [ ] **Step 2: Run SDK regression gates**

Run:

```bash
npm test --workspace @e2a/sdk
sdks/python/.venv/bin/pytest sdks/python/tests/ -q
sdks/python/.venv/bin/mypy
```

Expected: SDK unit tests and type gates pass unchanged.

- [ ] **Step 3: Audit the reduction**

Run:

```bash
git diff --stat origin/main...HEAD
git ls-files 'examples/agent-framework-webhooks/**'
rg -n 'AGENT_FRAMEWORK|adapters/|rawMessage|raw_message|sk-' \
  examples/agent-framework-webhooks .github/workflows/test.yml \
  scripts/check-sdk-example-contracts.mjs
```

Expected: no selector or adapter directory remains; raw MIME references occur
only in negative tests/security prose; no real credential is present.

- [ ] **Step 4: Perform independent review and fix findings**

Review the whole diff for signature ordering, body limits, first-contact
threading, idempotency, dependency accuracy, snippet correctness, and accidental
scope. Re-run focused tests after every fix.

- [ ] **Step 5: Rebase, push, and update the PR**

```bash
git fetch origin main
git rebase origin/main
git push --force-with-lease origin codex/agent-framework-inbound-examples
gh pr edit 617 --title "docs(examples): add minimal ergonomic webhook examples"
```

Update the PR description to describe two runnable OpenAI examples plus three
provider snippets and list the final verification counts. Expected: PR #617 is
mergeable and CI starts on the reduced diff.
