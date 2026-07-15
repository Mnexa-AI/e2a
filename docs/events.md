# Events API & reconciliation

e2a maintains a durable log of every event it emits to webhook subscribers — `email.received`, `email.sent`, the review-hold events (`email.review_requested`, `email.review_approved`, `email.review_rejected`), and the screening events (`email.flagged`, `email.blocked`), among others. The log is queryable via `/v1/events` for 30 days and is the source of truth for replay.

This guide is for customers who:

- Want to **reconcile** their webhook state after their receiver was down.
- Want to **replay** an event manually (one-off recovery).
- Want to **bulk-replay** every event a webhook missed during an outage window.

## Quick start

```bash
# List the most recent events for your account.
curl -H "Authorization: Bearer $E2A_API_KEY" \
  https://api.e2a.dev/v1/events

# Filter by event type and time window.
curl -H "Authorization: Bearer $E2A_API_KEY" \
  "https://api.e2a.dev/v1/events?type=email.received&since=2026-06-01T00:00:00Z"

# Fetch one event in detail (includes delivery_status).
curl -H "Authorization: Bearer $E2A_API_KEY" \
  https://api.e2a.dev/v1/events/evt_abc123
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
from e2a.v1 import AsyncE2AClient
import os

client = AsyncE2AClient(api_key=os.environ["E2A_API_KEY"])
for e in client.events.list(type="email.received", limit=20):
    print(e.id, e.type, e.created_at)
```

## Event types

| Type | When it fires | Guarantee |
|---|---|---|
| `email.received` | Inbound SMTP message accepted | **At-least-once** end-to-end |
| `email.flagged` | Inbound message accepted but did not match the agent's `inbound_policy` (delivered + flagged, never dropped) | **At-least-once** end-to-end |
| `email.sent` | Outbound `/send` accepted by SES | Best-effort |
| `email.failed` | Outbound send terminally failed (retries exhausted / permanent reject) — carries `reason` | **At-least-once** |
| `email.review_requested` | Message held for human review (outbound HITL or inbound screening) — carries `direction` | **At-least-once** |
| `email.review_approved` | Review approved (outbound: sent; inbound: released to the inbox) | Best-effort |
| `email.review_rejected` | Review rejected (outbound: discarded; inbound: dropped) | **At-least-once** |
| `email.blocked` | Message refused by screening (inbound accept-then-quarantine / outbound 403) | **At-least-once** |
| `email.delivered` | Outbound message accepted for delivery by the recipient's server (per-recipient async outcome) | Best-effort |
| `email.bounced` | Outbound message bounced for a recipient (hard/soft delivery failure) | Best-effort |
| `email.complained` | Recipient marked an outbound message as spam (feedback-loop complaint) | Best-effort |
| `domain.suppression_added` | An address was auto-suppressed after a bounce/complaint (account-scoped despite the `domain.` prefix) | Best-effort |
| `domain.sending_verified` | A domain's async SES sending identity reached the verified terminal state | Best-effort |
| `domain.sending_failed` | A domain's async SES sending identity reached a failed terminal state | Best-effort |

The review-hold + screening events (`email.flagged`, `email.blocked`, `email.review_requested`, `email.review_approved`, `email.review_rejected`) are **beta** — their payloads may change before they are declared stable.

## Envelope and typed payloads

Webhook deliveries and WebSocket frames use the canonical OpenAPI
`EventEnvelope` component. Its five core fields are required, while the
envelope and `data` objects remain open for additive fields and future event
types. The REST event log uses `EventJSON`: it contains the same five core
fields and payload, plus REST-only processing/routing fields such as `status`,
`agent_email`, and `delivery_status`. Consumers should not require those
REST-only fields on push deliveries.
(For the WebSocket channel's connection lifecycle — the one-connection-per-agent policy and the frozen close-code
table, including `4000 replaced` — see [api.md → Connection lifecycle & close codes](api.md#connection-lifecycle--close-codes).)

```json
{
  "type": "email.received",
  "id": "evt_62eb7644b075459043c358bc6448d754",
  "schema_version": "1",
  "created_at": "2026-07-01T10:30:00.123456789Z",
  "data": { }
}
```

The server currently emits `schema_version: "1"`. Treat the version as an open
string: a future version and an unknown `type` must still parse into the generic
envelope. SDK stable-event guards narrow `data` only when both the type matches
and `schema_version === "1"`.

`data` is deliberately **open at the envelope level** (no discriminator,
`oneOf`, or closed event enum). OpenAPI publishes the non-constraining
`x-e2a-event-data-schemas` map on `EventEnvelope.data`; it maps each stable
event type to the named schema to validate after inspecting `type`. Unknown or
beta event types must still parse. The stable mapping is:

| Event type | `data` schema | Required fields | Optional fields |
|---|---|---|---|
| `email.received` | `EmailReceivedData` | `message_id`, `agent_email`, `direction` (`inbound`), `from` (display/Reply-To sender), `authenticated_from` (the SPF/DKIM/DMARC-verified identity — gate on THIS), `to[]`, `delivered_to` (scalar — the one per-agent copy; the fetch key), `subject`, `auth_headers{}`, `received_at` | `conversation_id`, `cc[]`, `reply_to[]`, `attachments[]` (metadata only: `filename`, `content_type`, `size_bytes` — the DECODED payload size, `index`) |
| `email.sent` | `EmailSentData` | `message_id`, `agent_email`, `direction` (`outbound`), `provider_message_id`, `method`, `from`, `to[]`, `subject`, `message_type` | `conversation_id`, `cc[]`, `bcc[]` |
| `email.failed` | `EmailFailedData` | `message_id`, `agent_email`, `direction`, `method`, `from`, `to[]`, `subject`, `message_type`, `reason` | `conversation_id`, `cc[]`, `bcc[]`, `reason_code`, `retryable` (present only when genuinely known) |
| `email.delivered` | `EmailDeliveredData` | `message_id`, `agent_email`, `direction`, `delivered_to` (the one recipient this outcome is about) | `subject`, `smtp_detail` |
| `email.bounced` | `EmailBouncedData` | `EmailDeliveredData` fields + `bounce_type` (`permanent` \| `transient` \| `undetermined`, from the SES bounce classification) | `subject`, `smtp_detail`, `bounce_sub_type` (raw SES sub-type, e.g. `General`) |
| `email.complained` | `EmailComplainedData` | `message_id`, `agent_email`, `direction`, `delivered_to` | `subject`, `smtp_detail` |
| `domain.sending_verified` | `DomainSendingVerifiedData` | `domain`, `sending_status` | — |
| `domain.sending_failed` | `DomainSendingFailedData` | `domain`, `sending_status` | `reason` |
| `domain.suppression_added` | `DomainSuppressionAddedData` | `address`, `source` (`bounce` \| `complaint`) | `reason`, `message_id` |

Notes:

- The delivery-outcome events (`email.delivered`/`bounced`/`complained`) carry **no `status` field** — the event type IS the outcome.
- `delivered_to` is always a **scalar**: on `email.received` it's the one per-agent copy (the relay emits one event per delivery); on the delivery-outcome events it's the one recipient the outcome is about. The peer `to`/`cc` lists are the message's parsed headers.
- These shapes are locked by committed golden fixtures (`internal/eventpayload/testdata/`) that the server builders AND both SDKs test against — a payload change is a conscious, reviewed fixture regeneration.
- Every full and minimal stable-event fixture validates twice: once against the
  generic `EventEnvelope`, then against the mapped typed `data` schema. A future
  unknown event/version fixture validates against only the generic envelope.

Every event payload is **metadata only** — it carries identifiers, routing fields, and verdicts, never message content. In particular `email.received` is a notification: it carries the fetch keys plus attachment *metadata* but **not** the body. Fetch the full message — body + attachment bytes — with `client.webhooks.fetch_message(event)` / `webhooks.fetchMessage(event)` (which resolves to `GET /v1/agents/{delivered_to}/messages/{message_id}`). This keeps the fan-out payload bounded and makes the REST resource the single source of truth for content.

"At-least-once" means the event is written to the durable outbox in the same database transaction as the business state, so a process crash between the trigger and webhook fan-out cannot drop the event. "Best-effort" means the outbox write is attempted but a failure logs and continues — used for post-side-effect triggers where the underlying action (SES delivery) has already happened and rolling back would orphan it. See the [design doc](design/2026-06-01-stripe-tier-webhooks.md) §4.2 for the full taxonomy.

## Cursor pagination

`GET /v1/events` returns a `next_cursor` when more pages are available. Pass it back via `?cursor=…` to walk forward in time. Use `since` / `until` (RFC3339) to bracket a specific window — both are optional and stack with the cursor.

```bash
# Get the first page.
RESP=$(curl -s -H "Authorization: Bearer $E2A_API_KEY" \
  "https://api.e2a.dev/v1/events?limit=50")
CURSOR=$(echo "$RESP" | jq -r '.next_cursor')

# Walk forward.
while [ "$CURSOR" != "null" ] && [ -n "$CURSOR" ]; do
  RESP=$(curl -s -H "Authorization: Bearer $E2A_API_KEY" \
    "https://api.e2a.dev/v1/events?limit=50&cursor=$CURSOR")
  # process events…
  CURSOR=$(echo "$RESP" | jq -r '.next_cursor')
done
```

Page size (`limit`) is 1–100 (default 100). Events past the 30-day retention boundary are not returned; querying their id directly returns **410 Gone**.

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
  https://api.e2a.dev/v1/events/evt_abc123/redeliver

# Empty body = replay to every webhook that originally matched.
curl -X POST -H "Authorization: Bearer $E2A_API_KEY" \
  https://api.e2a.dev/v1/events/evt_abc123/redeliver
```

The replay reuses the original event id (`evt_abc123`). Customer-side receivers that dedupe on event id will discard the replay if they've already processed it — by design. **Replay is recovery, not re-delivery.** If you want your handler to run twice for real, you need to call the underlying API again, not replay.

To reconcile a whole window after an outage, walk `GET /v1/events?since=…&until=…` and redeliver each missing event id individually (see the reconciliation flow above).

## Listing, fetching, and replaying events

The event log is driven over the REST API (`GET /v1/events`,
`GET /v1/events/{id}`, `POST /v1/events/{id}/redeliver`) or the MCP tools
(`list_events`, `get_event`, `redeliver_event`) — there is no CLI for events.
The TypeScript / Python SDKs wrap the same endpoints (`client.events.*`).

## Retention and expiry

Events live for **30 days** then drop out of the log. Delivery rows in `webhook_subscriber_deliveries` live longer (90 days post-creation, matching the retry envelope) but become detached from the parent event once it expires — `GET /events/{id}` returns 410 Gone while the delivery history endpoint `GET /webhooks/{id}/deliveries` still shows the row.

Replay requires the source event to still exist. If you need a longer reconciliation window, plumb your own copy into your DB at event-receipt time.

## See also

- [API reference — Webhooks](api.md#webhooks-v1webhooks) — subscriber model, signing, retry policy.
- [Design: Stripe-tier webhooks](design/2026-06-01-stripe-tier-webhooks.md) — full rationale for the outbox + event log architecture.
