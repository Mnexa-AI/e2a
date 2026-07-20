# Live Inbox Polling and Delivery Status Design

## Problem statement

The web dashboard fetches inbox messages, unread counts, and review holds with
SWR, but it only revalidates on mount, browser focus, reconnect, or explicit
mutations. A user who keeps the dashboard visible does not see newly received
mail or outbound status transitions until another revalidation occurs.

The message API already exposes the outbound delivery lifecycle, but the inbox
only labels `pending_review`. An outbound row persisted as `accepted` and later
transitioned through `sending` to `sent` is listed by the API, yet the UI does
not distinguish those states.

## Goals and non-goals

### Goals

- Make the active inbox converge on new inbound and outbound messages without a
  manual refresh.
- Keep account-wide Pending Review counts and visible per-inbox unread badges
  fresh.
- Surface attention-worthy outbound delivery states in compact thread rows.
- Surface every outbound message's delivery or review state in the opened
  conversation.
- Preserve the existing Gmail-like density and Loft design language.
- Reuse the existing REST API, SWR cache keys, and mutation invalidation
  helpers.

### Non-goals

- No OpenAPI, backend handler, database, SDK, CLI, MCP, or event-schema change.
- No browser WebSocket or SSE transport.
- No change to the agent WebSocket authentication or its one-connection-per-
  agent replacement policy.
- No polling of static dashboard resources such as domains, API keys, billing,
  templates, or settings.
- No server-side aggregate unread-count endpoint in this slice.

## Relevant context and constraints

- `SWRProvider` already enables focus and reconnect revalidation, request
  deduplication, previous-data retention, and bounded retries.
- The active inbox uses `agentMessagesKey(email, "all", "all")` and fetches the
  newest 100 mixed inbound and outbound summaries.
- The sidebar and Review page share `pendingMessagesKey`, so one SWR refresh
  updates both consumers.
- Inbox cards use one `agentUnreadKey(email)` probe per mounted card. These
  probes exist only while the Inboxes page is mounted.
- `MessageSummary.status` already projects `delivery_status`; its
  `review_status` remains a separate lifecycle axis.
- Delivery statuses are an open set. Known values are `accepted`, `sending`,
  `sent`, `delivered`, `deferred`, `bounced`, `complained`, and `failed`.
- The existing agent WebSocket cannot be reused safely in a browser: browsers
  cannot set its bearer handshake header, and a second connection replaces the
  active CLI or SDK listener.

## Proposed design

### 1. Scoped polling

Add shared web constants for the two agreed refresh cadences:

- Active inbox messages: 10 seconds.
- Shared Pending Review query: 10 seconds.
- Mounted inbox-card unread probes: 15 seconds.

Apply `refreshInterval` only at these SWR call sites. Set
`refreshWhenHidden: false` and `refreshWhenOffline: false` explicitly so the
contract is visible in code rather than relying on library defaults. Existing
`revalidateOnFocus` and `revalidateOnReconnect` behavior remains enabled.

The Review page and `usePendingCount` must continue using the same SWR key and
fetcher. SWR therefore coalesces them into one cache entry and one request per
interval when both are mounted.

Background refresh errors retain the last successful cached result. Existing
initial-load and visible-page error states remain unchanged; polling does not
clear already-rendered messages or counts.

### 2. Canonical status derivation

Refactor the existing message-status helper into the single source of truth for
outbound status labels, tones, and whether a state belongs in a compact thread
row. It accepts both `delivery_status` (the UI's current `status` field) and
`review_status`.

Precedence is:

1. Unresolved or rejected review lifecycle states.
2. Delivery lifecycle state.
3. Review `sent` only when no delivery state is present.

This prevents `review_status="sent"` from masking
`delivery_status="accepted"`, `sending`, or a terminal delivery failure.

| Source state | UI label | Tone | Compact thread row |
|---|---|---|---|
| `review_status=pending_review` | Pending review | warn | yes |
| `review_status=review_rejected` | Rejected | danger | yes |
| `review_status=review_expired_rejected` | Auto-rejected | danger | yes |
| `delivery_status=accepted` | Queued | info | yes |
| `delivery_status=sending` | Sending | info | yes |
| `delivery_status=deferred` | Delayed | warn | yes |
| `delivery_status=failed` | Failed | danger | yes |
| `delivery_status=bounced` | Bounced | danger | yes |
| `delivery_status=complained` | Complaint | danger | yes |
| `delivery_status=sent` | Sent | success | no |
| `delivery_status=delivered` | Delivered | success | no |
| `review_status=review_expired_approved` with no delivery state | Sent (auto) | success | no |
| `review_status=sent` with no delivery state | Sent | success | no |
| unknown non-empty delivery state | unchanged raw value | neutral | no |

Inbound unread/read presentation remains separate and unchanged.

### 3. Thread-list presentation

`ThreadRow` derives the latest message from the thread's existing oldest-to-
newest `messages` array. If that message is outbound, it asks the canonical
status helper for the compact-row status. Attention-worthy states render as the
existing small Loft chip immediately before the timestamp.

Only the latest message drives this row-level indicator. Historical failures
remain visible inside the conversation but do not permanently label a thread
whose later message succeeded. The existing thread-level Pending behavior and
pending-first sorting remain unchanged.

### 4. Conversation presentation

`ThreadBubble` renders a canonical status chip beside the sender metadata for
every outbound message. This includes settled `Sent` and `Delivered` states as
well as transient and failed states. Inbound bubbles receive no new delivery
chip.

The existing pending-review callout, review navigation, delete restrictions,
message-body loading, and attachment behavior remain unchanged.

### 5. Live-update experience

Polling updates the SWR data in place. New messages are grouped and sorted by
the existing pure threading logic, so a newly active thread moves to its normal
position without a full-page reload. Updated delivery states replace their
chips in place.

The sidebar Pending badge and visible unread badges update from their existing
components. The first implementation does not add a persistent "connected"
indicator or toast: polling is an implementation detail, and normal background
refresh should stay visually quiet like Gmail. Existing loading and error UI is
used only when it is already relevant.

## Edge cases and failure handling

- Hidden or offline tabs do not poll. Focus or reconnect triggers an immediate
  revalidation through the existing global SWR policy.
- Concurrent consumers with the same key are deduplicated by SWR.
- A slow request does not create overlapping visible data mutations; SWR keeps
  its normal request deduplication and race handling.
- A background 4xx is not retried by the global error policy. A background 5xx
  retains prior data and uses the existing bounded retry policy.
- Unknown future delivery values remain legible in message detail without
  cluttering the compact thread list.
- An outbound message with no delivery or review status renders no status chip
  rather than incorrectly claiming it was sent.
- Review status wins only for pending/rejected outcomes. A stale or collapsed
  review `sent` value cannot hide a newer delivery state.
- Polling the first 100 rows preserves the existing pagination boundary. This
  feature does not attempt to refresh imperatively loaded older pages.

## Scalability and extensibility notes

Scoped polling avoids repeatedly loading unrelated dashboard resources. The
only N-per-inbox behavior is the existing unread probe on the Inboxes page; a
15-second cadence is acceptable for the current product scale because those
hooks are not mounted elsewhere.

If accounts grow to enough inboxes that unread polling becomes expensive, the
follow-up is an account-level unread-count projection on `GET /v1/agents` or a
dedicated aggregate endpoint. That is deliberately outside this UI-only slice.

The canonical status helper isolates the open delivery vocabulary. New states
can be added in one mapping without changing `ThreadRow` and `ThreadBubble`.
A later server-push transport can invalidate the same SWR keys and remove the
intervals without changing presentation components.

## Verification strategy

### Unit and component tests

- Cover every known status mapping, tone, precedence rule, compact visibility
  rule, empty status, and unknown status.
- Verify `ThreadRow` shows transient/failure states for the latest outbound
  message, omits settled states, and does not surface a historical failure when
  a newer message exists.
- Verify `ThreadBubble` shows settled, transient, review, and failure states on
  outbound messages and no delivery chip on inbound messages.
- Use fake timers or SWR test configuration to verify the active inbox refetches
  at 10 seconds.
- Verify `usePendingCount` and the Review page share the key and refresh at the
  10-second cadence without duplicate visible state.
- Verify unread probes refresh at 15 seconds and retain the existing `99+`
  behavior.
- Verify hidden/offline polling options are disabled at each live-data hook.

### Repository checks

- Run the focused message, inbox-page, pending-count, Review-page, and AgentCard
  Jest tests while iterating.
- Run `npm test` in `web/`.
- Run `npm run lint` in `web/`.
- Run `npm run build` in `web/`.

## Rollout

This is a web-only, backward-compatible change and needs no migration or feature
flag. Deploy with the normal dashboard build. Monitor API request volume and
browser error reporting after release, with particular attention to accounts
that have many inboxes open on the Inboxes page.

## Open questions

None. Polling cadence, status vocabulary, placement, failure behavior, and
scope were approved during design review.
