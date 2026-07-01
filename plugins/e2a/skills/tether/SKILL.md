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
- **Receive = poll-driven.** The agent runs `tether.sh listen` — a cheap,
  curl-only poll loop (no LLM tokens per check) that runs for the duration set at
  `start --for`. It wakes the agent *only* when a reply actually arrives (or the
  window ends). See **Durability tiers** for keeping it alive across idle/sleep.
- **Questions = ask by email.** When the agent needs a decision or clarification
  from you, it must **not** use a terminal prompt / AskUserQuestion — you're AFK
  and can't see it, which would stall the whole session. It calls
  `tether.sh ask "<question>"`, which emails the question into the thread and
  **blocks until you reply**, then prints your answer. This is the hard rule:
  **while tethered, every question goes over email, never the terminal.**
- **Blocked-alert = hook (optional).** A `Notification` hook emails you when the
  agent is stuck on a *permission prompt* it can't proceed past. Note: an emailed
  reply cannot answer a CLI permission prompt (there's no native way to inject
  approval), so for unattended runs **pre-authorize** the tools the session needs
  (a permission allowlist / a less-prompting mode). The hook is the safety net,
  not the approval channel.

## Setup (once)

Tether needs an **agent-scoped** e2a API key (`e2a_agt_…`) bound to a single
inbox — least privilege, so a leaked key can't touch the rest of the account.

1. **Get an agent-scoped API key from the e2a website:**
   **https://e2a.dev/api-keys** — create or pick an agent and generate a key
   scoped to it. Note the agent's email address too.
2. **Save the credentials:**
   ```bash
   cp "${CLAUDE_PLUGIN_ROOT}/skills/tether/tether.env.example" ~/.e2a-tether.env
   chmod 600 ~/.e2a-tether.env   # fill E2A_API_KEY (e2a_agt_…) + E2A_AGENT_EMAIL
   ```
3. **(Optional) blocked-alert hook:**
   ```bash
   "${CLAUDE_PLUGIN_ROOT}/skills/tether/install.sh" --to <repo-root>
   ```

Credentials resolve in order: explicit env vars → `~/.e2a-tether.env` →
`~/.e2a/config.json`. (That last one, written by `e2a login`, is an
**account-scoped** key — broader than tether needs; prefer the agent key above.
A smoother CLI path is coming.)

> The tether agent must have send-side protection / HITL **off**, or each update
> is held as an approval email instead of reaching you. `tether.sh` warns on
> stderr if it sees `pending_review`.

## Runtime flow (what the agent does when `/tether` is invoked)

Let `T="${CLAUDE_PLUGIN_ROOT}/skills/tether/tether.sh"`.

1. **Ask** the user's email address **and how long to stay tethered** (e.g. 30m,
   2h, 8h/overnight, or until they say stop). They're present at this step, so a
   normal question is fine.
2. **Start**: `"$T" start <email> --for <duration>` (or `--until <ISO>`; omit
   both for until-stop) — sends the intro, opens the thread, arms, records the window.
3. **Work**, and **send updates as you see fit**: `"$T" update "<what changed / what you need>"`.
   Good moments: finished a slice, made a decision that's worth surfacing, hit a
   blocker, or before a long unattended stretch. Skip trivial turns. For a rich
   update (diagram, table, formatting), write the HTML to a file and run
   `"$T" update --html <file>` — a plain-text fallback is auto-derived (or pass
   `--text "<fallback>"`).
4. **Need a decision from the user? Ask by email — never the terminal.** Run
   `"$T" ask "<question>"` (in the background); it emails the question and blocks
   until the user replies, then prints the answer. **Do not** use AskUserQuestion
   or a bare terminal prompt while tethered — an AFK user can't answer it and the
   session stalls.
5. **Listen for the whole window**: run `"$T" listen` **in the background**. It
   polls cheaply (curl, no tokens) and exits with either:
   - `REPLY_RECEIVED:` + the message → act on it (then `update` with the result),
     and **relaunch `listen`** for the remaining window; or
   - `TETHER_EXPIRED` → the window is up; run `"$T" stop`.
   Replies are deduped by message-id and survive e2a's async parse, so none are
   dropped or repeated. (`poll` is the same one-shot check if you want it manually.)
6. **Stop** when the user replies `stop`/`done`, the window expires, or the work
   is complete: `"$T" stop`.

## Writing good emails

The recipient is a **person reading email (often on a phone)**, not a terminal.
Write for that medium, not for a CLI.

**Plain text (default — use for most updates):**
- Lead with the takeaway (what changed / what you need), then details. Keep it
  short and scannable.
- **No markdown** — `**bold**`, `` `code` ``, `#` headings render as literal
  characters in a plain-text email. Use plain prose and simple `-` bullets.
- Be concrete: name the file / PR / decision ("merged #357"), not "did some work".
- If you need something, end with **one clear ask** ("Reply A or B?").
- No large code/log dumps — summarize or link. Don't paste stack traces.

**HTML (`update --html <file>` — use when structure earns it):**
- Reach for it for a diagram, table, before/after, or a multi-section status —
  not for a one-liner.
- **Mobile-first** (learned the hard way): `max-width:~480px`, **inline styles
  only** (email strips `<style>`/`<head>`), readable sizes (14–15px), and a
  **vertical/stacked layout**. Avoid wide tables and big ASCII in `<pre>` — they
  force horizontal scroll and shrink to unreadable on phones.
- Prefer real elements (stacked `<div>` boxes, small `<table>`s) over ASCII art.
- Use a system font stack; keep colors subtle. `update` auto-derives the
  plain-text fallback, so HTML sends are always safe.

**Both:**
- **Acknowledge fast.** When a reply comes in, a quick "on it — doing X" beats
  silence; there's inherent email latency, so don't leave the user wondering if
  you heard them.
- Everything stays in one thread automatically (updates reply into it).

## Poll interval & knobs

`listen`/`poll` hit the inbox with a plain `curl` — **no LLM tokens per check** —
so polling can be frequent. The agent is only woken (a real turn) when a reply
actually lands.

| env var | default | effect |
|---|---|---|
| `E2A_TETHER_POLL_INTERVAL` | `20` (s) | how often `listen`/`ask` check the inbox |
| `E2A_TETHER_ASK_TIMEOUT` | `1800` (s) | how long `ask` blocks for an answer before giving up |
| `E2A_BASE_URL` | `https://api.e2a.dev` | API base (set for self-host) |

The only thing that costs a turn per tick is a `/loop` **heartbeat** (tier 2
below) — keep that coarse (e.g. 30m). Reply latency ≈ the poll interval, and
it's cheap, so 20–30s is fine.

## Durability tiers

1. **In-session (default):** `listen` polls for the whole `--for` window —
   automatic while the terminal stays open, and cheap (curl only, no tokens).
   This is what the duration setup buys you: one long-lived poller, not manual
   restarts. Add **`listen --awake`** to keep the machine from *idle*-sleeping
   during the window (macOS `caffeinate`, auto-released when listening ends).
   Note: `--awake` does **not** survive *closing the lid* (macOS clamshell still
   sleeps) — that's tier 3.
2. **Heartbeat (optional):** a slow `/loop` (e.g. every 30m) can relaunch
   `listen` if it dies and keep the session warm. `/loop` wakes the *agent* (a
   full turn each tick) — use it as a supervisor, not the poller.
3. **Always-on (survives a closed laptop):** nothing in-session outlives a
   closed terminal, regardless of duration — that needs an **e2a webhook firing
   a cloud Routine** (a *fresh* session per fire, loses live context). Follow-on.

## Files

| file | role |
|---|---|
| `tether.sh` | runtime CLI: `start [--for]` / `update` / `ask` / `listen` / `poll` / `status` / `stop` |
| `lib.sh` | config + e2a send/reply/poll helpers |
| `hooks/tether-notify.sh` | optional Notification hook (blocked-alert) |
| `install.sh` | wire/unwire the Notification hook; `_selftest` |
| `tether.env.example` | credentials template |
