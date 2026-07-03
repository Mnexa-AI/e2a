<!--
Thanks for the PR! Keep the description tight — explain *why*, link the
context, and use the checklist below to surface anything that's still
open.

If this PR doesn't touch the API or any client surface (e.g. docs-only,
infra, refactor), feel free to delete the "Client surface checklist"
section entirely.
-->

## Summary

<!-- 1–3 sentences: what changes, why. Link the issue / RFC / Slack thread if any. -->

## Client surface checklist

<!--
Every API change must land in every client at the same time. Skipping a
client should be an explicit decision noted below, not an oversight.
Delete rows that genuinely don't apply (e.g. `e2a config` doesn't need
to be in the Python SDK).
-->

- [ ] Go handler + integration tests
- [ ] Migration written + idempotent + safe on prod-sized tables (see [CLAUDE.md](../CLAUDE.md))
- [ ] OpenAPI spec + generated types refreshed (`make generate` is clean)
- [ ] TypeScript SDK — generated base regenerated (`make generate-sdk`) **and** resource added to the hand-written `E2AClient` (`sdks/typescript/src/v1/client.ts`)
- [ ] Python SDK — generated base regenerated (`make generate-sdk`) **and** resource added to the hand-written async `E2AClient` (`sdks/python/src/e2a/v1/client.py`)
- [ ] CLI command or flag in `cli/src/commands/` + wired into `cli/src/bin/e2a.ts`
- [ ] MCP tool in `mcp/src/tools/` + registry assertion in `mcp/tests/{tools,http}.test.ts`
- [ ] Tests at each surface above (positive + at least one negative-path / regression case)

**If a row is intentionally skipped**, state why here so the reviewer doesn't have to guess:

> _e.g. "MCP tool deferred — endpoint is admin-only and not useful to an LLM"_

## Operational risk

<!--
Touched user data, side effects, auth, billing, notifications, or
external integrations? Note rollback behavior, feature flags, anything
worth flagging to oncall.
-->

## Test plan

<!--
What you ran locally, what should be tried after merge. Tick items as
they're verified.
-->

- [ ]
- [ ]
