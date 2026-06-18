# e2a Python SDK

Async Python SDK for [e2a](https://e2a.dev) — email for AI agents.

## Install

```bash
pip install e2a          # add the [ws] extra for client.listen(): pip install "e2a[ws]"
```

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

```python
import asyncio
from e2a.v1 import E2AClient

async def main():
    # reads E2A_API_KEY; base_url defaults to https://api.e2a.dev
    async with E2AClient() as client:
        address = "my-agent@agents.e2a.dev"

        # List endpoints return an AutoPager: async-iterate, or collect with a limit.
        async for m in client.messages.list(address, status="unread"):
            email = await client.messages.get(address, m.message_id)
            print(email.subject)
            await client.messages.reply(address, m.message_id, {"body": "Got it!"})

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
        ...  # event.data carries the message payload
    return {"ok": True}
```

During a rotation you can pass a list of secrets — accepted if any matches:
`construct_event(body, header, [old_secret, new_secret])`.

## Resources

`client.agents`, `client.messages`, `client.conversations`, `client.domains`,
`client.events`, `client.webhooks`, `client.account` (with
`client.account.suppressions`), plus `await client.info()`. Each method maps to
a `/v1` operation; per-agent methods take the agent `address` first.

### `E2AClient(api_key=None, *, base_url=None, max_retries=2, max_elapsed_ms=None)`

`api_key` falls back to `E2A_API_KEY`; `base_url` to `E2A_BASE_URL` then
`https://api.e2a.dev`. Use it as an async context manager (or call
`await client.aclose()`) to close the underlying HTTP connections.

### Errors

Every failure raises an `E2AError` (or subclass) with `.code`, `.status`,
`.request_id`, `.retryable`: `E2AAuthError` (401), `E2APermissionError` (403),
`E2ANotFoundError` (404), `E2AConflictError` (409), `E2AValidationError` (422),
`E2AIdempotencyError`, `E2ARateLimitError` (429), `E2AServerError` (5xx),
`E2AConnectionError` (no response), `E2AWebhookSignatureError`.

> e2a hides the existence of agents you don't own — `agents.get` of an unknown
> address raises `E2APermissionError` (403), not `E2ANotFoundError`.

### Pagination

List methods return an `AutoPager` — async-iterate it, or use
`await pager.to_list(limit=N)` (the limit is required, to bound memory) or
`await pager.for_each(fn)` (return `False` to stop early).

## WebSocket (real-time delivery for local agents)

```python
async for notif in client.listen("bot@agents.e2a.dev"):  # falls back to E2A_AGENT_EMAIL
    email = await client.messages.get(notif.recipient, notif.message_id)
```

`client.listen(address)` returns a `WSStream` (async-iterable of
`WSNotification`) that reconnects with exponential backoff. Requires the `[ws]`
extra (`pip install "e2a[ws]"`).

## License

Apache-2.0 — see [LICENSE](https://github.com/Mnexa-AI/e2a/blob/main/LICENSE) and [NOTICE](https://github.com/Mnexa-AI/e2a/blob/main/NOTICE) in the upstream repo.
