# MCP Tool Alias Compatibility

## Goal

Keep every previously shipped MCP tool name callable after a rename and add an
additive-only catalog gate so future removals fail CI.

## Compatibility surface

The server will register these deprecated tools alongside their canonical
replacements:

| Deprecated tool | Canonical tool | Scope |
|---|---|---|
| `send_email` | `send_message` | runtime |
| `get_attachment_data` | `get_attachment` | runtime |
| `list_pending_messages` | `list_reviews` | account |
| `get_pending_message` | `get_review` | account |
| `approve_pending_message` | `approve_review` | account |
| `reject_pending_message` | `reject_review` | account |
| `approve_message` | `approve_review` | account |
| `reject_message` | `reject_review` | account |

Aliases remain visible in `tools/list` because a cached client must also be able
to rediscover and reason about them. Their titles and descriptions begin with a
clear deprecation notice and direct new callers to the canonical name.

## Adapter behavior

Aliases live in one dedicated `mcp/src/tools/legacy.ts` compatibility module.
They call the same `McpClient` methods as canonical tools but preserve the
input and output vocabulary that shipped with each old name.

- `send_email` accepts `body`, `html_body`, and `agent_email`, then maps them to
  the current send request's `text`, `html`, and explicit agent address.
- `get_attachment_data` accepts `attachment_index` and `agent_email`, requests
  inline bytes from the current attachment endpoint, and returns the historical
  attachment object including base64 `data`. The current server-side 256 KB
  inline limit still applies; the alias cannot restore the retired 2 MB
  client-side limit.
- `list_pending_messages` walks the canonical paginated account review queue
  and returns the historical `{ messages: [...] }` envelope. It includes both
  inbound and outbound holds because the unified review API is authoritative.
- `get_pending_message` delegates to canonical review detail.
- `approve_pending_message` maps `body_text` and `body_html` to current
  `text` and `html` reviewer overrides.
- `approve_message` already used the current `text` and `html` vocabulary.
- Both reject aliases preserve `message_id` and optional `reason`.

The compatibility module must not copy backend HTTP logic or call raw REST
endpoints. Authentication, retries, errors, idempotency, and snake_case output
continue through `McpClient` and `runTool`.

## Scope and safety

Each alias has the same visibility as its canonical tool. Runtime aliases are
available to both credential scopes. Every review alias is account-only,
including discovery, so an agent cannot regain review access through an old
name. Approval aliases preserve idempotency-key forwarding; reject aliases keep
the canonical destructive annotation.

## Additive catalog gate

`mcp/tool-names.v1.json` is the frozen, append-only list of all canonical and
deprecated v1 names. The MCP catalog test reads the actual account-scoped
`tools/list` response and fails if any baseline name is absent. New names may be
added without breaking the test; renames require retaining the old name as an
adapter and appending the new name to the baseline. The normal MCP CI job runs
this test, so an accidental removal is a blocking failure.

The baseline file is a compatibility artifact: entries are never removed or
renamed within v1. A future major MCP surface must introduce a separately
versioned baseline rather than rewriting this one.

## Testing

- Assert the account catalog contains all 59 canonical and compatibility names.
- Assert the agent catalog contains only the 13 canonical runtime tools plus
  the two runtime aliases; no review alias is visible or callable.
- Exercise every alias through MCP, including legacy field-name mapping,
  idempotency forwarding, inline attachment retrieval, both review directions,
  and historical response envelopes.
- Assert alias descriptions name the replacement and say they are deprecated.
- Prove the frozen baseline is sorted, unique, and a subset of the actual
  account catalog.
- Run SDK/MCP builds, the complete MCP suite and type tests, and
  `git diff --check`.

## Non-goals

- Reintroducing retired backend endpoints or their exact historical limits.
- Hiding aliases from discovery; doing so would undermine stale-client repair.
- Adding a general-purpose alias facility to the upstream MCP SDK.
- Removing aliases on a timer. Removal requires a future major-version policy.
