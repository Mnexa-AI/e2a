# Autonomous-Repo Framework — feedback loop delivered as an `/agentify` deploy skill

| | |
|---|---|
| **Status** | Proposed (design discussion) |
| **Date** | 2026-06-29 |
| **Author** | Josh + Claude |
| **First adopter** | e2a (this repo) |
| **Derived from** | `~/Desktop/agentdrive/docs/feedback-loop-design.md` — generalizes its §7 extraction seam |
| **Memory** | `project_feedback_loop_framework` |

---

## 1. Problem statement

Feedback, bug reports, and small fixes for a repo arrive wherever users find us
and die in inboxes and issue queues. Triage, dedup, fix-drafting, and filer
follow-up are all manual. We want a **reusable, repo-agnostic framework** that
turns a GitHub repo into a self-managing one: feedback in → triaged GitHub
issue → human-gated fix PR → filer notified — with Claude Code agents doing
everything *between* intake and the merge button, and a human deciding the two
things that matter (what to attempt, what to ship).

It must be installable by anyone in one move — a **deploy skill (`/agentify`)** —
and must **dogfood on e2a first** (e2a's support loop runs on e2a itself). The
loop already exists, proven, in agentdrive; this design generalizes agentdrive's
deliberately-built extraction seam into a distributable framework and builds the
**zero-backend path** agentdrive deferred.

## 2. Goals and non-goals

**Goals (v0):**

- A generic **GitHub-Actions lane runtime** (triage, fix, comms, release-callback)
  that depends only on three adapter contracts + one config file — never on a
  specific product's schema, tools, or auth.
- **Three adapter contracts**, each with one real shippable implementation:
  `TicketStore`→**GitHubStore**, `CommsChannel`→**E2A**, `Intake`→**Email**.
- A **`/agentify` deploy skill** that bundles the framework as templates and
  scaffolds a configured instance into a target repo via a **reviewed PR**.
- **e2a slice-1**: a working loop with GitHubStore + E2A comms + Email intake,
  **zero new backend** (e2a's own conversation store holds the PII).
- **Two human gates**: the `agent-fix` label (what the coding agent attempts)
  and PR review (what ships).
- **Security invariants enforced by construction**: fix lane holds zero deploy
  secrets, untrusted text is fenced-as-data, comment trust is filtered to
  OWNER/MEMBER, markers are honored only in bot-authored placement, identity is
  per-adopter.

**Non-goals (v0):**

- **BackendStore implementation** — agentdrive's §5.4 Postgres API is the
  *on-paper anchor* that shapes the `TicketStore` contract; it is not built or
  wired here.
- The `GitHubIssue` / `SMTP` / `None` adapters beyond interface stubs + docs.
- comms auto-translation, SLA machinery, priority beyond labels, a ticket-browsing UI.
- The deploy skill's **`update` mode** (re-render templates preserving config) —
  designed-for, not built.
- A published-plugin / template-repo **distribution mechanism** — deferred by
  decision; v0 distribution is "grab the skill dir".
- Multi-tenant hosting — each adopter runs lanes in their own repo's Actions.

## 3. Relevant context and constraints

**agentdrive is the proven reference.** Reuse, don't reinvent: its §5.4 internal
API = the `BackendStore` shape; its §6.1 = the security model; its
`feedback-loop.config.yml` = ~90% of this framework's config file; its three lane
YAMLs + `support-engineer` skill = the templates the deploy skill ships.

**e2a integration contract** (verified against the codebase):

- **Inbound**: relay fires `email.received` (`internal/relay/server.go:338`).
  Two consumption modes, no "delivery mode" to pick: a `create_webhook`
  subscription filtered to `email.received` for `support@`, or polling
  `list_messages direction=inbound read_status=unread sort=asc`. The
  `email.received` event id is **deterministic** —
  `sha256(message_id|event_type)` (`internal/webhookpub/event.go:216`) — so SMTP
  retries dedup for free.
- **`authenticated_from`** (the SPF/DKIM/DMARC-verified sender identity, in the
  inbound payload at `internal/relay/server.go:589`) is the **load-bearing
  security primitive** for reply routing — e2a provides natively the
  verified-sender check agentdrive built by hand.
- **Threads**: `conversation_id` = `conv_<id>`; reply with **`reply_to_message`**
  (preserves In-Reply-To/References) — never `send_message` (forks a new
  thread). A fresh human first-contact may carry an **empty conversation_id**
  until the first reply mints the thread.
- **Mailbox**: an agent *is* its email address; create `support@<verified
  domain>` via `create_agent`, mint an **agent-scoped API key** bound to it
  (returned once) as the comms secret.
- **`pending_review`** on send/reply is **success-held (HITL), not failure** —
  must not be retried.
- MCP comms surface: `list_messages`, `get_message`, `reply_to_message`,
  `send_message`, `list_conversations`, `get_conversation`, `get_attachment`,
  `create_webhook`.

**Repo constraints**: e2a is OSS, Go 1.25, Postgres on 5433, npm workspaces.
Migrations idempotent; `/v1` spec + SDK drift gates. The plugin lives at
`plugins/e2a/`. e2a already ships a HITL approval flow (`internal/hitlnotify`,
`hitlworker`, `approvaltoken`, `idempotency`) whose idempotency/notification
conventions the lanes should mirror.

**Central threat**: feedback emails and issue/PR comments are
attacker-controlled text flowing into agent prompts *and* a repo-write coding
agent. Treat as data, never instructions (§5).

## 4. Proposed design

### 4.1 Four layers

1. **Lane runtime** — 4 GitHub Actions workflows (triage, fix, comms, released)
   running headless Claude Code (`claude-code-action` / `claude -p`).
   Product-neutral; everything product-specific is read from config.
2. **Runtime skill** — `autonomous-repo` (the generalized `support-engineer`):
   `SKILL.md` + per-lane procedures (`triage.md`, `fix.md`, `comms.md`) + email
   templates. Runs **both** in the lanes and **interactively** ("drain the
   triage queue"). Calls the three adapters via documented procedures. Never
   names a product.
3. **Config** — `autonomous-repo.config.yml` (rename of agentdrive's
   `feedback-loop.config.yml`): the **only** file an adopter owns.
4. **Deploy skill** — `/agentify`: bundles `templates/` (workflows + runtime
   skill + config schema) + `references/` (setup checklist, adapter docs,
   security invariants) + `SKILL.md` (the deploy procedure). **Self-contained:
   grabbing the skill *is* grabbing the framework.**

### 4.2 The three adapter contracts

Adapters are **prompt-level contracts**, not Go interfaces: the lane runtime is a
Claude Code agent, so an "adapter" = (a) a procedure section the skill follows,
(b) the tools/secrets that procedure is allowed, (c) config keys. This keeps the
runtime generic without a code plugin system.

**`TicketStore`** — operations the skill needs:

| Op | Meaning |
|---|---|
| `list_pending(stage)` | tickets needing a lane's attention |
| `get(ref)` | `{status, kind, title, body, dup_of, issue, pr, timeline, notified_set, comms_ref}` |
| `transition(ref, to, {dup_of?, issue?, pr?, summary?})` | validates the state machine (§4.4) |
| `append_event(ref, kind, detail)` | timeline entry |
| `find_candidates(query)` | dedup search |
| `record_notified(ref, stage, comms_ref)` | notification idempotency write |

**GitHubStore** (the v0 build):
- ticket = a GitHub issue labeled `feedback`; `ref` = the issue number.
- status = labels (open + one `status:*`) or closed + native `state_reason`.
- the **ticket-card**: one pinned bot comment carrying an HTML-comment-fenced
  JSON block — the machine-readable projection (`status`, `kind`, `dup_of`,
  `comms_ref`=conversation_id, `notified_set[]`, event log). All lanes read/patch
  this single comment; it is the authoritative state, labels are the
  human-visible projection.
- `list_pending` = `gh issue list` by label/state; `find_candidates` =
  `gh search issues`; `transition` = relabel/close via `gh` + patch the
  ticket-card; `record_notified` = append a stage to `notified_set`
  (read-before-send; the workflow `concurrency:` group is the serializer).

**BackendStore** (anchor only, not built): the same ops map onto agentdrive's
§5.4 endpoints; documented in `references/adapters.md`.

**`CommsChannel`** — `notify(ref, stage, slots)`, `poll_replies()`,
`resolve_contact(ref)`.

**E2A** (the v0 build):
- `notify` = `reply_to_message` into the ticket's conversation; for the first ack
  with no thread yet, `send_message` mints the thread and its `conversation_id`
  is captured into the ticket-card.
- `poll_replies` = `list_messages direction=inbound read_status=unread sort=asc`
  → `get_message` per item (body + `conversation_id` + `authenticated_from`).
- **verified reply** = `conversation_id` matches a ticket-card `comms_ref` **AND**
  `authenticated_from` == the address that opened that thread. A subject-line
  ticket hint is **never** sufficient (issue numbers/markers are public).
- `resolve_contact` is a no-op for E2A — the conversation *is* the contact store;
  the address never leaves e2a.
- `pending_review` → record a `pending` event, do **not** retry.

**`Intake`** — produces raw items the triage lane normalizes into tickets.

**Email** (the v0 build):
- new feedback = inbound to `support@` that is **not** a reply to a known ticket
  (no matching `conversation_id`).
- triage normalizes `{authenticated_from, subject, body}` → creates a **PII-free**
  GitHub issue (quoted body + marker, never the address) → records the
  `conversation_id` in the ticket-card → comms sends the triage-ack into that thread.

### 4.3 Control flow (e2a slice-1)

**Triage lane** (`cron */30` + manual dispatch):
1. Pause-switch repo-var check → early exit (before any model call).
2. `Intake.poll` new feedback emails (inbound, unread, no ticket `conversation_id`).
3. Per item (FIFO, ≤budget): classify `{bug|feature|other|noise}` — body fenced-as-data.
4. Dedup: `find_candidates` → read top issues → judge:
   - **duplicate** → comment evidence on canonical issue, close-as-duplicate, point ticket-card `dup_of`;
   - **noise/question** → close-as-not_planned + `status:noise`;
   - **actionable** → **claim-first**: create issue (`feedback` + `status:triaged`), write the ticket-card (with `conversation_id` + marker footer), *then* attach.
5. Reconciliation sweeps: human `wontfix`/close → sync ticket-card; `in_progress`
   PR merged >24h / closed-unmerged → `shipped` / `triaged` (missed-callback repair).
6. Mark intake messages read **only after** the ticket-card records them.
7. On failure → alert step (e2a-mail Josh + comment the pinned `feedback-ops` issue).

**Fix lane** (on `agent-fix` label):
1. `claude-code-action`; GitHub App installation token as `github_token`; `CLAUDE_CODE_OAUTH_TOKEN`.
2. Consume **only** the bot-authored issue body (fenced user block) + OWNER/MEMBER comments.
3. Boot the verify stack via the config-named `verify_setup_script` (e2a:
   `docker compose up postgres`, `make migrate`, throwaway env). **Every
   credential is worthless outside the run.**
4. Prepare branch + PR titled with the marker; PR body includes a `customer-note`
   block (the source for the shipped email's prose — reviewed as part of PR review).
5. A **separate non-agent step** flips the ticket-card to `status:in-progress`
   (the agent step holds zero backend/deploy secrets — in GitHub-only mode this
   is a `gh` label/comment patch by a bot-token step, *simpler than agentdrive's
   FIX backend secret*).
6. Request `reviewer` (jiashuoz) review + assign.

**Release callback** (`feedback-released`, on push to main / merge):
- resolve the merged PR; scan its **description** (bot-authored placement) for the
  marker; honor the marker **only** from a PR authored by the bot App; transition
  ticket-card → `shipped`. A 409 is tolerated (the triage sweep reconciles).

**Comms lane** (`cron */30`):
1. Pause-switch.
2. **Outbound** — per ticket past a policy stage with no matching `notified_set`
   entry → `reply_to_message` the templated email → `record_notified`. Stages:
   `triage-ack` (received→triaged|dup|noise), `shipped` (→shipped; slots the PR
   `customer-note`), `resolved-closed` (→wontfix). `in-progress` deferred.
3. **Inbound** — `poll_replies`; per **verified** reply, route: dispute-of-fix
   (shipped→triaged), dispute-of-dup (dup→triaged), substantive-on-noise
   (noise→received) — each quote-commented onto the issue. `stop`/unsubscribe →
   flip `contact` off in the ticket-card. Unverified / ambiguous / escalation
   (anger, legal, "talk to a person") → **leave for a human**.
4. Untrusted email fenced-as-data; one thread's content never enters another's context.

### 4.4 State model (GitHub-only)

- **open** + `status:triaged` | `status:in-progress`; **closed**: `state_reason`
  `completed`=shipped, `not_planned` + `status:wontfix`|`status:noise`,
  `duplicate`=closed_duplicate.
- Recovery edges (per agentdrive): `shipped→triaged`, `closed_duplicate→triaged`,
  `closed_noise→received`, `in_progress→triaged`, driven by the comms lane / sweeps.
- The state machine is a **single shared spec** (a product-neutral procedure
  section in the runtime skill) that every `TicketStore` honors — so the skill
  stays dumb and both stores validate the same transitions.

### 4.5 Deploy skill (`/agentify`)

Bundle layout (home: `plugins/e2a/agentify/` for v0):
```
agentify/SKILL.md                         # the deploy procedure (the only thing that "runs")
  templates/workflows/*.yml.tmpl          # triage, fix, comms, released
  templates/autonomous-repo.config.yml.tmpl
  templates/runtime-skill/**              # SKILL.md, triage.md, fix.md, comms.md, email-templates/
  references/{setup-checklist,adapters,security-invariants}.md
```
Run procedure: **detect** repo facts (language, test cmd, CI → fill the verify
bootstrap) → **ask** config questions (repo, labels, reviewer, comms adapter,
intake adapter, auto-fix policy, model pins, `verify_setup_script`) → **render**
templates into the target repo → **auto-do** the `gh`-reachable setup (create
labels) → **hand off** the setup checklist for what a skill must not do
(create the GitHub App, paste the Anthropic + e2a secrets, enable Actions, set
branch protection) — *handing over the auth commands, not running them* → **open
the install as a PR**. The e2a install is the first invocation; its output is
slice-1.

### 4.6 Identity & secrets (e2a)

- **GitHub App** (bot) for triage/fix/comms attribution; installation token.
- **e2a agent** `support@<verified e2a domain>` + agent-scoped API key → the comms secret.
- **`CLAUDE_CODE_OAUTH_TOKEN`** (or `ANTHROPIC_API_KEY` for a team).
- No FIX backend secret in GitHub-only mode (the `in_progress` write is a
  bot-token `gh` patch).

### 4.7 Fix gate: `auto` vs email-HITL (config `fix_gate.mode`)

The pre-PR human gate is a per-deployment choice. Two modes — and in **both**,
PR **merge** stays the unchanged ship gate (neither auto-merges; `auto` means
auto-*open-PR*, never auto-ship):

- **`auto`** — when triage judges an item confidently actionable, it applies
  `agent-fix` itself → the fix lane opens a PR. No human pre-gate; the
  maintainer's first touch is PR review.
- **`hitl`** (recommended e2a default) — triage does **not** label. It records
  `fix_gate.decision: needs_approval` on the ticket-card. The **comms lane**
  (the only lane holding e2a *send*) emails the approver (`fix_gate.approver`):
  *"Issue #N looks fixable — reply `approve` to have me draft a PR, or `decline`
  with a reason"*, moving the ticket to `awaiting_approval`. It then polls for
  the reply; on a **verified** approval (`authenticated_from == fix_gate.approver`
  on the approval thread) it applies `agent-fix` → fix lane → PR. A decline →
  `triaged` / `closed_wontfix` with the reason. **No reply → stays
  `awaiting_approval`** (fail-closed: silence never ships). *(Build deviation:
  the approval email is sent by comms, not triage — capability minimization keeps
  e2a-send in one lane. §10.)*

The fix lane is **identical in both modes** — the only difference is *what
applies the `agent-fix` label*: triage directly (auto), or the comms lane after
a verified email approval (hitl). The label stays the technical trigger; the
human gate is what moves.

This is the framework's **second dogfood of e2a**: the same `authenticated_from`
verified-reply machinery that routes filer replies now carries **maintainer
approvals**. "Approve a code-fix by replying to an email" is both the gate and a
live e2a demo — a push to the maintainer, not a GitHub queue to scan.

Config:
```yaml
fix_gate:
  mode: hitl              # auto | hitl
  approver: josh@e2a.dev  # hitl: the approval email goes here AND must be replied from here
  # optional safety valve — force hitl for sensitive surfaces even when mode: auto
  always_hitl:
    - auth, DKIM/SPF, HMAC header signing
    - billing / Stripe
    - migrations on messages / usage_events
    - /v1 wire contract / SDK codegen
```

**State addition**: `triaged → awaiting_approval → in_progress` (approved) |
`→ triaged | closed_wontfix` (declined). `awaiting_approval` exists only under
`mode: hitl`. The ticket-card gains
`approval: {status, conversation_id, decided_by, reason?}`.

**Security**: an approval requires a verified reply from the configured approver
on the approval thread — untrusted feedback cannot forge it (can't send mail as
the approver), and a stranger who knows the issue number cannot approve (subject
tokens never route). Intent parsing is conservative: only an unambiguous
approve/decline acts; "maybe / let me look" stays pending.

**Sub-decisions** (recommended defaults): `approver == reviewer` (both Josh) for
e2a; a no-reply reminder after 3 days then indefinite hold (no auto-decline) in
v0; `always_hitl` safety valve **on** even when `mode: auto`.

## 5. Edge cases and failure handling

- **Untrusted input**: fenced-as-data everywhere; OWNER/MEMBER author-association
  comment filter; markers honored only in bot-authored placement; attachments are
  *described*, never executed/rendered — triage uses `get_attachment` to write a
  factual description + extracted error text, bytes never reach GitHub or the fix lane.
- **Empty conversation_id first-contact**: the triage-ack `send_message` mints the
  thread; capture `conversation_id` *before* marking the inbound read; if the send
  fails, leave it unread (retried next tick).
- **Forged reopen**: subject ticket-hints never route; only `conversation_id` +
  `authenticated_from`. e2a's verified sender makes this airtight.
- **`pending_review` on reply**: record `pending`, don't retry; a later run sees it resolved.
- **Duplicate intake** (SMTP retry): deterministic `email.received` id +
  `message_id` dedup; mark-read-after-record ordering.
- **Claim-first issue creation**: intent recorded before the GitHub call;
  eventual-consistency recovery lists recent bot-authored issues (not the search index).
- **Lane overlap**: mandatory per-lane `concurrency:` group (no cancel) — the
  *only* serializer for `notified_set` in GitHub-only mode. Honestly weaker than a
  DB unique index; documented as the price of zero-backend.
- **Pause switch** repo-var → all lanes early-exit before a model call.
- **Failure alerting**: every lane's on-failure step e2a-mails Josh + comments the
  pinned `feedback-ops` issue (scheduled-run failures otherwise notify nobody).
- **Defaults fail closed**: `contact` off unless the filer is the verified sender;
  finite budgets; unverified mail unanswered; illegal transitions rejected. (Auto-fix,
  if enabled, is deliberately *not* fail-closed — it leans on the merge gate.)
- **e2a HITL on `support@`**: if screening/HITL is enabled on the mailbox, inbound
  feedback itself can be held `pending_review`; triage must treat held inbound as
  not-yet-arrived. **Recommendation: run `support@` with protection OFF** (it's an
  intake firehose). → open question #2.

## 6. Scalability and extensibility

- **Volume**: intake is a poll; triage reads ≤20 rows/run; dedup reads a handful of
  issues — fine until open `feedback` issues number in the hundreds, then add a
  search/embedding prefilter in front of the same judge. e2a's OSS volume is low.
- **Zero-backend's weaker guarantees** (no transactional notification idempotency,
  no PII boundary beyond e2a) are the explicit price; the **BackendStore** adapter
  is the upgrade path when an adopter has private filer identity or needs DB-grade
  idempotency.
- **Seams**: a new `TicketStore` (Backend), `CommsChannel` (SMTP/None), or `Intake`
  (GitHubIssue/MCP/web) plugs at the documented contract; the lane runtime and
  deploy skill don't change. **Adding the GitHubIssue intake adapter is the
  cheapest second proof of the intake seam.**
- **Multi-repo**: each adopter runs lanes in their own repo with their own
  identity — scales by copy, not by tenancy.

## 7. Verification strategy

- **Golden-fixture lane tests** (agentdrive pattern): per-lane `claude -p` over
  fixed inputs — a dup pair, a non-dup near-miss, an injection attempt (incl.
  image-borne), a verified reply-reopen, a dup-dispute, a forged subject-token, an
  unsubscribe — run in CI against the **pinned model** (prompt+model is the unit).
- **GitHubStore unit checks**: ticket-card read/patch round-trip; transition
  validation incl. illegal-edge rejection; `notified_set` idempotency via a
  serialized double-run.
- **E2A comms integration**: against a dev mailbox (Mailpit / local e2a) — ack,
  reply-into-thread `conversation_id` continuity, and a spoofed `authenticated_from`
  rejected by the verified-reply rule.
- **Deploy-skill verification**: run `/agentify` against a scratch repo (worktree)
  → assert it scaffolds files, renders config, creates labels, and the install PR
  is coherent. The **e2a install is the first real E2E.**
- **Manual first-release E2E**: email `support@` from a cold address → issue
  created + ack received; apply `agent-fix` → PR; merge → shipped email; reply to
  dispute → reopen.
- **Most-likely regressions**: ticket-card JSON drift across lane edits
  (schema-validate it); label taxonomy vs config mismatch; `pending_review`
  mishandled as failure.

## 8. Open questions

1. **Intake scope for e2a v0** — email-only (recommended; matches the original
   framing and is the tightest dogfood), or add GitHubIssue intake in v0 too?
2. **`support@` mailbox protection** — run with HITL/screening **OFF** (recommended;
   it's a feedback firehose), confirm against e2a defaults.
3. ~~**Auto-fix posture**~~ **Resolved (§4.7)**: two modes, `fix_gate.mode:
   auto | hitl`. `auto` = triage opens a PR directly; `hitl` (e2a default) =
   triage emails the approver, and a verified approval reply triggers the PR.
   PR merge stays the ship gate in both. Remaining sub-decisions in §4.7.
4. **Build location** — inside `e2a/plugins/e2a/agentify` first then extract to a
   standalone repo at the second real adopter (recommended; extract-after-second-use),
   vs standalone from day one (purer framework-first).
5. **Shared-source for the runtime skill across adopters** — vendored copy (deploy
   skill writes it into each repo; recommended v0) vs submodule vs published plugin.
6. **Bot identity** — GitHub App (recommended) vs machine user.
7. **`support@` domain** — which verified e2a domain (e2a.dev / api.e2a.dev / a
   dedicated `support.` subdomain).

## 9. v0 slices (for `/implement`)

1. **Runtime skill + GitHubStore + Email intake + Triage lane** — the
   `autonomous-repo` skill (triage procedure), the ticket-card schema + GitHubStore
   procedures, `autonomous-repo.config.yml`, `feedback-triage.yml` (pause switch,
   concurrency, claim-first creation, sweeps, failure alert). Issues start flowing
   from emails. Shippable alone.
2. **Comms lane (E2A)** — `feedback-comms.yml`, the notification policy + templates
   (triage-ack, shipped, resolved-closed), verified-thread inbound routing,
   unsubscribe, escalation. Closes the loop to the filer.
3. **Fix lane + release callback** — GitHub App, `feedback-fix.yml` (label-gated,
   author-association filter, verify bootstrap, customer-note block, separate
   transition step), `feedback-released.yml` (bot-authored-marker rule).
4. **Deploy skill `/agentify`** — bundle the above as templates + the deploy
   procedure + references; prove by re-deriving the e2a install from a scratch repo.

Slices 1+2 already beat the status quo; 3 adds the fix automation; 4 makes it distributable.

### §10 addenda (slice 4: the `/agentify` deploy flow)

Built on `main`. `plugins/e2a/agentify/agentify-render.sh` is the deterministic
scaffolder; `SKILL.md` is the interactive wrapper.

- **Render** fills `autonomous-repo.config.yml` from `ANS_*` answers (failing
  loudly on any unfilled placeholder — checked against the real `{{UPPERCASE}}`
  tokens, not the literal `{{...}}` in the template's comment) and copies the
  runtime skill, scripts, and the four workflows into their real paths
  (`.claude/skills/autonomous-repo/`, `scripts/`, `.github/workflows/*.yml`,
  `.tmpl` stripped). `_selftest` renders into a temp dir and asserts the tree +
  substitution; an **e2e renders e2a's answers into a scratch repo** and the
  three scaffolded scripts pass their own selftests in the rendered location —
  the wizard provably reproduces the e2a install.
- **Re-run preserves the adopter's config** (updates code only) unless
  `--force` — so re-rendering to pick up framework updates never clobbers a
  tuned `always_hitl` / filled `bot_login`. This is the foundation of the
  deferred `update` mode.
- **Honest scope**: the mechanical render is automated; the Q&A and the
  one-time identity/secret setup remain guided (a skill can't create a GitHub
  App or paste secrets — `references/setup-checklist.md`). sed answer-injection
  is bounded (`\ & |` escaped; `|` delimiter so `/` in `owner/repo` is safe).
- **Going live on e2a** = running this render against the e2a repo root (Phase
  A) + the one-time setup; deferred to an explicit "go live" step since it
  needs the human identity/secret work regardless.

### §10 addenda (addon mechanism + submit_feedback)

Built on `main`. An **addon mechanism** (`templates/addons/`) makes the
framework extensible: each addon is `manifest.yml` + `files/` + `setup.md`,
opted in via `ANS_ADDONS`; the render scaffolds `files/` → `tools/<name>/` and
appends `setup.md` → `AGENTIFY-ADDON-SETUP.md`. Selftest covers scaffold +
unknown-addon rejection + the no-addons default.

The first addon, **`submit-feedback-mcp`**, is an *intake* adapter — a
`submit_feedback` / `feedback_status` MCP server that **email-bridges** agent-
filed feedback into the support mailbox the triage lane already drains, so it
is purely additive (zero loop changes).

- **In-band model**: the bridge sends from its OWN e2a identity (TO the support
  address, a fixed recipient — structurally bounded like `comms_send.sh`); it
  never accepts a caller-supplied "email me here" address (spoof/spam vector).
  The filer polls `feedback_status` rather than getting direct email replies.
- **Pure logic unit-tested** (`bridge.mjs` + `bridge.test.mjs`, node:test):
  validation (validate-before-charge), email composition (untrusted body sent
  as opaque data — never interpolated/evaluated), coarse status derivation. The
  MCP + e2a-REST wiring (`server.mjs`) is verified at install.
- **Accepted residuals**: `feedback_status` ids are bearer capabilities
  (unguessable `conv_` ids; the tool returns only coarse `received`/`answered`
  status, not thread content) — fine for the in-band model. Rate limit is a
  per-process backstop (the host/e2a is the durable limiter). `feedback_status`
  is coarse vs the ticket-card.
- **Richer variant (follow-on)**: put `submit_feedback` inside a host MCP
  server that authenticates the *caller* and sends as them — then comms acks
  reach the filer's own inbox. For e2a, a tool in its own `mcp/` server; the
  agent-facing contract is unchanged.

### §10 addenda (test harness)

`plugins/e2a/agentify/test/run.sh` is the deterministic suite (CI:
`.github/workflows/agentify-test.yml`): every script `_selftest` + the addon's
`bridge.test.mjs` + bash/JS syntax + `test/validate.py` (YAML parse, the
rendered config vs what the workflows read, **e2a MCP/REST URL host
consistency** — which catches the `mcp.e2a.dev` vs `api.e2a.dev` class, with a
negative test confirming it fails on the bug — required keys, stray
placeholders). **Golden-fixture lane tests** (`test/fixtures/`,
`.github/workflows/agentify-lane-fixtures.yml`) drive each lane's real prompt
via `claude -p` over a mocked world (stub e2a MCP + fake `gh`/scripts that
record actions) and assert on what the agent attempted — triage fixtures cover
the happy path, **injection-as-data resistance**, and the **read-on-fetch
reply-skip**. The model layer is token-gated; the assertions are
deterministically self-tested (`assert-selftest.sh`, in the main suite) so a
broken assertion is caught without the model. Still open: comms/fix fixtures,
and the **live over-the-wire e2e** at go-live.

**Hardened after adversarial review** (relay/spoof/SSRF/key-exfil all refuted —
the fixed-recipient bound holds): `feedback_status` now validates the id is a
`conv_…` before the fetch (an `.`/`..` id otherwise reached unintended same-host
endpoints via dot-segment normalization) and is rate-gated (enumeration was
free); the subject strips CR/LF (defense-in-depth vs a downstream MIME-header
splat); `submit_feedback` wraps the e2a call so a failure can't disclose the
intake address; and `apply_addons` rejects non-`[a-z0-9-]` addon names (cp
escaping `tools/`). Each fix has a regression test.

### §10 addenda (plugin packaging)

`/agentify` is shipped as a **skill in the e2a Claude Code plugin** so it's
installable, not just copy-able:

- The deploy procedure lives at `plugins/e2a/skills/agentify/SKILL.md` (a
  second skill alongside the existing `skills/e2a/`); the plugin's marketplace
  entry is unchanged, version bumped `0.3.2 → 0.4.0` across all manifests
  (`scripts/validate-plugin.mjs` enforces the sync + the frontmatter rules).
- The scaffolder + templates stay at `plugins/e2a/agentify/` (so CI and the
  test suite are unchanged); the skill references them via
  **`${CLAUDE_PLUGIN_ROOT}/agentify/`** — the whole plugin ships to the install
  cache, so the bundled `agentify-render.sh` / `templates/` / `references/`
  resolve at runtime.
- Install: `/plugin marketplace add Mnexa-AI/e2a` → `/plugin install e2a` →
  `/agentify` is available (plus the e2a MCP tools). The §4.5 "home:
  `plugins/e2a/agentify/`" note refers to the tooling dir; the user-facing
  skill is the plugin skill above.

## 10. Implementation reconciliation (`feat/agentify-feedback-loop`)

Deviations recorded at build time (slice 1 — intake + triage):

- **Home / shape**: the framework lives at `plugins/e2a/agentify/` — a deploy
  skill (`SKILL.md` + `references/`) whose `templates/` *are* the framework
  (`autonomous-repo.config.yml.tmpl`, `runtime-skill/**`,
  `workflows/feedback-triage.yml.tmpl`, `scripts/ticket_card.sh`).
  `examples/e2a/autonomous-repo.config.yml` is the rendered e2a instance.
- **Approval email is sent by the comms lane, not triage** (§4.7): only comms
  holds e2a-send. Triage records `fix_gate.decision` + `approval.status:needed`
  on the ticket-card; comms actuates (sends, moves to `awaiting_approval`,
  applies `agent-fix` on a verified approval).
- **Workflows are per-(comms/intake) adapter, not purely config-driven**: the
  triage YAML wires the e2a MCP **read** tools (`mcp__e2a__list_messages` etc.)
  for email intake. A future SMTP/none adapter is a different workflow variant.
  Product *values* still come only from config; the *adapter surface* is in the
  workflow + the runtime skill's email-intake section.
- **Ticket id = the issue number** (github store); the `marker` is a
  presence-only own-line footer in bot-authored issues/PRs (trust = author is
  `github_app_login`). No minted `fbk_`-style id in the github store.
- **Ticket-card** is the state authority (a pinned bot comment, JSON between
  `autorepo:ticket-card:begin/end` sentinels); labels are its projection.
  `scripts/ticket_card.sh` is the only Bash surface the lane is allowlisted for
  it (read/init/set/add-event/find-by-comms + a `_selftest` for the pure logic).
- **No backend / no triage secret** in the github store: the lane's credential
  inventory is the Anthropic token + a GitHub App token + a read-only e2a key.
- **Deferred to later slices**: comms lane (send + verified-reply routing,
  slice 2); fix lane + release callback (slice 3); the thick deploy wizard +
  `examples` re-derivation (slice 4); the triage↔comms shared-mailbox partition
  (a slice-2 coordination decision — see §8 / Open questions).

### §10 addenda (dual-review hardening, slice 1)

Fixed after independent + adversarial review of `feat/agentify-feedback-loop`:

- **Security posture is "bounded blast radius", not "secrets unreachable".** The
  adversarial pass showed `Bash(jq:*)` reads the run env (`jq -rn env.E2A_API_KEY`)
  and `Bash(gh:*)` exposes `gh auth token` + `gh api`. The triage allowlist is
  narrowed to `Bash(gh issue:*)` + the ticket-card helper + `Read` + e2a **read**
  tools (no `jq`, no broad `gh`). The honest guarantee is bounded blast radius
  (read-only e2a key, ~1h issues-only App token, no deploy creds, human PR merge),
  recorded in `references/security-invariants.md` #5.
- **`ticket_card.sh` gh path was non-functional** (`tail -n 1` on a multi-line
  card body returned only the end sentinel). Rewritten: `_select_card` picks the
  latest **bot-authored** card as a JSON `{id,body}` record (no line splitting),
  `_extract_card` matches sentinels as whole lines (a sentinel substring in a
  value no longer truncates), `_merge` strips `events` from a patch (a `set` can
  never clobber the audit trail). The trust filter + these paths are now covered
  by `_selftest`.
- **Dup window closed**: the bot-authored issue-body footer carries
  `comms:<conversation_id>`, written atomically with the issue, so `find-by-comms`
  recovers a crashed claim even before the ticket-card exists.
- **`closed_noise`/`closed_wontfix` use native `not_planned`** (+ the `wontfix`
  label for wontfix); no separate `status:noise` label — `state-machine.md` is the
  authority, diverging from §4.3's mention of `status:noise`.
- **`pause_switch_var` config key removed** (non-functional — GitHub Actions can't
  resolve a config-named var); the var name is fixed at `AUTOREPO_LANES_PAUSED`,
  exact-match `"true"`.
- **Known bounds (accepted in v0)**: `find-by-comms` scans ≤500 issues; the pause
  match is exact-string; an injection that the model obeys can still publish a
  bounded-scope token (the blast-radius framing, not a leak-proof claim).

### §10 addenda (slice 2: comms lane)

Built on `main`. The E2A comms lane — `runtime-skill/comms.md`,
`runtime-skill/templates/{triage-ack,approval-request,resolved-closed,shipped}.md`,
`workflows/feedback-comms.yml.tmpl`:

- **Triage↔comms mailbox partition resolved** (was the deferred §8 question):
  triage owns inbound that is NOT a known reply (new feedback → it marks read);
  comms owns owed notifications + inbound that IS a reply to a known thread. The
  predicate is `find-by-comms` (deterministic, same for both), and comms only
  *routes* a reply once `triage-ack` is in `notified[]` — so the original
  feedback is never mistaken for a reply.
- **The fix-gate hitl loop is actuated by comms**, not triage (capability split):
  triage records `fix_gate.decision=needs_approval`; comms emails the approver
  (`send_message` to `fix_gate.approver` only), moves `triaged → awaiting_approval`,
  and on a verified approval reply applies `agent-fix` (back to `triaged`, fix
  lane picks it up) / on decline → `closed_wontfix`.
- **Verified reply** = `conversation_id` matches a ticket's `comms_ref` (filer) or
  `approval.conversation_id` (approver) AND `authenticated_from` is the address on
  file — for the approver that is the config address; for the filer it is proven by
  thread membership (e2a only delivers a thread to its participants).
- **Send guardrails** (prompt-level, the honest bound): outbound is
  template-bounded (free prose only inside a reply thread); `send_message`'s `to`
  is ONLY `fix_gate.approver`, never an address from email content;
  `reply_to_message` cannot be redirected out of its thread; one thread's content
  never enters another's context. `forward_message` is disallowed.
- **Notification stages active in v0**: `triage-ack`, `approval-request`,
  `resolved-closed`. `shipped` ships dormant (needs the fix + release lanes).
  Dup-filer fan-out acks are a noted refinement (the dup `conversation_id` is on
  the canonical ticket's `dup_merged` event).
- **Deferred to slice 3**: fix lane + release callback; the `shipped` stage and
  the `shipped → triaged` filer-dispute edge activate then.

### §10 addenda (slice-2 dual-review hardening)

Fixed after independent + adversarial review of the comms lane:

- **Structural send bound (was an open mail relay).** The raw e2a send tools
  accept `to`/`cc`/`bcc`/`reply_all`, so "reply stays in-thread" was false —
  an injection could relay mail off the verified domain / bcc thread content +
  secrets out. Fix: all sends go through `scripts/comms_send.sh` (reply →
  server-derived recipient; approval → `fix_gate.approver` only; never
  cc/bcc); the raw `send_message`/`reply_to_message`/`forward_message` tools
  are **disallowed**. Recipient bounding is now structural. Added
  `comms.e2a_api_url`; `comms_send.sh` has a `_selftest`.
- **`get_message` read-on-fetch broke the loop (HIGH).** Fetching a message
  marks it read, so triage's classify-pass consumed approver/dispute replies
  before comms could route them. Fix: both lanes classify from the
  `list_messages` **summary** (`conversation_id`) and call `get_message` ONLY
  on a message they own and will act on — triage fetches only non-matching
  (new feedback), comms only matched replies.
- **Approval-gate bindings hold; the actuator is prompt-gated.** A filer/third
  party cannot forge an approval (conversation_id + `authenticated_from` +
  bot-only trust). But `agent-fix` is applied by `gh issue edit` with no
  structural tie to the verified branch — documented honestly (PR-merge is the
  real fence). Hardened: a null/empty `approval.conversation_id` never matches.
- **`security-invariants.md` corrected**: the comms key is read+**send**, not
  read-only; its mail-egress is recipient-bounded by the wrapper.
- **Coherence fixes**: decline now relabels (drop `status:awaiting-approval`,
  add `wontfix`, close `not_planned`); `contact` added to the ticket-card
  schema; the `feedback_status` dangling reference removed; over-granted e2a
  tools trimmed; the `closed_duplicate`/`closed_noise` dispute branches marked
  deferred (no issue exists for those in v0).
- **Accepted residuals (documented)**: inbound is at-most-once (no inbound
  ledger — read-on-fetch); double-send window across runs; cross-thread body
  discipline is prompt-level (recipients are structural); escalation/
  unsubscribe detection is prose judgment.

### §10 addenda (slice 3: fix + release lanes)

Built on `main`. The coding agent + the merge→shipped callback complete the loop:

- **Fix lane** (`workflows/feedback-fix.yml.tmpl` + `runtime-skill/fix.md`):
  `claude-code-action` gated on the `agent-fix` label. Credential inventory =
  Anthropic token + App token + a throwaway local verify stack
  (`verify_setup_script`, e2a's example at
  `examples/e2a/agentify-fix-verify-setup.sh`). **Zero backend/cloud/prod
  secrets** — blast radius is a rejected PR. The agent reads only the
  bot-authored issue + OWNER/MEMBER comments, opens ONE PR with a
  `customer-note` block + a bot-authored `<!-- {marker} fix:#N -->` footer, and
  stops. A **non-agent post-step** (App token, no backend secret in the github
  store) records `in_progress` + the PR number on the ticket-card, captures the
  `customer-note` into the card, relabels, and requests the `reviewer`.
- **Release callback** (`workflows/feedback-released.yml.tmpl` +
  `scripts/released_markers.sh`): on push to main, resolves merged PRs for the
  SHA and flips ticket #N → `shipped` (close `completed`) for each
  **bot-authored** PR carrying `fix:#N`. `released_markers.sh` enforces
  bot-author + footer placement (a forged/human marker is ignored); it has a
  `_selftest`. Idempotent: already-`shipped`/closed tickets are skipped (the
  triage sweep reconciles real misses).
- **Activations**: the triage `in_progress` sweep is live (PR merged >24h →
  shipped; closed-unmerged → triaged) — `gh pr list`/`view` added to the triage
  allowlist (`gh pr merge` denied). The comms `shipped` notification + the
  `shipped → triaged` filer-dispute reopen are active; `shipped.md` slots the
  card's `customer_note` (no PR read needed by comms).
- **Marker change**: PR↔issue link is `fix:#N` in the PR body (github store
  uses the issue number as the id; no minted `fbk_`).
- **Accepted residuals**: the PR-find is a local scan of ≤30 open PRs (search
  index lags); `claude-code-action`'s broad `Bash/Edit/Write` is the fix
  agent's necessary surface — bounded by zero prod creds + the human merge gate,
  not by a narrow allowlist.

### §10 addenda (slice-3 dual-review hardening)

Fixed after independent + adversarial review of the fix + release lanes:

- **CRITICAL (no attacker): `released_markers.sh` digit-leak.** The issue-number
  extraction ran over the whole marker, so `e2a-feedback` (the `2`) made every
  merge emit a phantom `#2`. Anchored to `fix:#[0-9]+ → [0-9]+$`; the `_selftest`
  now uses a digit-containing marker so it can't hide again. (Lesson: an
  unrepresentative fixture passed a broken function.)
- **Cross-ticket marker forgery (injection-as-bot).** The "user text never
  reaches a PR body" argument fails when the bot writing the PR is the injection
  target — it could smuggle `fix:#<other>`. The release callback now ships #N
  ONLY if #N's own ticket-card `pr` is set AND that PR is `MERGED` (a forged
  marker in an unrelated PR can't match #N's recorded PR). Per-issue guards
  (`|| continue`) stop a missing/forged card from aborting the whole step.
- **PR-find prefix collision** (both reviewers): `contains("fix:#$ISSUE")` matched
  `#4` inside `#42`. Now matches the footer form `fix:#<n> -->` via real `jq
  --arg` (no program interpolation).
- **Config-driven labels**: the fix/release relabels parsed their `status:*` /
  `agent-fix` labels from config (were hardcoded literals); skill-prose status
  labels normalized to `{labels.status_*}`. The triage missed-callback sweep now
  drops `status:in-progress` on `shipped`/`triaged` for projection parity.
- **`security-invariants.md §2` corrected** (was overstated). The fix lane's
  honest fences are: zero prod creds + **branch protection (now REQUIRED, not a
  checklist nicety)** + the App **without `workflows:write`** + a diligent PR
  review (incl. the now-visible customer-note and any config diff). Residual:
  run-env token exfil needs no merge (network-egress restriction is the future
  hardening).
- **Customer-note** made review-salient (visible heading; content already renders
  between the markers) — PR review is its gate.
- **Lower residuals documented**: `Fixes #<n>` auto-closes at merge independent of
  the callback (the triage sweep reconciles a card left `in_progress`); the
  `marker` is interpolated into a grep regex (adopters: alphanumeric+dash only);
  `on: push branches:[main]` is a literal (adopters with a different default
  branch edit it); release-vs-triage-sweep can both set `shipped` (idempotent).
