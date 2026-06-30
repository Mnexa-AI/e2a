# Setup checklist (one-time, human)

`/agentify` scaffolds files; it cannot grant an agent write access to your
repo or mint your credentials. Do these once, then the lanes activate
themselves (each no-ops loudly until its secrets exist). Run the auth
commands yourself — the skill hands them over, it does not run them.

## 1. Bot identity (GitHub App)

A dedicated App so every triage comment, label, and PR is attributable to
the bot, not a human or the generic Actions bot.

- Create a GitHub App (org or personal), grant it **Issues: read/write**,
  **Pull requests: read/write**, **Contents: read/write**; install it on
  the repo.
- Put its login in config: `github_app_login: "<app-name>[bot]"` (the
  ticket-card and markers are trusted ONLY from this login).
- Add repo secrets `AUTOREPO_APP_ID` and `AUTOREPO_APP_PRIVATE_KEY`.

## 2. Comms mailbox (e2a)

The mailbox that receives feedback and (later lanes) sends acks + approval
emails.

- Create the agent: `mcp__e2a__create_agent` with the `support_address`
  from config (a verified domain you own), or `POST /v1/agents`.
- Mint an **agent-scoped** API key bound to that address:
  `POST /v1/account/api-keys` with `scope: agent`, `agent: <support_address>`.
  The plaintext key is shown ONCE.
- Add it as the repo secret `E2A_API_KEY`.
- Run the mailbox with screening/HITL **off** — it is an intake firehose;
  inbound held for review looks "not yet arrived" to triage.

## 3. Model token

- `claude setup-token` (subscription, 1-year, manual rotation) → repo secret
  `CLAUDE_CODE_OAUTH_TOKEN`; or an `ANTHROPIC_API_KEY` for a team.

## 4. Repo settings

- Enable GitHub Actions on the repo.
- Branch protection on the default branch so the fix lane's PRs require the
  `reviewer`'s review before merge — **PR merge is the ship gate.**
- (Optional) Set the `AUTOREPO_LANES_PAUSED` repo **variable** to `true` to
  pause all lanes; unset/`false` to run.

## Activation order

Intake + triage need: a model token, `E2A_API_KEY`, and the App secrets.
Until all three exist the triage lane no-ops loudly. The comms and fix lanes
(later slices) gate on the same plus their own. Rotate the App key + model
token on a calendar reminder set today.
