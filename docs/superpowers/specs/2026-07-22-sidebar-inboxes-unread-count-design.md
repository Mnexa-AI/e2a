# Sidebar Inboxes Unread Count

## Goal

Show the account-wide unread inbound-message total beside the sidebar's
**Inboxes** navigation item so unread mail remains visible from every dashboard
route.

## User experience

- Render a numeric badge beside **Inboxes** when at least one unread inbound
  message exists across the account.
- Match the existing **Pending** navigation badge exactly: the same filled
  `var(--accent)` pill, white bold monospace text, dimensions, and spacing.
- Hide the badge when the total is zero or has not loaded yet.
- Display `99+` when the known total exceeds 99 or any inbox reports more than
  its capped page.
- Poll every 15 seconds only while the page is visible and online, matching the
  existing unread-card cadence.
- Retain the last successful count when a transient refresh fails. An initial
  failure remains visually quiet and renders no badge.

## Data flow

Add one sidebar-owned SWR query under an account-wide unread cache key. Its
fetcher lists the account's agents, requests each agent's existing capped unread
probe, and aggregates `{count, more}` into an account total. This reuses the
current `/v1/agents/{address}/messages?direction=inbound&read_status=unread`
contract and avoids a backend/OpenAPI/client-surface change.

The query is intentionally one owner rather than one hook per navigation item.
The number of HTTP requests remains one agent-list request plus one capped
unread request per inbox. If that fan-out becomes material for large accounts,
the follow-up is a server-side account unread aggregate.

When reading, deleting, restoring, or otherwise changing a message invalidates
an agent's unread cache, the account-wide unread key must also be invalidated so
the sidebar responds immediately instead of waiting for its next poll.

## Components

- `swrKeys.ts`: define the account-wide unread key and include it in unread
  invalidation.
- `useUnreadCount.ts`: own aggregation, `99+` semantics, and the shared polling
  subscription.
- `Sidebar.tsx`: subscribe to the count and render the existing navigation badge
  treatment for **Inboxes** as well as **Pending**.

## Error handling

SWR keeps the last successful value when a refresh rejects. Before the first
successful response, the hook returns an unknown state and the sidebar omits the
badge. The navigation remains usable regardless of unread-probe failures.

## Testing

- Hook tests: aggregate multiple inboxes, hide at zero/unknown, cap at `99+`,
  retain cached data on a transient failure, and refresh every 15 seconds.
- Sidebar tests: show the unread badge only on **Inboxes**, use `99+` when
  capped, and preserve the existing **Pending** badge behavior.
- Cache tests: message-read invalidation revalidates both the per-agent and
  account-wide unread queries.
- Live verification: seed an unread inbound message in the isolated local e2a
  service and confirm the sidebar badge appears without opening the message.

## Non-goals

- No backend or OpenAPI changes.
- No per-inbox unread breakdown in the sidebar.
- No change to when fetching message detail marks an inbound message read.
- No change to the message-row unread highlight.
