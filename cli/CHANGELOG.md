# Changelog

## Unreleased

**Breaking:** `e2a login --agent <inbox>` is removed. The CLI is now
account-credential only on the login path — the browser handoff always saves an
account-scoped key. Mint a least-privilege inbox-bound key with
`e2a keys create --agent <inbox>` after logging in, or install one headlessly
with `e2a login --with-key`.

**Breaking:** `e2a login` no longer sets `agent_email`. It previously persisted
whichever inbox the server's handoff happened to name first, which silently
chose the `From:` address for every later `send`/`reply`. An account-scoped key
spans every inbox, so there is no inbox to infer. Commands needing one resolve
`--agent` → `E2A_AGENT_EMAIL` → an explicitly-set `agent_email`, or exit `2`.
Set a default with `e2a config set agent_email <email>`; a value set that way is
preserved across re-login.

## 1.6.0

Current release. Adds the CLI's scripting/harness surface: `whoami`,
`send`/`reply` (with `--attach`), `messages list`/`get`, and a stable 0–7
exit-code contract (`cli/src/exit.ts`) for shell-based harnesses (skills,
hooks, CI) — scripts can branch on the process exit status instead of
parsing JSON. Also adds:

- `agents list`/`create`/`get`, `keys create`/`list`/`delete`, and
  `protection get`/`set` — provision an inbox and a least-privilege key
  end to end without the dashboard.
- `login --agent <inbox>` (mint a least-privilege agent-scoped key, revoking
  the account bootstrap key — removed in Unreleased) and `login --with-key`
  (headless: validate and save a key from the arg, `$E2A_API_KEY`, or stdin).
- `listen --conversation`/`--once`/`--until`/`--text` — a blocking-wait
  primitive for a script waiting on one reply.

## 1.5.1

Republishes the CLI with a corrected `@e2a/sdk` dependency
range (`^4.0.0`); the previously published 1.5.0 still declared `^3.0.0`, so a
fresh `npm i -g @e2a/cli` could resolve an SDK major incompatible with the
current API. No CLI behavior changes.

## 1.5.0

The `e2a` CLI targets the e2a v1 API and runs on the 4.x TypeScript SDK
(`@e2a/sdk`).

### Notes
- Commands are thin wrappers over the namespaced SDK surface
  (`client.agents`, `client.messages`, `client.domains`, `client.webhooks`,
  `client.account`). Auth reads `E2A_API_KEY`; `E2A_URL` overrides the endpoint
  for self-hosted deployments (default `https://e2a.dev`, the hosted product's
  unified host — it serves the `e2a login` browser flow and proxies the `/v1`
  API). Direct SDK users (no browser login) can point at the API host
  `https://api.e2a.dev` instead.
