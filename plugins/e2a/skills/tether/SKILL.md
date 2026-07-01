---
name: tether
description: Beta — Stay in the loop over email during a long-running coding session. The agent sends threaded status updates to your inbox as it sees fit and picks up your emailed replies (questions/instructions) within a few minutes, so you can steer a working agent while AFK. Transport is e2a. Use when you want to walk away from a session but keep commanding it from your inbox.
---

# tether — steer a long-running session from your inbox

> **Beta.** The transport (e2a) and the flow are real; polish (adaptive
> backoff, richer digests) is still open.

`/tether` keeps you connected to a working session over email. The agent emails
you updates **when it judges there's something worth reporting** (not on a
timer, not every turn), and checks your inbox on an interval so your replies —
questions or new instructions — are picked up within a few minutes. Reply
`stop` to end.

## Architecture (why this shape)

Coding agents are turn-based; nothing native listens to an inbox mid-session. So
the two directions use different mechanisms:

- **Send = agent-driven.** The agent calls `tether.sh update "…"` at meaningful
  moments (finished a slice, hit a blocker, needs a decision). Cadence is the
  model's judgment → no per-turn spam, nothing to throttle.
- **Receive = poll-driven.** The agent calls `tether.sh poll` on an interval to
  fetch replies. **Keep the session alive with `/loop`** so polling continues
  while the agent would otherwise be idle — this is the only way a reply sent
  *after* the agent goes idle gets picked up (no hook fires while idle).
- **Blocked-alert = hook (optional).** A `Notification` hook emails you when the
  agent is stuck on a permission prompt and can't proceed at all.

## Setup (once)

```bash
cp "${CLAUDE_PLUGIN_ROOT}/skills/tether/tether.env.example" ~/.e2a-tether.env
chmod 600 ~/.e2a-tether.env    # fill E2A_API_KEY + E2A_AGENT_EMAIL (agent protection OFF)
# optional blocked-alert hook:
"${CLAUDE_PLUGIN_ROOT}/skills/tether/install.sh" --to <repo-root>
```

> The tether agent must have send-side protection / HITL **off**, or each update
> is held as an approval email instead of reaching you. `tether.sh` warns on
> stderr if it sees `pending_review`.

## Runtime flow (what the agent does when `/tether` is invoked)

Let `T="${CLAUDE_PLUGIN_ROOT}/skills/tether/tether.sh"`.

1. **Ask** the user's email address.
2. **Start**: `"$T" start <email>` — sends the intro email, opens the thread, arms.
3. **Work**, and **send updates as you see fit**: `"$T" update "<what changed / what you need>"`.
   Good moments: finished a slice, made a decision that's worth surfacing, hit a
   blocker, or before a long unattended stretch. Skip trivial turns.
4. **Poll on an interval**: run `"$T" poll`; if it prints a reply, treat it as a
   new instruction and act on it (then `update` with the result). To keep polling
   while idle, run the session under **`/loop <interval>`** (or self-schedule the
   next poll). See the interval guidance below.
5. **Stop** when the user replies `stop`/`done`, or the work is complete:
   `"$T" stop`.

## Interval guidance

The poll interval is the reply latency while the agent is idle, traded against
token cost (each tick is a turn). Recommended:

| situation | interval |
|---|---|
| **Attended-ish / expecting a reply soon** (just sent an update) | **2–3 min** |
| **Default** | **3 min** |
| **Fully AFK, latency-tolerant** | **5–10 min** |
| floor (Claude Code `/loop` minimum) | 1 min |

Nice-to-have (adaptive backoff): poll every ~2 min right after sending an
update, then back off toward ~10 min after prolonged silence — responsive when a
reply is likely, cheap when it isn't. While the agent is actively working it also
catches replies at natural checkpoints, so the interval mainly governs the idle
gap.

## Durability note

`/loop` keeps the session alive only while the terminal is open. To keep
listening after you close the laptop, the always-on path is an **e2a webhook
firing a cloud Routine** — but that runs a *fresh* session (loses the live
working context). Left as a follow-on.

## Files

| file | role |
|---|---|
| `tether.sh` | runtime CLI: `start` / `update` / `poll` / `status` / `stop` |
| `lib.sh` | config + e2a send/reply/poll helpers |
| `hooks/tether-notify.sh` | optional Notification hook (blocked-alert) |
| `install.sh` | wire/unwire the Notification hook; `_selftest` |
| `tether.env.example` | credentials template |
