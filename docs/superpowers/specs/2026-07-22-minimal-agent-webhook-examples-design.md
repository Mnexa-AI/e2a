# Minimal agent webhook examples

## Goal

Reduce PR #617 from an eight-integration framework matrix to two small,
copyable reference applications: one Python webhook and one TypeScript
webhook. Keep the ergonomic inbound SDK flow obvious and move alternative
provider integrations into short README snippets.

## Maintained examples

- `examples/agent-framework-webhooks/python`: one runnable OpenAI Agents SDK
  webhook.
- `examples/agent-framework-webhooks/typescript`: one runnable OpenAI Agents
  SDK webhook.
- `examples/adk-cloud-webhook`: keep the existing expanded ADK tutorial as the
  advanced stateful example; do not duplicate it in the minimal suite.

Each runnable example has one application entry point, one delivery handler,
one deterministic fake agent for tests/dry runs, a small package manifest, and
focused tests. There is no runtime framework selector or provider adapter
directory.

## Delivery flow and invariants

Both examples continue to:

1. enforce a 1 MiB request-body limit while preserving the exact signed bytes;
2. call `construct_event` / `constructEvent` before hydration or model work;
3. claim `event.id` before processing and release the claim on failure;
4. hydrate with `client.inbound.from_event(event)` /
   `client.inbound.fromEvent(event)`;
5. derive a collision-safe, retry-stable conversation anchor for first contact;
6. project only normalized email fields into the model prompt;
7. reply through the bound `email.reply(...)` with `event.id` as idempotency key;
8. expose a fake, signed, no-network dry run covering reply and duplicate paths.

Raw MIME, the full message view, and attachments remain excluded from prompts
and logs. The in-memory deduper remains explicitly tutorial-only.

## Provider presentation

OpenAI Agents SDK is the only installed and executable model integration in
the two minimal applications. The shared README includes compact replacement
snippets for:

- Anthropic Messages SDK;
- LangChain;
- Google ADK, linking to the expanded ADK webhook example for session handling.

The snippets show only how to turn the safe normalized prompt into reply text.
They do not introduce extra runnable projects, package dependencies, framework
selection variables, tests, or CI jobs.

## Removals

- Python and TypeScript provider adapter directories and adapter matrix tests.
- `AGENT_FRAMEWORK` selection and provider-specific credential validation.
- Anthropic, LangChain, and ADK dependencies from the minimal package manifests.
- Matrix tables and duplicated per-provider setup prose.
- Repository guards that require eight adapter files; replace them with guards
  for the two runnable facade paths and the README snippets.

## Testing and CI

CI keeps one job for the two minimal examples and the existing isolated ADK
tutorial job. The minimal job installs only local e2a, OpenAI Agents SDK, web
framework dependencies, and test tools. It runs:

- Python tests, mypy, and signed fake dry run;
- TypeScript tests, typecheck, build, and signed fake dry run.

Repository guards require both facade hydration spellings, bound replies, both
dry-run commands, and the three alternative-provider README snippets. Legacy
low-level fetch/reply calls remain forbidden.

## Success criteria

- A reader can understand each runnable example without navigating a provider
  abstraction layer.
- Only two minimal runnable applications are introduced by PR #617.
- OpenAI production factories compile against the installed official SDKs.
- Alternative providers remain discoverable through concise, accurate snippets.
- All example, SDK, repository guard, and CI checks remain green.
