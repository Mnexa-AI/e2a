# e2a-sdk-monitor

Continuously exercises the **published** e2a Python SDK against production.

## Why this exists

The two existing continuous validators never touch an SDK. `cmd/e2a-prober`
speaks raw HTTP; `tests/e2e-prod` deliberately uses zero-dependency `fetch`
("no SDK" is a comment in the harness). So SDK regressions — including a
failed or forgotten publish — were invisible to CI. That was not theoretical:
the SDKs sat at 4.0.1 on PyPI/npm for weeks while `main` was at 5.2.0, and a
downstream integration was broken against every fresh install in that window
with nothing to catch it.

This service closes that gap for Python. It lives beside `tests/e2e-prod`
because it is the same kind of thing — a production-targeting harness that is
not part of local dev — rather than under `cmd/`, which is Go binaries only.

## What it exercises

A full agent-to-agent round trip, entirely through the SDK's public surface:

| SDK surface | Where |
| --- | --- |
| `E2AClient(api_key=…, base_url=…)` | client construction |
| `client.messages.send(addr, body)` | `/tick` — outbound leg A → B |
| `construct_event(raw, header, secret)` | `/webhook` — signature verification + envelope parsing |
| `is_email_received(event)` | `/webhook` — event-type narrowing |
| `client.inbound.from_event(event)` | `/webhook` — hydration to `InboundEmail` |
| `email.reply(body, idempotency_key=…)` | `/webhook` — threaded reply B → A |

Anything that breaks any of those — a bad publish, a wire-contract drift, a
packaging regression — stops the success log line.

### Handlers

- **`POST /tick`** (Cloud Scheduler, ~5 min). Mints a nonce, sends
  `probe <nonce>` from agent A to agent B. Returns 202.
- **`POST /webhook`** (e2a delivers here — a real public HTTPS URL). Verifies
  the signature, ignores non-`email.received` types, hydrates the message, then
  branches on which agent received it: delivered to **B** → reply; delivered to
  **A** → that's the reply coming home, emit the success line.
- **`GET /health`** — liveness.

The service is **stateless**. Cloud Run scales to zero, so nothing may live in
memory between requests: the nonce in the subject carries all the correlation
state, and the send timestamp embedded in it yields latency for free.

`http.server` from the stdlib is deliberate — traffic is roughly one request
per five minutes plus the webhook, and a framework would add dependencies that
dilute the "does the SDK install and work" signal this image exists to produce.

### Correlation and stale replies

The nonce is `e2asdkmon.<epoch_ms>.<16 hex>`. The random suffix keeps
concurrent probes distinct; the timestamp gives latency without storage. The
leg is decided by `email.inbox` (the delivered-to agent), not by a `Re:`
prefix, since subjects get rewritten in transit.

A reply whose nonce timestamp is older than `E2A_MONITOR_MAX_AGE_MS`
(default 15 min) logs `sdk_monitor_stale` and **not** `sdk_monitor_ok`, so a
redelivered or long-delayed message can never refresh the freshness alert.
Replies carry `idempotency_key=sdkmon:<event id>` so a webhook redelivery does
not fan out into duplicate sends.

## Log markers

One JSON object per line on stdout. Build the alert on the success marker:

```json
{"event":"sdk_monitor_ok","nonce":"e2asdkmon.1753056000000.…","latency_ms":4231,"message_id":"msg_…"}
```

| `event` | Meaning |
| --- | --- |
| `sdk_monitor_ok` | Round trip completed. **Alert on the absence of this.** `latency_ms` is the metric. |
| `sdk_monitor_tick` | Outbound leg sent. |
| `sdk_monitor_replied` | Inbound leg received and replied to. |
| `sdk_monitor_stale` | A probe arrived past `MAX_AGE_MS`; not a success. |
| `sdk_monitor_error` | Failure, with `stage` = `config` \| `send` \| `signature` \| `hydrate` \| `reply`. |
| `sdk_monitor_start` | Process start. |

Secret values are never logged — only env var *names* on a config failure.

## Configuration

All via environment.

| Var | Required | Default | Notes |
| --- | --- | --- | --- |
| `E2A_API_KEY` | yes | — | Account-scoped key owning both agents. |
| `E2A_MONITOR_AGENT_A` | yes | — | Sender; also receives the reply. |
| `E2A_MONITOR_AGENT_B` | yes | — | Receiver; sends the reply. |
| `E2A_MONITOR_WEBHOOK_SECRET` | yes | — | The `whsec_…` from the subscription. |
| `E2A_BASE_URL` | no | `https://api.e2a.dev` | |
| `E2A_MONITOR_MAX_AGE_MS` | no | `900000` | Stale-reply cutoff. |
| `PORT` | no | `8080` | Cloud Run injects this. |

Missing required vars exit 1 at startup so a misconfigured deploy fails loudly.
The webhook additionally fails closed at request time: an unset secret or a bad
signature is a 401, never an accepted delivery.

## One-time setup (infrastructure, not application code)

The service does **not** create its own webhook subscription — subscriptions
are infrastructure, created once out of band:

1. Create the two agents (A and B) on the monitoring account.
2. Create an account-scoped webhook subscription pointing at the deployed
   service's `https://…/webhook`, via the e2a MCP `create_webhook` tool or the
   dashboard. The `whsec_…` secret is **shown only at creation** — capture it
   then and store it as the deployment's `E2A_MONITOR_WEBHOOK_SECRET`. If it is
   lost, rotate rather than trying to read it back.
3. Point a Cloud Scheduler job at `POST /tick`.

Note the subscription fans out every account event (`email.sent`, bounces,
domain events), not just `email.received` — the handler narrows on its own.

## SDK version bump policy

`requirements.txt` pins an exact published version (currently **5.2.0**). It is
**never** a path or editable dependency on `sdks/python` — installing from
local source would defeat the entire purpose by hiding a broken publish.

Bump the pin as a step of every Python SDK release, *after* the new version is
live on PyPI. Rebuilding the image then proves the release is installable and
works end to end against production. A build failure here is the intended
signal, not something to work around.

## Local checks

```bash
python -m venv venv && ./venv/bin/pip install -r requirements.txt
E2A_MONITOR_WEBHOOK_SECRET=whsec_testsecret ./venv/bin/python test_monitor.py
```

`test_monitor.py` is offline and dependency-free (no pytest): it covers
signature accept/reject, replay rejection, fail-closed on an unset secret,
non-inbound event types, the stale-reply guard, and the success path with a
stubbed hydration. The live round trip is only exercisable against a real
deployment with a reachable webhook URL.
