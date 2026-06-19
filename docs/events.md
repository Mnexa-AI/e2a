# Events API & reconciliation

e2a maintains a durable log of every event it emits to webhook subscribers — `email.received`, `email.sent`, `email.pending_approval`, `email.approved`, `email.rejected`. The log is queryable via `/v1/events` for 30 days and is the source of truth for replay.

This guide is for customers who:

- Want to **reconcile** their webhook state after their receiver was down.
- Want to **replay** an event manually (one-off recovery).
- Want to **bulk-replay** every event a webhook missed during an outage window.

## Quick start

```bash
# List the most recent events for your account.
curl -H "Authorization: Bearer $E2A_API_KEY" \
  https://e2a.dev/v1/events

# Filter by event type and time window.
curl -H "Authorization: Bearer $E2A_API_KEY" \
  "https://e2a.dev/v1/events?type=email.received&since=2026-06-01T00:00:00Z"

# Fetch one event in detail (includes delivery_status).
curl -H "Authorization: Bearer $E2A_API_KEY" \
  https://e2a.dev/v1/events/evt_abc123
```

In TypeScript:

```ts
import { E2AClient } from "@e2a/sdk/v1";
const client = new E2AClient({ apiKey: process.env.E2A_API_KEY });

// Walk the last 24h of email.received events.
const events = await client.events
  .list({
    type: "email.received",
    since: new Date(Date.now() - 24 * 3600 * 1000).toISOString(),
  })
  .toArray(); // auto-pages via next_cursor
for (const e of events) console.log(e.id, e.type, e.created_at);
```

In Python:

```python
from e2a.v1 import E2AClient
import os

client = E2AClient(api_key=os.environ["E2A_API_KEY"])
for e in client.events.list(type="email.received", limit=20):
    print(e.id, e.type, e.created_at)
```

## Event types

| Type | When it fires | Guarantee |
|---|---|---|
| `email.received` | Inbound SMTP message accepted | **At-least-once** end-to-end |
| `email.flagged` | Inbound message accepted but did not match the agent's `inbound_policy` (delivered + flagged, never dropped) | **At-least-once** end-to-end |
| `email.sent` | Outbound `/send` accepted by SES | Best-effort |
| `email.pending_approval` | HITL-gated message held for human review | **At-least-once** |
| `email.approved` | Reviewer approved + SES accepted | Best-effort |
| `email.rejected` | Reviewer rejected (no SES involvement) | **At-least-once** |

"At-least-once" means the event is written to the durable outbox in the same database transaction as the business state, so a process crash between the trigger and webhook fan-out cannot drop the event. "Best-effort" means the outbox write is attempted but a failure logs and continues — used for post-side-effect triggers where the underlying action (SES delivery) has already happened and rolling back would orphan it. See the [design doc](design/2026-06-01-stripe-tier-webhooks.md) §4.2 for the full taxonomy.

## Cursor pagination

`GET /v1/events` returns a `next_cursor` when more pages are available. Pass it back via `?cursor=…` to walk forward in time. Use `since` / `until` (RFC3339) to bracket a specific window — both are optional and stack with the cursor.

```bash
# Get the first page.
RESP=$(curl -s -H "Authorization: Bearer $E2A_API_KEY" \
  "https://e2a.dev/v1/events?limit=50")
CURSOR=$(echo "$RESP" | jq -r '.next_cursor')

# Walk forward.
while [ "$CURSOR" != "null" ] && [ -n "$CURSOR" ]; do
  RESP=$(curl -s -H "Authorization: Bearer $E2A_API_KEY" \
    "https://e2a.dev/v1/events?limit=50&cursor=$CURSOR")
  # process events…
  CURSOR=$(echo "$RESP" | jq -r '.next_cursor')
done
```

Page size (`limit`) is 1–200 (default 50). Events past the 30-day retention boundary are not returned; querying their id directly returns **410 Gone**.

## Reconciliation pattern

If your webhook receiver went down for an hour, the steps to reconcile are:

1. Pick a `since` timestamp covering the outage (with a buffer).
2. List events filtered to the affected window — `GET /events?since=…&until=…`.
3. Compare event IDs against what your receiver actually processed.
4. For any gaps, use `POST /events/{id}/redeliver` to re-fire one event.

```python
# Pseudocode for the reconciliation flow.
processed = set(load_processed_event_ids_from_my_db())
for e in client.events.list(since=outage_start, until=outage_end):
    if e.id not in processed:
        client.events.redeliver(e.id, webhook_id="wh_my_handler")
```

## Replay

### Per-event replay

```bash
# Replay event evt_abc123 to one specific webhook.
curl -X POST -H "Authorization: Bearer $E2A_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"webhook_id": "wh_aaa"}' \
  https://e2a.dev/v1/events/evt_abc123/redeliver

# Empty body = replay to every webhook that originally matched.
curl -X POST -H "Authorization: Bearer $E2A_API_KEY" \
  https://e2a.dev/v1/events/evt_abc123/redeliver
```

The replay reuses the original event id (`evt_abc123`). Customer-side receivers that dedupe on event id will discard the replay if they've already processed it — by design. **Replay is recovery, not re-delivery.** If you want your handler to run twice for real, you need to call the underlying API again, not replay.

To reconcile a whole window after an outage, walk `GET /v1/events?since=…&until=…` and redeliver each missing event id individually (see the reconciliation flow above).

## CLI

```bash
e2a events list --type email.received --since 2026-06-01T00:00:00Z --limit 100
e2a events get evt_abc123
e2a events redeliver evt_abc123 --webhook wh_aaa
```

The CLI uses the same Bearer-token auth as the API.

## Retention and expiry

Events live for **30 days** then drop out of the log. Delivery rows in `webhook_subscriber_deliveries` live longer (90 days post-creation, matching the retry envelope) but become detached from the parent event once it expires — `GET /events/{id}` returns 410 Gone while the delivery history endpoint `GET /webhooks/{id}/deliveries` still shows the row.

Replay requires the source event to still exist. If you need a longer reconciliation window, plumb your own copy into your DB at event-receipt time.

## See also

- [API reference — Webhooks](api.md#webhook-signing-secrets) — subscriber model, signing, retry policy.
- [Design: Stripe-tier webhooks](design/2026-06-01-stripe-tier-webhooks.md) — full rationale for the outbox + event log architecture.
