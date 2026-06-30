---
name: agentify
description: Deploy the autonomous-repo feedback loop into a GitHub repo. Scaffolds the lane workflows, the runtime skill, and one config file as a reviewed PR, then prints the one-time identity/secret setup checklist. Turns a repo into one that triages incoming feedback into issues and prepares human-gated fix PRs. Use when someone wants to make a repo self-managing / "agentify" it / install the feedback loop.
---

# agentify — deploy the autonomous-repo feedback loop

`/agentify` installs the framework into a target repo: feedback in →
triaged GitHub issue → human-gated fix PR → filer notified. It **bundles
the framework as templates** (this skill's `templates/`), so grabbing this
skill is grabbing the framework. The install lands as a **PR the repo owner
reviews and merges** — the install itself goes through the same human gate
the framework runs on.

> **v0 scope.** The full loop ships: **triage/intake** (email → triaged
> issue), **comms** (filer acks + the fix-gate approval email + verified-reply
> routing), **fix + release** (the coding agent's human-reviewed PR + the
> merge→shipped callback), and this **deploy** flow. The mechanical render is
> automated (`agentify-render.sh`, with a `_selftest`); the Q&A and the
> one-time identity/secret setup stay guided. An `update` mode (re-render
> preserving the adopter's config tweaks) is the natural follow-on — the
> render is already idempotent.

## What gets scaffolded into the target repo

| from `templates/` | to the target repo |
|---|---|
| `autonomous-repo.config.yml.tmpl` | `autonomous-repo.config.yml` (the only file the adopter owns) |
| `runtime-skill/**` | `.claude/skills/autonomous-repo/**` |
| `scripts/ticket_card.sh` | `scripts/ticket_card.sh` |
| `workflows/*.yml.tmpl` | `.github/workflows/*.yml` |

## Deploy procedure

1. **Detect.** Read the target repo: its `OWNER/REPO`, primary language,
   test command, and CI — used to fill the fix-lane `verify_setup_script`
   and sensible defaults.
2. **Configure.** Ask the adopter the config values and export them as the
   `ANS_*` env vars `agentify-render.sh` reads: `ANS_PRODUCT_NAME`,
   `ANS_OWNER`, `ANS_REPO`, `ANS_MARKER`, `ANS_REVIEWER_LOGIN`,
   `ANS_BOT_LOGIN`, `ANS_SUPPORT_ADDRESS`, `ANS_FIX_GATE_MODE` (`hitl`
   recommended), `ANS_APPROVER_ADDRESS`, `ANS_VERIFY_SETUP_SCRIPT`. (The bot
   login can be filled later from the checklist; secrets are never gathered
   here.)
3. **Render.** Run `agentify-render.sh --to <target-repo-root>`. It fills
   `autonomous-repo.config.yml` from the `ANS_*` answers (failing loudly on
   any unfilled placeholder) and scaffolds the runtime skill, the scripts,
   and the four workflows into their real paths
   (`.claude/skills/autonomous-repo/`, `scripts/`,
   `.github/workflows/*.yml`). **Re-running updates the scaffolded code but
   PRESERVES an existing `autonomous-repo.config.yml`** (your tuned
   `always_hitl`, the filled `bot_login`) — pass `--force` only to regenerate
   the config. Then **tune** the rendered config's `always_hitl` list for the
   product's sensitive surfaces, and sanity-check: `scripts/*.sh _selftest`
   all green and the config parses.
4. **Auto-do the safe parts.** Create the labels from `labels.*` via `gh`
   (`feedback`, `agent-fix`, `wontfix`, `feedback-ops`, the `status:*` set).
5. **Hand off the rest** (print, don't do — see `references/setup-checklist.md`):
   create the GitHub App (bot identity) and set `github_app_login` in config;
   create the e2a `support@` agent + an agent-scoped API key; add the repo
   secrets (`CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`, `E2A_API_KEY`,
   `AUTOREPO_APP_ID`, `AUTOREPO_APP_PRIVATE_KEY`); enable Actions; set branch
   protection so the fix lane's PRs require review. **Hand over the exact
   commands/links — never run the auth yourself.**
6. **Open the install as a PR.** Branch, commit the scaffolded files, open a
   PR titled "agentify: install the autonomous-repo feedback loop" that
   summarizes what each file does and links the setup checklist. Do not
   merge.

## After merge + setup

The lanes activate themselves: each no-ops loudly until its secrets exist,
then starts on the next cron tick. Flip the `AUTOREPO_LANES_PAUSED` repo
variable to pause everything. Run the loop interactively any time with the
`autonomous-repo` skill ("drain the triage queue").

## References

- `references/setup-checklist.md` — the one-time identity/secret setup.
- `references/adapters.md` — the TicketStore / CommsChannel / Intake
  adapter contracts and which are implemented.
- `references/security-invariants.md` — the defaults an adopter must not
  misconfigure away.
