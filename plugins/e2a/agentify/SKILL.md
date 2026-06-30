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

> **v0 scope.** Slices 1–2 ship the **triage/intake** lane (email → triaged
> issue) and the **comms** lane (filer acks + the fix-gate approval email +
> verified-reply routing). The fix and release-callback lanes are later
> slices; their templates are added under `templates/workflows/` as they
> land. This SKILL.md is the deploy procedure; it is intentionally thin in v0
> (a guided scaffold, not a fully automated wizard).

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
2. **Configure.** Ask the adopter (or take from args) the config values:
   `product_name`, `reviewer` (PR ship-gate login), the comms channel +
   `support_address`, `fix_gate.mode` (`hitl` recommended) + `approver`,
   `marker`, labels (defaults are fine), model pins. Render
   `autonomous-repo.config.yml.tmpl` → `autonomous-repo.config.yml` with
   these. Leave `github_app_login` and secrets as the checklist's job.
3. **Render.** Copy `runtime-skill/**` → `.claude/skills/autonomous-repo/`,
   `scripts/ticket_card.sh` → `scripts/`, and each
   `workflows/*.yml.tmpl` → `.github/workflows/*.yml` (drop the `.tmpl`).
   The workflows read everything from config — they are copied verbatim, not
   string-substituted.
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
