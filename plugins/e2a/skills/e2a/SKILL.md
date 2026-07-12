---
name: e2a
description: Use when operating e2a (email for AI agents) over its MCP tools — sending or receiving email, replying in-thread, handling the human-in-the-loop review hold (pending_review), managing agents and custom domains, or working with attachments — OR when integrating e2a into your own software/service (the developer email-API use case: API keys, SDKs, webhooks). With e2a YOU are the agent and the inbox IS the agent (not a human reading their mail). Covers send_message vs reply_to_message threading, multi-agent disambiguation, the custom-domain DNS flow, the protection (screening + review) config, programmatic integration, and common gotchas.
version: 15
---

# Using e2a

<!-- version: 15 -->

e2a is an authenticated email gateway for AI agents. It gives an agent a real email address (`agent@agents.e2a.dev` or `agent@your-domain.com`), verifies sender identity (SPF/DKIM), threads conversations, and optionally holds outbound mail for human review.

## How this fits

This file is the **operate-well manual** — the mental model and gotchas. It assumes you're already connected over MCP (the tools appear as `mcp__e2a__*`). For the things this file deliberately doesn't duplicate:

- **Connect / pick a client / first inbox** → https://e2a.dev/e2a.md
- **Auth (OAuth 2.1 DCR + PKCE, API keys, scopes)** → https://e2a.dev/auth.md
- **Webhook + SDK code (TypeScript / Python, signature verification)** → https://e2a.dev/sdk.md
- **Exact, current tool signatures** → call `tools/list` (authoritative), or the OpenAPI contract at https://e2a.dev/openapi.yaml

The mental model below holds regardless of surface. Tool descriptions teach the precise per-tool contract; this file teaches the model the descriptions assume.

## The mental model

Seven load-bearing facts. Internalize these before you start calling tools.

1. **An agent is an email address.** `support-bot@agents.e2a.dev` is an agent. When you send mail, the recipient sees a message FROM that address — not from "the user." When you list messages, you are reading the agent's own inbox, not the user's personal mail. You are not a secretary; you are the mailbox owner.

2. **Replies preserve threads; new sends do not.** `reply_to_message` carries the `In-Reply-To` and `References` headers from the original message, so the response lands in the same email thread. A fresh `send_message` creates a new thread every time. If a user (or an inbound message) is asking you to respond to something specific, reply with the original `message_id` — even when you could synthesize an equivalent body as a new send. Thread fragmentation is the #1 visible symptom of getting this wrong.

   **Two threading systems, and they don't share a key.** e2a threads on `conversation_id` (it's what `list_conversations`/`get_conversation` group by). The recipient's mail client — Gmail, Outlook, Apple Mail — ignores `conversation_id` entirely and threads on the wire headers instead: `In-Reply-To`/`References` plus a **stable `Subject`**. `reply_to_message` sets those headers correctly, so it threads in *both* systems. A `send_message` — even one you tag with the same `conversation_id` — carries no `References` and lets you pick a new subject: e2a still files it in the same conversation, but the user's inbox shows a *separate* thread. This is the trap — the `conversation_id` looks like it threads because e2a's own views stay tidy, while Gmail splits the exchange in two. Within one ongoing exchange: reply, and keep the subject stable.

3. **`pending_review` is success, not failure.** When the agent's protection config holds outbound mail, a send returns `{ status: "pending_review", message_id: "msg_..." }`. The message was accepted by the server and is being held for a human to review. Do NOT retry. Do NOT report this as an error to the user. Tell them the draft was queued for review, and (if asked) check on it via the pending tools.

4. **Multi-agent accounts need `agent_email` per call.** If the account owns exactly one agent (the common case), tools auto-resolve to it — `whoami` is the cheapest way to confirm. If the account owns more than one, you'll get "agentEmail required." The fix is to enumerate once (`list_agents`), then pass `agent_email` explicitly to subsequent calls. Don't guess; don't pick at random; don't ask the user to pick if context already makes the choice obvious (e.g. they said "my support inbox").

5. **Most users don't need a custom domain — default to the shared one.** Every account can create agents on the shared `agents.e2a.dev` domain with zero DNS setup: `create_agent` with just a local part (e.g. `support-bot`) yields `support-bot@agents.e2a.dev`, live immediately. This is the right default for onboarding and for anyone who doesn't already **own** a domain. Only reach for a custom domain when the user explicitly owns a domain and wants branded addresses — if they don't own one, stay on `agents.e2a.dev` and skip the domain flow entirely. Don't send a user who just wants to get started down the DNS dance.

6. **Custom domains are a two-step async dance.** `register_domain` returns DNS records (MX + TXT) to publish — it does NOT make the domain live. The user (or a DNS-provider MCP, if one is loaded) must add those records out-of-band, wait for DNS propagation (minutes to hours), then `verify_domain`. Verification is idempotent and safe to retry. Until verification succeeds, the domain cannot send or receive mail. Don't promise the user their domain works the moment registration returns.

7. **HITL lives in the protection config.** A new agent has no review hold by default. To turn one on, set the agent's protection posture (see below).

## Common workflows

### First run: onboard a new user before anything else

Before driving any e2a task or harness, check whether this is a **new/unconfigured user** and, if so, get them to a working setup instead of failing partway. One cheap probe answers it: call `whoami`.

- **`whoami` errors with auth/connection failure** → not connected over MCP yet. Point the user at connecting the e2a plugin and completing OAuth: open **https://e2a.dev/e2a.md** (the connect guide) and, in Claude Code, run `/plugin` to authenticate. Interactive auth is theirs to complete — hand them the step, don't try to drive it. Once connected, continue.
- **`whoami` succeeds but the account has no agent** (or `list_agents` is empty) → connected, but no inbox exists. Create their first one on the shared domain with `create_agent` (local part only → `name@agents.e2a.dev`, live immediately — see mental-model fact #5). No custom domain, no DNS.
- **`whoami` returns an agent** → already set up; proceed straight to the task.

The through-line: for a first-time user, **help them stand up a functional e2a setup first** (connect → create a shared-domain inbox), then run the harness — rather than assuming credentials and erroring. Default new inboxes to `agents.e2a.dev`; only involve a custom domain if the user owns one and asks for it.

### Triage the inbox

1. List unread messages with `list_messages` (defaults to `read_status: unread`).
2. Read one fully with `get_message` (the `message_id`).
3. Reply in-thread with `reply_to_message` and that same `message_id`.

For attachment bytes, use `get_attachment` with a 0-based index. It returns the attachment's metadata plus a short-lived `download_url`; pass `inline: true` to get base64 `data` inline for small files. Indexes are stable within a message.

### Send a new email (with HITL awareness)

1. `send_message` with `to`, `subject`, `body`.
2. Check the response:
   - `status: sent` — done.
   - `status: pending_review` — the agent's protection config held it for review; the message is queued. Tell the user it's awaiting review. They can review in the dashboard, via the magic link in their notification email, or with the pending/review tools.

### Templates (beta): recurring sends without free-writing

When the same *kind* of email goes out repeatedly — run reports, digests, approval asks — don't compose it fresh each time. A stored template gives every send the same structure: recurring mail stays consistent, and a HITL reviewer can scan held drafts at a glance (they learn the shape once; only the variables change). Reach for one by the third same-shaped send; keep free-writing for one-offs and conversation.

Three starters are agent-native:

- **`agent-status`** — a run report: what you did, what happened.
- **`approval-request`** — ask a human to approve an action before you take it.
- **`daily-digest`** — a scheduled summary of many items.

(The catalog — `list_starter_templates` — also has `welcome`, `verify-code`, `password-reset`, `receipt` for product mail.)

The flow is copy once, send many:

1. `create_template` with `{ "from_starter": "agent-status", "alias": "run-report" }` — copies the starter verbatim into the account's library (account scope; once at setup). Customize the copy later with `update_template` if needed.
2. Send by alias — no literal subject/body (a template reference is mutually exclusive with them); the server renders **before** any review hold, so the reviewer sees final content:

```json
{ "to": ["owner@acme.com"], "template_alias": "run-report",
  "template_data": { "agent_name": "deploy-bot", "status": "success",
                     "summary": "3 services deployed", "sections_html": "<ul><li>api: ok</li></ul>" } }
```

Syntax is a flat Mustache subset: `{{var}}` (HTML-escaped in the HTML part), `{{{var}}}` raw, dot paths into nested data — no loops or conditionals. **Missing variables render as empty strings, silently.** Preview with `validate_template` (its `suggestedData` names every variable the source references) instead of discovering blanks in sent mail. List/table content goes through raw `{{{…_html}}}` fragment slots: you build the HTML fragment, and you must HTML-escape any user-supplied text inside it — raw slots bypass escaping.

**Approval links must be confirmation pages.** For `approval-request`, `approve_url` / `reject_url` must point to pages that require an explicit human click to act — never state-changing GET endpoints. Email security scanners prefetch every link in a message, so a GET-to-approve URL gets "approved" by a robot before the human ever opens the mail.

Templates are beta: shapes may change before they're declared stable. Only `send_message` takes template references — reply and forward don't.

### Review held messages (account scope)

Holds — outbound drafts and screened inbound — are an **account-owner / human** action, never agent self-approval. With an account-scoped credential you can:

- `list_reviews` / `get_review` — see what's held (runtime scope can view its own review queue).
- `approve_review` / `reject_review` — release or discard a hold. These are **account/admin scope**; an agent-scoped credential is 403'd (releasing your own held outbound would defeat the review gate).

For outbound, approving *is* sending. For inbound, approving releases the message into the inbox.

### Turn on a review hold for an agent

Posture lives on the protection sub-resource — `update_protection` (MCP) / `PUT /v1/agents/{email}/protection`. The config has three required top-level keys:

```json
{
  "inbound":  { "gate": { "policy": "open" }, "scan": { "sensitivity": "off" } },
  "outbound": { "gate": { "policy": "open", "action": "review" }, "scan": { "sensitivity": "off" } },
  "holds":    { "ttl_seconds": 604800, "on_expiry": "reject" }
}
```

- A direction's `gate.action` is what a non-match does: `flag` (deliver + annotate), `review` (hold), or `block`. Set `outbound.gate.action: "review"` (or turn on a content `scan`) to hold outbound mail for approval.
- `holds.ttl_seconds` is how long a hold waits; `holds.on_expiry` is `approve` or `reject` when the TTL fires.
- Read the current posture with `get_protection`. Both are account-scope only. Confirm the exact shape with `tools/list` / the OpenAPI contract — the protection config is beta and may change.

### Add a custom domain (e.g. `mail.acme.com`)

**First: does the user actually own this domain?** If they just want to get started and don't own a domain, skip this entirely — create the agent on the shared `agents.e2a.dev` (mental-model fact #5), which is live with no DNS. Only run the flow below when the user owns the domain and wants branded addresses.

1. `register_domain` with the FQDN — returns MX + TXT records and an unverified domain row.
2. Hand the records to the user (or to a DNS-provider MCP — Cloudflare, Route 53, etc. — if one is loaded; call its `create_dns_record`-style tool with the returned values).
3. Wait. DNS propagation is asynchronous — minutes typically, occasionally hours.
4. `verify_domain` with the same FQDN. If it returns `verified: true`, the domain is live. If still false, the response shows what DNS state was resolved so the user can debug. Retry as needed.
5. Once verified, agents can be created on (or moved to) that domain.

### Receive mail in your own backend (webhooks)

If the user is building a service that handles inbound mail in their own code, that's an SDK/webhook job, not an MCP one. Subscribe a webhook (`create_webhook`, a separate `/v1/webhooks` resource — NOT a per-agent "mode") and verify deliveries with the per-webhook `whsec_…` secret returned once at creation. The full handler code (FastAPI / Express, `construct_event` / `constructEvent`) is at https://e2a.dev/sdk.md — defer to it rather than reconstructing it here.

## Integrating e2a into software

Everything above assumes **you** are the agent operating an inbox over MCP. But e2a is also a plain email API any software can build on — the "send and receive email from code" use case, except every address is an authenticated *agent* identity, with optional human-in-the-loop review. When a user asks you to **integrate e2a into their app, service, or agent framework**, you're helping them write code against the REST API with an API key — not driving these MCP tools. Make the pivot explicit:

- **MCP** (these tools) — interactive; the agent itself reads/sends mail; auth is OAuth; no integration code. This is you, right now.
- **SDK / REST API** — programmatic; the user's software sends/receives mail; auth is an **API key**. This is what an integration uses.

The mental model in this skill carries over unchanged: the REST/SDK responses use the same `status`, `message_id`, `pending_review` hold, and reply-threading semantics the MCP tools do — only the transport and auth differ.

### Set it up

1. **Issue an API key and choose its scope.** Programmatic access authenticates with a key sent as `Authorization: Bearer e2a_…` (not the OAuth flow MCP uses). Scope is the load-bearing decision:
   - **account** — workspace admin: provision agents, domains, webhooks, and approve/reject review holds. Use for a backend that manages inboxes or drives the HITL queue.
   - **agent** — bound to one inbox (`agent` email at creation): send / read / reply for that identity only. Use for a service that *is* a single sender.

   The secret is returned **once** at creation — store it in the app's secret manager, never in source. (Keys + scopes: https://e2a.dev/auth.md.)

2. **Have an agent identity to send as.** Mail goes out FROM an agent address — `name@agents.e2a.dev` out of the box, or `name@their-domain.com` after the custom-domain verify dance (see "Add a custom domain" above). Create it once (`create_agent` / `POST /v1/agents`) or reuse an existing one.

3. **Send from code.** `POST /v1/agents/{email}/messages` to send, `…/messages/{id}/reply` to reply in-thread — or the equivalent helper in the TypeScript (`@e2a/sdk`) or Python SDK. A `pending_review` response is an accepted-and-held message, **not** an error, exactly as over MCP — surface it to the user as "queued for review," don't retry.

4. **Receive from code (optional).** To handle inbound mail or delivery/review *events* in their backend, subscribe a webhook (`create_webhook` / `POST /v1/webhooks`) and verify every POST with the per-webhook `whsec_…` secret — see "Receive mail in your own backend" above. Don't poll the API for new mail.

The full, current integration code — SDK install, send / reply / parse, webhook handlers with signature verification — lives in the docs, not here. Point the user at:

- SDK + webhook code (TypeScript / Python): https://e2a.dev/sdk.md
- Auth (API keys, scopes, OAuth): https://e2a.dev/auth.md
- REST contract: https://e2a.dev/openapi.yaml

## Gotchas

- **Don't encode raw text as base64 yourself for attachments.** The `data` field expects base64 produced by another tool (a file reader, a doc generator, `get_attachment`). If you have plain text and want to attach it, write it to a file first and read it back, or generate the encoding via a Bash call — don't construct base64 from a Markdown string in your head.
- **Forwarding attachments is a verbatim copy.** Pass the `{filename, content_type, data}` tuple from `get_attachment` straight into the next send's `attachments[]`. No re-encoding, no re-naming necessary.
- **`get_message` deliberately omits raw MIME and attachment bytes.** Don't ask for the "full message" — you have what you need (decoded text/html bodies, headers, attachment metadata). Use `get_attachment` for actual bytes when you need them.
- **Destructive ops require `confirm: true`.** `delete_agent` and `delete_domain` refuse without explicit confirmation. This is a guard against hallucinated deletes; pass it only when the user has clearly asked for the destructive action.
- **`approve_review` with `attachments: []` strips attachments.** An omitted `attachments` field keeps the original draft's attachments; an explicit empty array removes them. Same shape applies to other override fields — omit to keep, specify (including empty) to override.
- **Held bodies are scrubbed after the terminal transition.** `get_review` returns the full body only while status is `pending_review`. Once it reaches a terminal state (`sent`, `review_rejected`, `review_expired_approved`, `review_expired_rejected`), body columns are wiped server-side for compliance.
- **Token expiry on OAuth flows.** The hosted MCP runs over OAuth; if a tool starts erroring with auth failures across multiple calls, the refresh token has likely expired — re-auth via `/plugin` in Claude Code.

## When NOT to use a tool

- Don't send a fresh message to respond to something in the inbox — reply (threading).
- Don't loop on the pending list waiting for an approval — there's no event in MCP; let the user drive when they want to check.
- Don't verify a custom domain immediately after registering it — DNS has not propagated. If the user wants a verification check, call it once and report the result; don't poll.
- Don't delete agents or domains from inferred intent. Require the user to say it.
- Don't enumerate agents on every turn. `whoami` is cheaper for the common single-agent case; `list_agents` is only needed when `whoami` errors with the multi-agent diagnostic.

## Reference

- Connect / clients / first inbox: https://e2a.dev/e2a.md
- Auth (OAuth 2.1 DCR + PKCE, API keys, scopes): https://e2a.dev/auth.md
- Webhook + SDK code: https://e2a.dev/sdk.md
- Exact tool signatures: call `tools/list` (authoritative).
- OpenAPI contract: https://e2a.dev/openapi.yaml
- The MCP surface is **45 tools** (14 runtime/inbox + 31 admin/setup) spanning agents, messages, HITL review, attachments, domains, events, webhooks, and templates (beta). The set you see depends on your credential's scope: an agent-scoped credential sees the 14 runtime tools; an account-scoped credential sees all 45. Tool descriptions teach behavior; this skill teaches the mental model.
- Plugin homepage / docs index: https://e2a.dev (machine-readable index: https://e2a.dev/llms.txt)
