---
description: Mental model and common workflows for the e2a email plugin (send/receive email, HITL approval, agent + domain management). Read this before driving the e2a MCP tools — pretraining assumes "human reading their inbox", but with e2a YOU are the agent and the inbox IS the agent. Covers send_email vs reply_to_message threading, the pending_approval HITL status, multi-agent disambiguation, custom-domain DNS flow, and other gotchas.
---

# Using the e2a plugin

e2a is an authenticated email gateway for AI agents. When this plugin is active, you have 18 MCP tools (prefixed in the menu as `mcp__e2a__*`) for sending mail, reading the inbox, approving held outbound messages, and managing agents + custom domains.

This skill exists because the most common failure mode with e2a is the wrong mental model — pretraining strongly biases toward "a human is reading their inbox and asking me to draft a reply." With e2a, **you ARE the agent and the agent IS the email address**. Treat the tool calls accordingly.

## The mental model

Six load-bearing facts. Internalize these before you start calling tools.

1. **An agent is an email address.** `support-bot@agents.e2a.dev` is an agent. When you call `send_email`, the recipient sees a message FROM that address — not from "the user." When you call `list_messages`, you are reading the agent's own inbox, not the user's personal mail. You are not a secretary; you are the mailbox owner.

2. **Replies preserve threads; new sends do not.** `reply_to_message` carries the `In-Reply-To` and `References` headers from the original message, so the response lands in the same email thread. `send_email` creates a fresh thread every time. If a user (or an inbound message) is asking you to respond to something specific, use `reply_to_message` with the original `message_id` — even when you could synthesize an equivalent body with `send_email`. Thread fragmentation is the #1 visible symptom of getting this wrong.

3. **`pending_approval` is success, not failure.** When the agent has HITL enabled, `send_email` and `reply_to_message` return `{ status: "pending_approval", message_id: "msg_..." }`. The message was accepted by the server and is being held for a human to review. Do NOT retry. Do NOT report this as an error to the user. Tell them the draft was queued for approval, and (if asked) check on it with `list_pending_messages` or `get_pending_message`.

4. **Multi-agent accounts need `agent_email` per call.** If the account owns exactly one agent (the common case), every tool auto-resolves to that one and you can omit `agent_email`. If the account owns more than one, tools error with "agentEmail required." The fix is `list_agents` once to enumerate, then pass `agent_email` explicitly to subsequent calls. Don't guess; don't pick at random; don't ask the user to pick if context already makes the choice obvious (e.g. they said "my support inbox").

5. **Custom domains are a two-step async dance.** `register_domain` returns DNS records (MX + TXT) to publish — it does NOT make the domain live. The user (or a DNS-provider MCP, if one is loaded) must add those records out-of-band, wait for DNS propagation (minutes to hours), then call `verify_domain`. `verify_domain` is idempotent; safe to retry. Until verification succeeds, the domain cannot send or receive mail. Don't promise the user their domain works the moment `register_domain` returns.

6. **HITL is not in the consent flow — toggle it with `update_agent`.** Creating a new agent does not enable HITL. To turn on approval gates for an existing agent, call `update_agent` with `hitl_enabled: true` (optionally with `hitl_ttl_seconds` and `hitl_expiration_action`). Same path applies to disabling it.

## Common workflows

### Triage the inbox

1. `list_messages` (defaults to `status: unread`) — get summaries.
2. `get_message` with one `message_id` — full body, headers, attachment metadata.
3. `reply_to_message` with that same `message_id` — sends a threaded reply.

Use `get_attachment_data` only when you need the actual bytes of one attachment — to inspect, forward, or hand off. Attachment indexes are 0-based and stable within a message.

### Send a new email (with HITL awareness)

1. `send_email` with `to`, `subject`, `body`.
2. If the response is `status: sent` — done.
3. If the response is `status: pending_approval` — the agent has HITL on, the message is queued. Tell the user it's awaiting review. They can review in the dashboard, via the magic link in their notification email, or through these tools:
   - `list_pending_messages` to see what's waiting
   - `get_pending_message` for the full draft of one
   - `approve_pending_message` to send (optionally with edits)
   - `reject_pending_message` to discard

### Onboard a new agent

1. `whoami` — find out the current default. Errors here usually mean the account has 0 or 2+ agents.
2. `create_agent` with a `slug` (e.g. `support-bot`) — registers `<slug>@<shared-domain>`. Defaults to `local` mode (poll-based delivery — fine for MCP/CLI clients). Use `agent_mode: "cloud"` with `webhook_url` only if there's a real HTTPS endpoint to receive pushes.
3. `update_agent` with `hitl_enabled: true` if the user wants approval gates — this is a separate step. Skip otherwise.

### Add a custom domain (e.g. `mail.acme.com`)

1. `register_domain` with the FQDN — returns MX + TXT records and an unverified domain row.
2. Hand the records to the user (or to a DNS-provider MCP — Cloudflare, Route 53, etc. — if one is loaded; call its `create_dns_record`-style tool with the returned values).
3. Wait. DNS propagation is asynchronous — minutes typically, occasionally hours.
4. `verify_domain` with the same FQDN. If it returns `verified: true`, the domain is live. If still false, the response shows what DNS state was resolved so the user can debug. Retry as needed.
5. Once verified, `create_agent` will accept agents on that domain (or update existing agents to use it).

## Gotchas

- **Don't encode raw text as base64 yourself for attachments.** The `data` field expects base64 produced by another tool (a file reader, a doc generator, `get_attachment_data`). If you have plain text and want to attach it, write it to a file first and read it back, or generate the encoding via a Bash call — don't try to construct base64 from a Markdown string in your head.
- **Forwarding attachments is a verbatim copy.** Pass the `{filename, content_type, data}` tuple from `get_attachment_data` straight into `send_email`'s or `reply_to_message`'s `attachments[]`. No re-encoding, no re-naming necessary.
- **`get_message` deliberately omits raw MIME and attachment bytes.** Don't ask for the "full message" — you have what you need (decoded text/html bodies, headers, attachment metadata). Use `get_attachment_data` for actual bytes of one attachment when you need them.
- **Destructive ops require `confirm: true`.** `delete_agent` and `delete_domain` both refuse without it. This is a guard against hallucinated deletes; pass it explicitly only when the user has clearly asked for the destructive action.
- **`approve_pending_message` with `attachments: []` strips attachments.** An omitted `attachments` field keeps the original draft's attachments; an explicit empty array removes them. Same shape applies to other override fields — omit to keep, specify (including empty) to override.
- **HITL approval bodies are scrubbed after the terminal transition.** `get_pending_message` returns the full body only while status is `pending_approval`. Once approved or rejected, body columns are wiped server-side for compliance.
- **Token expiry on OAuth flows.** The hosted MCP runs over OAuth; if a tool starts erroring with auth failures across multiple calls, the refresh token has likely expired and the user needs to re-auth via `/plugin` in Claude Code.

## When NOT to use a tool

- Don't call `send_email` to respond to an inbound message you can see in `list_messages` — use `reply_to_message` (threading).
- Don't loop on `list_pending_messages` waiting for an approval — there's no event in MCP; let the user drive when they want to check.
- Don't call `verify_domain` immediately after `register_domain` — DNS has not propagated. If the user wants a verification check, call it once and report the result; don't poll.
- Don't call `delete_agent` or `delete_domain` from inferred intent. Require the user to say it.
- Don't list agents on every turn. `whoami` is cheaper for the common single-agent case; `list_agents` is only needed when `whoami` errors out with the multi-agent diagnostic.

## Reference

- Plugin homepage: https://e2a.dev
- 18 MCP tools: agents (5), messages (5), HITL (4), domains (4) — plus `get_attachment_data` shared.
- Tool descriptions teach behavior; this skill teaches the mental model. When in doubt, read the tool's own `description` for the precise contract.
