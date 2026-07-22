# MCP `get_message` Contract Repair

## Goal

Repair the MCP `get_message` projection so agents receive the complete safe
message context already returned by the TypeScript SDK, without exposing raw
MIME or attachment bytes and without removing or renaming existing fields.

## Scope

This change is limited to `mcp/src/tools/messages.ts`, its MCP tool description,
and focused tests in `mcp/tests/tools.test.ts`. Compatibility aliases, review
queue routing, server versioning, transport capabilities, and general MCP output
schemas remain separate changes.

## Output contract

The tool keeps its existing flattened fields, including `text`, `html`, and
`received_at`. It adds the following optional or additive fields when the SDK
view supplies them:

- `direction`
- `labels`
- `flagged`
- `flag_reason`
- `protection`
- `truncated`
- `delivery_status`
- `delivery_detail`
- `review_status`
- `sent_as`
- `size_bytes`
- `deleted_at`

Inbound body selection uses `parsed.text` and `parsed.html` first, falling back
to `body.text` and `body.html` for outbound held drafts. `truncated` comes from
`parsed.truncated` and therefore reports whether inbound parsing clipped the
decoded body.

The tool continues to omit `raw_message` and attachment `data`. Attachments
remain metadata-only and are fetched separately through `get_attachment`.

## Implementation approach

Keep an explicit projection allowlist in the tool handler. Returning the entire
SDK `MessageView` would reduce code but could expose future large or sensitive
fields automatically. The explicit projection makes additions deliberate while
preserving the established MCP response shape.

Update the tool description to cover inbound and outbound messages, security
findings, labels, lifecycle fields, body truncation, and the intentional raw MIME
and attachment-byte exclusions.

## Testing

Add focused regression coverage that fails against the current implementation:

1. An HTML-only inbound message returns `parsed.html`, the parsed truncation
   flag, labels, direction, and suspicious-message evidence.
2. An outbound message returns its body fallback and delivery/review lifecycle
   fields.
3. Existing assertions continue to prove that raw MIME and attachment bytes are
   excluded.

Run the focused tool tests first, then the full MCP test and typecheck suite.

## Compatibility and safety

Every response change is additive. Existing field names and meanings remain
unchanged. No API calls, authorization rules, or mutation behavior change.
