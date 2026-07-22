# MCP Account Review Tools

## Goal

Make `list_reviews` and `get_review` truthful, complete account-administration
tools backed by the canonical review API. They must expose inbound and outbound
holds across the authenticated account and must not be visible to agent-scoped
credentials.

## Tool surface

`list_reviews` moves from the runtime tier to the admin tier. It accepts the
shared `cursor` and `limit` pagination inputs and returns the stable MCP list
envelope `{ reviews, next_cursor? }`. The implementation calls
`sdk.reviews.list({ limit }).page(cursor)` exactly once per requested page. It
does not enumerate agents, scan message histories, or filter outbound messages
inside the MCP process.

`get_review` also moves to the admin tier. It remains addressed by
`message_id`, which is the review ID, and calls `sdk.reviews.get(message_id)`
directly. It does not discover an owning inbox through message-list scans.

The descriptions will explicitly say that both tools require an account-scoped
credential, cover inbound screening holds and outbound send holds, and use the
same IDs consumed by `approve_review` and `reject_review`.

## Scope and security

Account-scoped sessions expose all four review tools. Agent-scoped sessions
expose none of them: an agent may create a hold by sending mail, but it cannot
inspect account review data or release its own hold. The backend remains the
authorization boundary; MCP tier gating reduces accidental disclosure and
keeps the advertised catalog aligned with the API.

## Compatibility

This intentionally corrects a pre-GA contract that was internally
contradictory: the descriptions promised the unified account queue while the
implementation exposed an outbound-only agent-visible approximation.
`approve_review` and `reject_review` are unchanged.

The list envelope follows the existing frozen MCP pagination convention:
domain-named array, optional `next_cursor`, and omission of the cursor on the
last page.

## Testing

- Prove agent-scoped catalogs exclude all four review tools.
- Prove account-scoped catalogs include all four review tools.
- Prove `list_reviews` forwards `cursor` and `limit`, returns inbound and
  outbound review rows unchanged, and emits the MCP pagination envelope.
- Prove `get_review` delegates directly to `sdk.reviews.get`.
- Prove the old agent/message fan-out path is not called.
- Run the SDK build, MCP build, full MCP test suite, type tests, and
  `git diff --check`.

## Non-goals

- Renaming review tools or adding compatibility aliases; that is a separate
  P0 compatibility PR.
- Changing approve/reject behavior or backend authorization.
- Adding output schemas or changing the success-output representation.
