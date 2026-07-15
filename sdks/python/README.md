# e2a Python SDK

Python SDK for [e2a](https://e2a.dev) — email for AI agents. Ships both a
synchronous client (`E2AClient`) and an async one (`AsyncE2AClient`) with an
identical surface.

## Install

```bash
pip install e2a          # add the [ws] extra for client.listen(): pip install "e2a[ws]"
```

The SDK major version tracks the SDK package's own breaking changes and is
independent of the API version path (`/v1`): SDK 5.x targets the e2a v1 API.

## Upgrading to 5.0

5.0 renames the async client and introduces a synchronous client under the
freed name:

- **`E2AClient` → `AsyncE2AClient`** (the 4.x client, unchanged in behavior).
  If you're upgrading from 4.x, the one mechanical change is:

  ```python
  from e2a.v1 import AsyncE2AClient   # was: from e2a.v1 import E2AClient
  ```

- **`E2AClient` is now the synchronous client.** Python convention (httpx,
  openai, anthropic) is plain name = sync client, `Async*` = async client; the
  rename freed the plain name for exactly this. The sync client is a facade
  over `AsyncE2AClient` — same constructor, resources, typed errors,
  retry/idempotency behavior, and pagination semantics, bridged through a
  background event loop, so the two cannot drift.

  ⚠️ 4.x code that still says `E2AClient` will now import successfully but get
  the **sync** client — `await client.messages.send(...)` no longer works on
  it. Calling any sync method from inside a running event loop raises a guiding
  `RuntimeError` ("use AsyncE2AClient"), so the misuse is caught immediately
  rather than deadlocking.

## Upgrading to 4.0

4.0 is a breaking change to the domain DNS-records shape (server #304).
`DomainView.dns_records` is now a single purpose-tagged `list[DNSRecord]`
instead of the old `dns_records.{ mx, txt, dkim }` object (and the separate
`sending_dns_records` list is gone). Each record carries `type`, `name`,
`value`, `priority`, `purpose`, and a per-record `status`. Address records by
`purpose` (`ownership`, `inbound_mx`, `dkim`, `mail_from_mx`, `mail_from_spf`)
rather than `dns_records.mx`/`.txt`/`.dkim` — the MAIL FROM records now live in
the same list. `purpose` and `status` are open sets, so tolerate unknown
values. No other public symbols changed.

## Upgrading from 2.x to 3.0

3.0 is a breaking redesign. The SDK now wraps a generated `/v1` client behind a
namespaced, **async-only** surface, with a typed error hierarchy, automatic
retries + idempotency, and async auto-pagination.

- **Async-only, namespaced.** The sync client and the flat methods are gone.
  `client.get_messages()` → `client.messages.list(address)`,
  `client.get_message(id)` → `client.messages.get(address, id)`,
  `client.send(...)` → `client.messages.send(address, body)`. Per-agent calls
  take an explicit `address`.
- **Webhook verification.** `client.parse` / `client.parse_webhook` /
  `InboundEmail` are removed. Verify and parse a delivery with the standalone
  `construct_event(raw_body, header, secret)`, which returns a typed
  `WebhookEvent`. Signatures are per-webhook (`whsec_…`), Stripe-style.
- **Typed errors.** Failures raise `E2AError` subclasses (`E2ANotFoundError`,
  `E2AConflictError`, `E2AValidationError`, `E2ARateLimitError`, …) carrying
  `.code`, `.status`, `.request_id`, and `.retryable`.

## Quick Start

### Synchronous

```python
from e2a.v1 import E2AClient

# reads E2A_API_KEY; base_url defaults to https://api.e2a.dev
with E2AClient() as client:
    address = "my-agent@agents.e2a.dev"

    # List endpoints return a sync pager: iterate, or collect with a limit.
    for m in client.messages.list(address, read_status="unread"):
        email = client.messages.get(address, m.id)
        print(email.subject)
        client.messages.reply(address, m.id, {"body": "Got it!"})
```

The sync client must not be used from async code — any call made while an
event loop is running in the current thread raises `RuntimeError` pointing you
at `AsyncE2AClient`.

### Async

```python
import asyncio
from e2a.v1 import AsyncE2AClient

async def main():
    # reads E2A_API_KEY; base_url defaults to https://api.e2a.dev
    async with AsyncE2AClient() as client:
        address = "my-agent@agents.e2a.dev"

        # List endpoints return an AutoPager: async-iterate, or collect with a limit.
        async for m in client.messages.list(address, read_status="unread"):
            email = await client.messages.get(address, m.id)
            print(email.subject)
            await client.messages.reply(address, m.id, {"body": "Got it!"})

asyncio.run(main())
```

### Send mail

```python
await client.messages.send(address, {
    "to": ["alice@example.com"],
    "subject": "Hello",
    "body": "Hi from my agent!",
    "html_body": "<p>Hi!</p>",
})
```

The mail-sending writes (`send` / `reply` / `forward` / `approve`) auto-mint an
`Idempotency-Key` and reuse it across retries, so a network blip can't
double-send. Pass a stable key to also survive a process restart:

```python
await client.messages.send(address, body, idempotency_key=derive_from(event))
```

Request bodies accept a plain `dict` (shown above) or the generated model
(`from e2a.v1 import SendEmailRequest`).

### Verify a webhook

Each subscription is signed with its own `whsec_…` secret. `construct_event`
verifies the `X-E2A-Signature` header (replay-protected) and returns a typed
event. **Pass the raw request body** — re-serialized JSON won't match.

```python
from e2a.v1 import construct_event, E2AWebhookSignatureError

@app.post("/webhook")
async def webhook(request):
    try:
        event = construct_event(await request.body(), request.headers["X-E2A-Signature"], SECRET)
    except E2AWebhookSignatureError:
        return Response(status_code=400)
    if event.type == "email.received":
        # metadata-only notification — fetch the full message (body + attachments)
        msg = await client.webhooks.fetch_message(event)
    return {"ok": True}
```

During a rotation you can pass a list of secrets — accepted if any matches:
`construct_event(body, header, [old_secret, new_secret])`.

## Resources

`client.agents`, `client.messages`, `client.conversations`, `client.domains`,
`client.events`, `client.webhooks`, `client.account` (with
`client.account.suppressions`), plus `await client.info()`. Each method maps to
a `/v1` operation; per-agent methods take the agent `address` first.

The sync `E2AClient` exposes the **same resource tree** — drop the `await`.
It mirrors the async client dynamically (every async method is bridged, not
re-implemented), so the two surfaces are identical by construction.

### `AsyncE2AClient(api_key=None, *, base_url=None, max_retries=2, max_elapsed_ms=None, timeout_ms=30000)`

`E2AClient` (sync) takes exactly the same arguments.

`api_key` falls back to `E2A_API_KEY`; `base_url` to `E2A_BASE_URL` then
`https://api.e2a.dev`. `timeout_ms` is the per-request timeout (default 30s); a
timed-out request retries like any other connection failure. Passing
`timeout_ms=0` or `None` removes the SDK's override and falls back to the HTTP
transport's built-in **300s** ceiling — it does **not** make requests unbounded
(this differs from the TypeScript SDK, where `timeoutMs: 0` is fully unbounded).
Use the async client as an async context manager (or call
`await client.aclose()`) and the sync client as a plain context manager (or
call `client.close()`) to close the underlying HTTP connections — for the sync
client this also stops its background event-loop thread. An unclosed sync
client cleans itself up at garbage collection / interpreter exit and never
hangs shutdown, but closing explicitly is preferred.

### Errors

Every failure raises an `E2AError` (or subclass) with `.code`, `.status`,
`.request_id`, `.retryable`: `E2AAuthError` (401), `E2APermissionError` (403),
`E2ANotFoundError` (404), `E2AConflictError` (409), `E2AValidationError` (422),
`E2AIdempotencyError`, `E2ALimitExceededError` (402 — a **quota** cap; not
retryable), `E2ARateLimitError` (429 — a request-**rate** limit; retryable after
`retry_after_seconds`), `E2AServerError` (5xx), `E2AConnectionError` (no
response), `E2AWebhookSignatureError`. The 402/429 split is permanent — branch on
the exception type: 402 → surface a quota/upgrade path, 429 → back off and retry.

> e2a hides the existence of agents you don't own — `agents.get` of an unknown
> address raises `E2APermissionError` (403), not `E2ANotFoundError`.

### Pagination

List methods return an `AutoPager` — async-iterate it, or use
`await pager.to_list(limit=N)` (the limit is required, to bound memory) or
`await pager.for_each(fn)` (return `False` to stop early).

For manual, caller-driven pagination (e.g. checkpoint/resume from a queue), use
`await pager.page(cursor)`: it fetches a SINGLE page and returns a `Page` of
`items` + `next_cursor`. Omit the cursor for the first page and pass the
previous page's `next_cursor` to resume; a `None` `next_cursor` means there are
no more pages.

```python
page = await client.messages.list("bot@agents.e2a.dev", limit=100).page()
process(page.items)
checkpoint(page.next_cursor)  # resume later with .page(saved_cursor)
```

On the sync client the same list methods return a **sync** pager: iterate it
with a plain `for`, and `page(cursor)` / `to_list(limit=N)` / `for_each(fn)`
are ordinary blocking calls with the same semantics.

```python
for m in client.messages.list("bot@agents.e2a.dev"):   # sync iteration
    ...
page = client.messages.list("bot@agents.e2a.dev", limit=100).page()
```

### Trash and restore

Soft-deleted agents and messages remain restorable for about 30 days. List the
trash with `deleted=True`, then restore an item through the same resource. The
sync client exposes the same methods without `await`.

```python
trashed_agents = client.agents.list(deleted=True)
await client.agents.restore("bot@agents.e2a.dev")

trashed_messages = client.messages.list("bot@agents.e2a.dev", deleted=True)
await client.messages.restore("bot@agents.e2a.dev", "msg_abc123")
```

## WebSocket (real-time delivery for local agents)

```python
async for event in client.listen("bot@agents.e2a.dev"):  # falls back to E2A_AGENT_EMAIL
    if event.type != "email.received":
        continue  # tolerate future event kinds
    data = event.data
    email = await client.messages.get(data["delivered_to"], data["message_id"])
```

`client.listen(address)` returns a `WSStream` (async-iterable of `WSEvent` —
the same versioned `{type, id, schema_version, created_at, data}` envelope a
webhook delivery carries) that reconnects with exponential backoff on
transient closes. The server keeps **one connection per agent**: if a newer
connection for the same agent takes over, iteration raises
`E2AConnectionReplacedError` (WS close code `4000 "replaced"`) instead of
reconnecting — reconnecting would steal the socket back and loop. Requires
the `[ws]` extra (`pip install "e2a[ws]"`).

On the sync client, `client.listen(address)` returns a plain iterable instead:

```python
for event in client.listen("bot@agents.e2a.dev"):
    if event.type != "email.received":
        continue  # tolerate future event kinds
    data = event.data
    email = client.messages.get(data["delivered_to"], data["message_id"])
```

Calling `client.close()` from another thread unblocks a pending iteration and
ends the loop cleanly.

## Conversation threading

`conversation_id` is an opaque string that ties multiple emails to one thread
across the email boundary. Pass it on any `send` / `reply` (as a body field) and
e2a surfaces it on the recipient's inbound — via `In-Reply-To` for humans, or a
forge-resistant `X-E2A-Conversation-Id` header for same-platform agent-to-agent
mail. It is not a security boundary; for sender identity check the message's
`auth`. On first contact from a human it arrives `None` — assign one yourself if
you want to thread.

```python
await client.messages.send(address, {
    "to": ["alice@example.com"],
    "subject": "Hello",
    "body": "Hi from my agent!",
    "conversation_id": "thread-42",
})

# Filter an inbox down to a single thread:
async for m in client.messages.list(address, conversation_id="thread-42"):
    ...
```

## License

Apache-2.0 — see [LICENSE](https://github.com/Mnexa-AI/e2a/blob/main/LICENSE) and [NOTICE](https://github.com/Mnexa-AI/e2a/blob/main/NOTICE) in the upstream repo.
