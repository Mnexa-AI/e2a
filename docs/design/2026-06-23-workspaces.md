# Design: Workspaces (multi-user teams)

Status: draft · 2026-06-23 · owner: @jiashuoz

## 1. Problem statement

e2a is single-user-per-account today. Every owned resource —
`domains`, `agent_identities`, `api_keys`, `account_limits`,
`account_usage`, `usage_events` — FKs directly to `user_id`. A user
authenticates via Google OAuth (web session) or a scoped API key
(`account` / `agent`, enforced in `internal/httpapi/scope.go`).

We want a **Workspace = team** model: a founder creates a workspace,
invites colleagues, and the team jointly manages agent emails, domains,
and keys. A user can belong to and create multiple workspaces.

## 2. Goals and non-goals

**Goals**
- A workspace is the **billed tenant**: it owns agents, domains, keys,
  limits, and usage. Individuals own nothing directly — access is
  membership.
- Each user has **exactly one default workspace**, auto-created at signup;
  they invite teammates *into it*. A user can be a *member* of others'
  workspaces (via invitation), so a user may belong to several — but
  **creating additional workspaces is deferred** (see non-goals).
- Invite by email with an **admin / member** role ladder.
- Preserve the existing scope ceiling (`account` / `agent`) and compose
  it with roles.
- A two-phase, idempotent, non-destructive migration from the current
  single-user model (every existing user gets a personal workspace).

**Non-goals (v1)**
- **Creating additional workspaces.** v1 ships exactly one default
  workspace per user (created at signup), into which they invite a team.
  `POST /v1/workspaces` (and workspace deletion as a standalone op) are
  deferred to a future "multiple workspaces" feature. This removes
  resource-transfer-between-workspaces, per-user workspace caps, and
  create/delete lifecycle from v1 scope. Multi-*membership* (a user in
  their default + workspaces they're invited to) **is** in scope, so the
  active-workspace selector stays.
- SSO / SAML / SCIM — that is the separate WorkOS conversation; deferred.
- Per-resource ACLs / per-agent assignment (role is workspace-wide).
- Nested workspaces / sub-teams, custom roles, cross-workspace agent
  sharing.
- **Billing mechanism** (Stripe, pricing, per-seat charging, the sidecar
  that writes caps) — this is OSS and stays billing-mechanism-free;
  monetization lives in the private ops repo. This doc defines only the
  generic limit/usage tenancy seam (§4.7).

## 3. Relevant context and constraints

- `Principal` (`internal/identity/store.go:3270`) = `User + Scope +
  AgentID`. `requirePrincipal` / `requireAccountScope` /
  `requireAgentAccess` are the only authz choke points.
- Scope is a *credential blast radius*, not a role: `account` = workspace
  admin reach; `agent` = pinned to one inbox. Already documented as
  "workspace admin" in `apikeys.go` — the ceiling anticipated this.
- API keys are hashed, scope-aware (`e2a_acct_…` / `e2a_agt_…`), and
  carry an optional `agent_id` FK (`api_keys`, migration 034).
- Billing is external: a Stripe sidecar writes `account_limits`
  (`plan_code`, caps, `upgrade_url`) keyed by `user_id`; the server only
  reads + enforces. Changing the key to `workspace_id` is a coordinated
  change in that external repo.
- Migrations **must be idempotent and non-destructive on prod-sized
  tables** (`CLAUDE.md`). `ADD COLUMN` is safe; avoid `ALTER COLUMN TYPE`
  / table rewrites on `messages` and `usage_events`.
- `/v1` just shipped (Huma, spec golden no-drift gate). Re-pathing the
  whole surface is expensive; prefer additive routes + a workspace
  context header over `/v1/workspaces/{id}/...` prefixing everything.

## 4. Proposed design

### 4.1 Data model

New tables:

```sql
workspaces (
  id          TEXT PK,            -- ws_…
  name        TEXT NOT NULL,
  created_by  TEXT FK users(id),
  created_at  TIMESTAMPTZ
)

workspace_members (
  workspace_id TEXT FK workspaces(id) ON DELETE CASCADE,
  user_id      TEXT FK users(id)      ON DELETE CASCADE,
  role         TEXT NOT NULL CHECK (role IN ('admin','member')),
  invited_by   TEXT FK users(id),
  created_at   TIMESTAMPTZ,
  PRIMARY KEY (workspace_id, user_id)
)

workspace_invitations (
  id           TEXT PK,            -- inv_…
  workspace_id TEXT FK workspaces(id) ON DELETE CASCADE,
  email        TEXT NOT NULL,
  role         TEXT NOT NULL,
  token_hash   TEXT NOT NULL,      -- SHA256 of the bearer token (cf. api_keys)
  invited_by   TEXT FK users(id),
  status       TEXT NOT NULL,      -- pending | accepted | revoked | expired
  expires_at   TIMESTAMPTZ,
  accepted_at  TIMESTAMPTZ,
  created_at   TIMESTAMPTZ,
  UNIQUE (workspace_id, email) WHERE status = 'pending'
)
```

**Retrofit `workspace_id` onto every owned table — audited from every
`REFERENCES users(id)`, classified workspace-owned vs identity-owned:**

Tables are split into two tiers. **Workspace-owned** tables re-key to
`workspace_id` and **drop** their `user_id → users ON DELETE CASCADE`.
**Identity-owned** tables stay keyed to the *human* and **keep** their
`user_id ON DELETE CASCADE` (a credential/session must die with the human,
not with a workspace). Getting this split wrong is blocker B2.

**Workspace-owned (re-key `workspace_id`, drop user-cascade):**

| Table | Action | Note |
|---|---|---|
| `domains` | re-key | re-point `ClaimOrCreateDomain` claim/squat logic + the `uniq_domains_primary_per_user` partial index (`013`) `user_id` → `workspace_id` (else two members each set a primary) |
| `agent_identities` | re-key | source of every message's workspace via `agent_id` |
| `api_keys` | re-key | + `created_by TEXT FK users(id) ON DELETE SET NULL` (audit/revoke); **no** `role` column (§4.3.1) |
| `account_limits` | re-key (PK flip → `workspace_id`) | external sidecar writer must re-key too (§8 #5) |
| `account_usage` | re-key (PK flip → `workspace_id`) | **+ re-key the storage trigger & its `ON CONFLICT` target** (§4.7/§4.8 B3) |
| `usage_events` | add col | standalone `id` PK; `user_id` is `ON DELETE SET NULL` → already has NULL rows (B2) |
| `usage_summaries` | re-key (PK flip) | per-month message-cap counter; enforced at send |
| `suppressions` | re-key | flip `UNIQUE(user_id,address)` → `(workspace_id,address)`; else cross-member leak |
| `webhooks` | re-key | hot inbound-routing query `WHERE … enabled`; outbox fan-out is per-workspace now |
| `webhook_events` | re-key | the outbox/event log §4.7's hook builds on; re-point its 3 hot indexes + the `outbox.go` INSERT + `GET /v1/events` (`events_api.go` hard-codes `user_id`) — else the shared event log fragments per member |
| `webhook_signing_secrets` | re-key | **behavior change** — agents in a workspace sign `X-E2A-Auth-*` with that workspace's secret set (below) |
| `idempotency_keys` | re-key (PK flip) | **dedup domain widens to the workspace** (below) |
| `messages` | none | owned via `agent_id → agent_identities.workspace_id` (below) |
| `send_attempts` | none | no `user_id`; owned via message → agent |

**Identity-owned (keep `user_id ON DELETE CASCADE`, do *not* re-key):**

| Table | Note |
|---|---|
| `user_sessions` | a browser session belongs to the human; dies with them |
| `oauth_auth_codes` / `oauth_access_tokens` / `oauth_refresh_tokens` | a user-consented token belongs to the human — must be revoked when the user is deleted (B2: else a deleted human keeps working bearer tokens until TTL — security + GDPR hole). OAuth *resolution* needs no `workspace_id` column: the `WorkspaceID` lives in the serialized `request` JSONB (`retention.go`) and rehydrates per use |

`api_keys` gains `created_by`; the other workspace-owned tables add/re-key
`workspace_id`. **Audit FKs** — `workspaces.created_by`,
`api_keys.created_by`, `workspace_invitations.invited_by`,
`workspace_members.invited_by` — are all `ON DELETE SET NULL` (so deleting
a user neither blocks on nor cascades through a surviving service key).

**Webhook signing:** the personal-workspace backfill carries each user's
existing signing-secret *set* (it is an N-row rotation set per tenant, not
a single key) onto their workspace; agents in a workspace verify against
that set. Note for offboarding: a removed member's old secret still
verifies until rotated — document in the changelog.

**Idempotency dedup widens to the workspace.** Re-keying `idempotency_keys`
to `(workspace_id, key)` means two members in one workspace replaying the
same key now collide (mostly a spurious `422` on a second member's
differently-bodied send). This is a deliberate choice — document it; add a
two-members-one-key test.

**Cascade-drop is scoped to workspace-owned tables only (B1/B2).** Their
`user_id → users ON DELETE CASCADE` FKs are dropped (the `workspace_id` FK
to `workspaces` becomes the cascade owner); `user_id` is retained for audit
without cascade. Identity-owned tables keep their cascade. **Move the
cascade-drop into the deferred Migration B, not the first deploy** — see
the rollback note in §4.8.

`users` becomes pure identity (`id, email, name, google_subject`) and
owns nothing directly.

**`messages`**: NOT given a `workspace_id` column — a message is owned via
`agent_id → agent_identities.workspace_id`. This dodges a backfill/rewrite
on the largest table. Queries that need workspace scoping join through the
agent. (Open Q if hot query paths demand denormalization.)

### 4.2 Principal & active-workspace resolution

`Principal` gains `Workspace *Workspace` and `Role string`. Every
authenticated request resolves to **(user, workspace, role, scope)**.

- **API key**: a key is minted *inside* a workspace, so
  `api_keys.workspace_id` makes the workspace intrinsic — no ambiguity.
- **Session (web/CLI)**: a user may belong to several workspaces, so the
  active one is explicit via an `X-E2A-Workspace` header (id). Resolution:
  1. **Header present** → verify the user is a *live member* of it; a
     non-member id → 403 (never silent fallback).
  2. **Header absent** → use `last_active_workspace_id` (new column on
     `user_sessions`) **only after re-verifying live membership**; if the
     user was removed from it or it was torn down, fall through.
  3. **Fallthrough** → the user's **default workspace** (deterministic
     `ws_+hash(user_id)`). Every user always has this, so resolution never
     fails or 403s on the no-header path.

  `last_active_workspace_id` is **advisory only** (a UI convenience, never
  an authz input) and written conditionally —
  `UPDATE … WHERE last_active_workspace_id IS DISTINCT FROM $1` — so
  steady-state requests do zero extra writes (fold into §6's hot-path
  budget). Needs an `ALTER TABLE user_sessions ADD COLUMN`.

Existing top-level routes (`/v1/agents`, `/v1/domains`, `/v1/account`, …)
keep their paths and simply operate within the resolved workspace. This
avoids re-pathing the freshly-shipped surface.

#### 4.2.1 Credential inventory

Five credential kinds exist in the new model (four authenticate API
requests; the fifth only accepts an invite). The details of the authority
column are developed in §4.3.1–4.3.3.

| Credential | Form | Used by | Workspace resolution | Admin-capable? |
|---|---|---|---|---|
| **Web session** | `e2a_session` cookie (Google OAuth, 7d) | humans in dashboard + CLI login handoff | `X-E2A-Workspace` header (membership-verified) → else re-verified `last_active` → else the default workspace (§4.2) | ✅ bounded by live role |
| **Account API key** | `e2a_acct_…` | headless code acting across all the team's agents | intrinsic (`api_keys.workspace_id`) | ❌ never (member-capped) |
| **Agent API key** | `e2a_agt_…` | headless code pinned to one inbox (least privilege) | intrinsic (`workspace_id` + bound `agent_id`) | ❌ never (member-capped) |
| **OAuth access token** | `ate2a_…` | MCP clients (interactive consent) | `agent` scope → bound agent's workspace; `account` scope → workspace **pinned into the OAuth session at consent** | ❌ never (member-capped, v1) |
| **Invitation token** | single-use bearer (stored as `token_hash`) | an invitee accepting a workspace invite | the invitation's `workspace_id` | n/a — authorizes only `POST /v1/invitations/{token}/accept` |

Two cross-cutting rules: **(1) no API key or token carries admin
authority — admin is reachable only by a human web session (§4.3.1);** MCP
is capped at member in v1 (admin-over-MCP is deferred — §4.3.2). **(2)**
keys/tokens are workspace-intrinsic (pinned at mint), whereas a human
session spans the user's workspaces and selects the active one per request
via the header.

**Keys vs. user-consented tokens differ on membership (B4).** An API key is
a *workspace service credential*: it survives the minter leaving (§4.3.1).
An `ate2a_…` OAuth/MCP token is a *user-consented* credential, so its
authority must track the **consenting user's live membership**: token auth
(agent *and* account scope) re-verifies `user ∈ workspace_members(resolved
workspace)` on every request, and `DELETE …/members/{user}` revokes that
user's OAuth grants for the workspace. Otherwise a removed member's token
keeps acting as a team agent. Today token resolution only checks
`ag.UserID == u.ID` (`api.go`) — a predicate the `agent_identities` re-key
drops, so this live-membership check is its required replacement.

**`account`-scope OAuth (was undefined).** An `account`-scope `ate2a_…`
token resolves to a principal with no agent and no workspace. v1 fix: pin a
`WorkspaceID` into `oauth.Session` at consent and resolve from it. Two
guards: (a) a **pre-migration token** whose session has empty `WorkspaceID`
**fails closed** (401 / force re-consent) — never falls back to a default;
(b) account-scope is in fact **unreachable in v1** anyway (DCR caps clients
to `scope=agent` — see §4.3.2), so the practical requirement is just the
fail-closed branch; the full agent-less account-scope flow ships with
admin-over-MCP. Cover with an authz-matrix test.

### 4.3 Roles × scope

Scope (credential blast radius) and role (member authority) **compose**;
the effective permission is the *intersection*.

Two roles. The split is **team/workspace administration (admin) vs.
resource operation (member)** — members run the workspace's infrastructure
(agents, domains, keys); admins additionally manage the people, the
workspace lifecycle, and billing. The workspace creator is the first
admin. Admins are peers — no super-admin/owner above them.

| Capability | member | admin |
|---|---|---|
| Act as / send-receive on workspace agents | ✓ | ✓ |
| Read workspace resources (agents, domains, members, usage) | ✓ | ✓ |
| Create / delete agents | ✓ | ✓ |
| Claim / verify / delete domains | ✓ | ✓ |
| Create / revoke workspace API keys | ✓ | ✓ |
| Invite members, remove members, change roles | | ✓ |
| Rename workspace, billing | | ✓ |

*(Workspace **deletion** is not an admin-reachable operation in v1 — it
happens only internally on account deletion of a sole member; §2, §5.)*

So the line is purely **administering the team and the workspace**, not
operating it: everything an individual single-user account can do today
(manage agents, domains, keys) is the **member** floor; admin adds people
management + workspace lifecycle + billing on top.

New choke point `requireWorkspaceRole(ctx, minRole)` alongside the
existing scope helpers. Resource ops (agents/domains/keys) require
`requireWorkspaceRole("member")` — i.e. any member. People/workspace/
billing ops require `requireWorkspaceRole("admin")`. `requireAccountScope`
stays (bars agent-pinned credentials from these ops); agent-scoped keys
remain pinned to one agent *and* to that agent's workspace.

#### 4.3.1 Admin is session-only; all keys and tokens are member-capped

The v1 rule is simple and strict: **admin authority is reachable only
through a human web session.** No API key (`e2a_acct_…` / `e2a_agt_…`) and
no OAuth/MCP token (`ate2a_…`) can perform an admin-only operation,
regardless of who minted it or how. The dashboard (session-authenticated,
bounded by the member's live role) is the sole path to invite/remove
members, change roles, rename a workspace, or touch billing.

This keeps the leak surface minimal: the credentials that actually leak —
CI-resident keys, tokens in client configs — top out at the member floor,
so a leak can destroy nothing team-/workspace-/billing-level. (Admin over
MCP was considered and deferred — §4.3.2.)

##### API keys — member-capped

A key has three attributes, kept orthogonal so a leaked key can never
widen its own authority:

- **`workspace_id`** — the tenant the key acts in (intrinsic; resolves the
  active workspace for key-auth requests with no header needed).
- **`scope`** (`account` | `agent`, unchanged on the wire) — blast radius
  across agents: `account` = any agent in the workspace + manage
  agents/domains/keys; `agent` = pinned to one agent.
- **`created_by`** — the minting user, for audit and revoke-scoping.

There is **no `role` axis on keys**. Every key tops out at the **member
floor** — operate agents, domains, and keys within its one workspace. The
admin-only operations (invite/remove members, change roles, rename
workspace, billing) are unreachable by any API key, regardless of who
minted it. A leaked key can destroy nothing team-/workspace-/billing-level.

Consequences:
- Both members and admins mint keys; since no key carries admin authority,
  an admin-minted key and a member-minted key have identical power. No
  "capped at minter's role" logic is needed.
- **Revoke**: the creator may revoke keys they created; an admin may revoke
  any key in the workspace. `list` shows `created_by` so ownership is
  legible. (Secrets are still shown only once, at creation.)
- **Offboarding (keys ≠ tokens).** Keys are workspace **service
  credentials**; their default on offboarding depends on *why*:
  - **Voluntary leave** → keys with `created_by = the leaver` **survive**
    by default (CI must not break when someone quits); the flow surfaces
    "this member created N keys."
  - **Involuntary remove** (possible bad actor) → default to **revoking**
    those keys (or hard-prompt) — survive-by-default is the wrong default
    for a removal. Add a §7 test that a removed member's *surviving* key
    still authenticates, so this is a conscious, tested decision.
  - User-consented **OAuth/MCP tokens** are *not* service credentials and
    do **not** survive removal — they track live membership (§4.2.1 B4).
- **`scope=account` is redocumented** as "workspace-wide" (member-level),
  not "workspace admin" — agent/domain/key management is now the member
  floor, so the word "admin" no longer applies to it. Wire value and
  `e2a_acct_` prefix are unchanged for compatibility.
- **Workspace deletion** is internal-only in v1 (no endpoint — §2/§5); not
  reachable by any credential.

##### 4.3.2 Admin over MCP — deferred (not in v1)

Letting an admin drive team management through an MCP client was
considered and **deferred out of v1.** The honest reason: doing it safely
is a real OAuth project, not a property we can assert. e2a's OAuth tokens
are **opaque HMAC, not JWTs** (`internal/oauth/strategy.go`), consent
currently grants *every* requested scope unconditionally
(`oauth_handlers.go` — `GrantScope` over all requested scopes), there is
no path to issue an agent-less workspace-only token, the OAuth session
carries no `WorkspaceID`, and DCR public clients are hard-capped to
`scope=agent` (`oauth_handlers.go`) so a `workspace:admin` scope could not
even be requested. A correct design needs: selective consent gated on live
membership, an agent-less consent branch, `WorkspaceID` on `oauth.Session`,
a live `min(scope, role)` re-check at request time, and a non-DCR issuance
flow for the privileged scope.

None of that is needed for the workspace feature itself — **the dashboard
already gives admins full authority** — so v1 caps MCP at the member floor
and admin stays session-only. Admin-over-MCP is a candidate for its own
later design once the workspace core has shipped.

##### 4.3.3 Choosing an agent-capable credential (why `e2a_agt_` stays)

Agent-pinning (one inbox vs. all) and authority (admin vs. member) are
**orthogonal** axes. The role/MCP changes touched authority; they did not
collapse the agent-pinning axis, so all three agent-capable credentials
keep distinct niches:

| | One agent | All workspace agents |
|---|---|---|
| **Static / headless** (no human, set-and-forget) | **`e2a_agt_…`** | `e2a_acct_…` |
| **Interactive** (browser consent, refreshable) | `ate2a_…` OAuth | — |

`e2a_agt_…` is the **only** way to obtain a statically-minted, single-inbox,
least-privilege credential — e.g. a long-running support bot on
`support@acme.com` deployed to a server with no interactive login, scoped
so a leaked env var exposes one inbox, not the team's whole agent fleet.
The alternatives don't fill that cell: `e2a_acct_…` violates least
privilege (a leak exposes every agent), and `ate2a_…` requires the
interactive consent flow (unsuitable for a headless service). So `e2a_agt_`
is retained.

### 4.4 New API surface

```
# Workspaces  (no POST/DELETE in v1 — creation/teardown deferred; see §2)
GET    /v1/workspaces                       list the ones I belong to (+ my role)
GET    /v1/workspaces/{id}                  any member
PATCH  /v1/workspaces/{id}                  rename — admin (e.g. "Josh's Workspace" → "Acme")

# Members (the permission CRUD)
GET    /v1/workspaces/{id}/members           list members + roles — any member
PATCH  /v1/workspaces/{id}/members/{user_id} set role admin|member — admin
DELETE /v1/workspaces/{id}/members/{user_id} remove member, or self = leave

# Invitations (create = grant access to a not-yet-member)
POST   /v1/workspaces/{id}/invitations       {email, role} — admin
GET    /v1/workspaces/{id}/invitations       list pending — admin
DELETE /v1/workspaces/{id}/invitations/{id}  revoke pending — admin
POST   /v1/invitations/{token}/accept        invitee accepts (token + email match)
```

`PATCH …/members/{user_id}` (promote to admin) *is* the transfer-admin
mechanism — admins are peers, so no separate endpoint is needed.

`GET /v1/account` (whoami) extends with additive `AccountView` fields:
active `workspace` (id + name) and the caller's `role`. The shape is
backward-compatible (additive), but `Limits`/`Usage` now reflect the
**active workspace** rather than the user — a semantic change to document.

**`DELETE /v1/account` / `GET /v1/account/export` reconciliation.** Both
exist today as `user_id`-scoped GDPR endpoints; post-migration their
meaning must be pinned: account-delete becomes "delete my user identity +
memberships" (subject to the sole-admin guard in §5), **not** a cascade of
workspace resources; export scopes to the active workspace (or enumerates
the user's workspaces). Spell this out before implementation.

**`X-E2A-Workspace` must be modeled in the OpenAPI contract**, not added
silently in middleware — an undeclared header is invisible to the SDKs and
trips `TestSpecGoldenNoDrift`. Model it as a **Huma header input field**
(the way `Idempotency-Key` is — a shared embed across the relevant ops),
**not** like `Authorization` (which is a `SecurityScheme`, not a header
parameter — following that literally yields no spec parameter and an
SDK-invisible header). Precedence on key/OAuth auth: the workspace is
intrinsic, so a matching header is ignored and a header naming a
*different* workspace → 400 (it is a session-only selector). Add
header-present × key-auth and × OAuth-auth matrix rows.

### 4.5 Signup & workspace creation

Every user always has at least one workspace they administer — there is no
"no workspace" state. Provisioning a personal workspace happens through a
**single shared helper** (`ensurePersonalWorkspace(tx, userID, name)`) so
that **no** user-creation path can bypass it (blocker B3):

- **Both creation call sites use the helper.** `CreateOrGetUser` (the
  Google-OAuth path, defined in `internal/identity/store.go`, called from
  `internal/auth/auth.go`) **and** `BootstrapUser`
  (`internal/identity/store.go`, the `-bootstrap-email` first-run) today
  run a bare `s.pool.QueryRow` and call `EnsureUserHasSigningSecret` in its
  *own* tx (`withUserSecretsLock`). The refactor: thread a `tx` through both,
  add a tx-accepting signing-secret variant, and call
  `ensurePersonalWorkspace` in the **same tx** as the user insert.
  `CreateOrGetUser` returns a new-vs-returning discriminant (`xmax=0` on the
  `ON CONFLICT DO UPDATE … RETURNING`) so a returning login does not
  double-provision; the helper is idempotent regardless
  (`INSERT … ON CONFLICT (id) DO NOTHING` for the workspace and
  `ON CONFLICT (workspace_id,user_id) DO NOTHING` for the membership).
- The personal workspace is named `"{Name}'s Workspace"` (fallback: the
  normalized email local-part) and the user is inserted as **`admin`** of
  it — full control by construction.
- **Deploy ordering (B3) — generalized to *all* owned-row INSERT paths.**
  The pre-A binary must already be `workspace_id`-aware on **every** path
  that mints an owned row, not just user creation. The dangerous repeat
  offenders the audit found: `EnsureSharedDomain` (`store.go` — re-creates a
  NULL-workspace domain on *every boot*), `EnsureUserHasSigningSecret`
  (`store.go` — mints a NULL row on *every login*), and the old binary's
  `CreateAgentTx` / `ClaimOrCreateDomain` / `CreateAPIKey` during the
  rolling restart. Any NULL row these mint between Migration A and full
  rollout reaches Migration B's `VALIDATE … NOT NULL` and aborts it.
  Mitigations: ship the helper + workspace-aware writes **before/with**
  Migration A, and **gate Migration B's VALIDATE on a per-table
  `count(*) WHERE workspace_id IS NULL = 0` precondition** so a stray
  window row is healed (re-run the backfill) rather than wedging the
  rollout. Migrations run once — `internal/identity/migrate.go`.
- **The default workspace is the team workspace.** Since v1 has no
  create-additional, a founder forms a team by inviting people *into their
  own default workspace* (rename it to the company, §4.4). They are its
  `admin` by construction — no separate "create team workspace" step.
- **Accepting an invitation** does *not* suppress the invitee's own default
  — they keep it (as admin) and additionally join the inviting team, so
  they may belong to several workspaces (multi-membership; the
  active-workspace selector picks which one a session acts in, §4.2).

### 4.6 Invitation flow

1. Admin `POST …/invitations {email, role}`. The email is case-folded via
   the existing `NormalizeEmail` (`internal/identity/email.go`) — case only,
   **not** Gmail dot/plus stripping — before storing and before the
   `UNIQUE(workspace_id,email) WHERE status='pending'` check. Server stores
   a `pending` row and a **≥128-bit CSPRNG token** (the `api_keys` path is
   256-bit) with a prefix (`e2a_inv_…`), persisting only `token_hash`, then
   emails an accept link. (A pending invite is not yet a seat — seat count
   changes only at accept; §4.7.)
   - **Invite-existing-member** → `409 already_member`, with a machine code
     pointing the caller at `PATCH …/members/{id}` for role changes
     (invitations never mutate an existing member's role — `PATCH` is the
     sole role writer). Same for same-role, different-role, and self-invite
     (all collapse to this path).
   - **Rate-limited.** Invitations are volume-capped per workspace (reuse
     `ratelimit.go`) so a compromised admin session can't turn invites into
     a spam cannon burning SES reputation/quota.
2. **Sender path:** the invite email goes through the system-mail path
   (an invite-appropriate `noreply@{fromDomain}` sender — *not* the literal
   `hitl-noreply@` and *not* the agent-keyed `outbound.Sender`, which needs
   a verified-domain agent).
3. Invitee opens the link → web `/invite/accept?token=…`. If not signed
   in, Google-OAuth first (provisioning their own personal workspace via
   the §4.5 helper if new), then `POST /v1/invitations/{token}/accept`.
4. **Accept is one transaction:** `SELECT … FOR UPDATE` the invitation row;
   re-check `status='pending' AND not expired` and that the authenticated
   user's normalized email equals `invitation.email`; `INSERT
   workspace_members … ON CONFLICT (workspace_id,user_id) DO NOTHING`; flip
   `status → accepted`. Token possession **and** email match are both
   required, and the status-flip-in-tx is the single-use guard.
   - **Idempotent double-accept** (retry / double-click by an
     already-joined user) → **200**, not an error.
   - **Email mismatch** (signed in under a different Google address than
     invited) → **403** whose envelope names expected-vs-actual ("signed in
     as X, invite is for Y — switch accounts"). Whether an admin can
     re-target a pending invite's email is a follow-up decision.
   - **Torn-down invite** (workspace deleted, or revoked/expired) → token
     resolves to no live `pending` row → **410 gone**, fail closed.
5. **Re-join after leave/remove**: requires a *fresh* invitation (the prior
   token is consumed); since remove hard-deletes the membership row, the
   re-accept INSERT is clean. Re-invite upserts the pending row; revoke
   flips it to `revoked`.

### 4.7 Limits, usage & tenancy seam (billing-mechanism-free)

This is **OSS** and stays billing-mechanism-free, preserving the existing
open-core seam: the server only ever *reads* caps and enforces them; it
never knows about Stripe, prices, or seats-as-dollars. Monetization (the
Stripe integration, per-seat pricing, the sidecar that *writes* caps)
lives in the private ops repo and is out of scope here — see the non-goal
in §2 and the pointer in Open Q5/Q6.

What this doc defines is only the generic seam the workspace model needs:

- **Workspace is the limit/usage tenant.** `account_limits` /
  `account_usage` re-key to `workspace_id` (PK); `usage_events` /
  `usage_summaries` carry `workspace_id`. The server reads `account_limits`
  and enforces caps exactly as today — the values are opaque and
  operator-populated (a missing row → operator default, so a solo personal
  workspace works with no external system at all).
- **Re-key the storage trigger (don't forget it).** `account_usage` is
  written *only* by the `e2a_messages_storage_delta` trigger
  (`migrations/039_…`), which today resolves `user_id` from
  `agent_identities` and upserts `ON CONFLICT (user_id)`. The re-key must:
  backfill `agent_identities.workspace_id` **first**, then
  `CREATE OR REPLACE` the trigger to resolve and upsert by `workspace_id`,
  **in the same deploy** that re-keys the table (and re-point the 016
  usage backfill `GROUP BY`). If the table is re-keyed without the trigger,
  every `messages` write aborts — a delivery-breaking outage. This is the
  most dangerous single step in the migration.
- **Seat count is observable, not priced.** A workspace's member count is
  just `SELECT count(*) FROM workspace_members WHERE workspace_id = $1`.
  The OSS server derives this; it attaches no cost to it. Any
  metering integration (the proprietary sidecar, a self-hoster's own
  script, or nothing) reconciles against this count out-of-band. The
  server does not call out to a billing system on membership changes.
- **Optional extension hook (audience-neutral).** The existing outbox
  already reserves non-`webhook` audiences via its `aud` column. If a
  deployment wants low-latency metering instead of polling, a membership
  change *may* emit a generic internal event on that outbox; the OSS code
  treats it as an opaque extension point with no billing semantics. A
  self-host with no metering simply never consumes it.

The proprietary layer maps Stripe customer ↔ workspace, decides whether
seats/agents/messages cost money, and writes the resulting caps back into
`account_limits` — all without any code in this repo. That boundary is the
whole point of the open-core split.

### 4.8 Migration (two-phase, idempotent)

**Pre-req (B3):** the §4.5 `ensurePersonalWorkspace` helper ships **before
or atomically with** Migration A, so no signup/bootstrap in the rollout
window can create a workspace-less user the one-shot backfill can't heal.

**Migration A — additive, safe to run on prod:**
1. `CREATE TABLE IF NOT EXISTS` workspaces / members / invitations.
2. Seed a **protected system/sentinel workspace** (`ws_system`) to own rows
   with no real user (B2): the seeded shared domain `agents.e2a.dev`
   (`user_id IS NULL`) and any `usage_events` rows already NULLed by
   `ON DELETE SET NULL`. `ws_system` is guarded against teardown (§5).
3. Backfill one **personal workspace per existing user**, deterministic id
   (`ws_` + stable hash of `user_id`) so re-runs are no-ops; insert an
   `admin` membership. Idempotency: `ON CONFLICT (id) DO NOTHING` for the
   workspace **and** `ON CONFLICT (workspace_id,user_id) DO NOTHING` for
   the membership (its PK would otherwise abort a re-run).
4. `ADD COLUMN IF NOT EXISTS workspace_id` (nullable) on each workspace-owned
   table (per the §4.1 table).
5. Backfill `workspace_id`: from `user_id → personal workspace` where the
   user exists, else → `ws_system`, so **no row is left NULL**. The large
   `usage_events` backfill runs as a **resumable, idempotent, chunked
   out-of-band script** (`WHERE workspace_id IS NULL`), not a tracked
   migration (the runner is one-tx-*per-file*, multi-statement OK — only
   `e2a:no-transaction` files run un-wrapped — `migrate.go`). Run a final
   sweep *after* the old container is gone, before Migration B.
6. **Constraint flips (B3).** For the small tables whose *primary/unique*
   key is user-scoped, drop+recreate the constraint on `workspace_id`:
   `account_limits` (PK), `account_usage` (PK — **required** before the
   re-keyed storage trigger's `ON CONFLICT (workspace_id)` will work),
   `usage_summaries` (PK), `idempotency_keys` (PK), `suppressions` and
   `uniq_domains_primary_per_user` (UNIQUE). Cheap on these small tables —
   *not* the `messages`/`usage_events` rewrite hazard. The `account_usage`
   storage trigger is `CREATE OR REPLACE`d to resolve+upsert by
   `workspace_id` **in lockstep** with its PK flip (§4.7), keeping its
   `IF workspace_id IS NULL THEN RETURN NEW` guard so window-created agents'
   message writes never abort.

**Code deploy** reads/writes `workspace_id`; authz switches to membership;
replaces user-delete teardown (§5). Existing single-user flows stay
transparent (everyone has exactly one personal workspace).

**Migration B — later, after the code deploy is stable:**
- **Drop the workspace-owned tables' `user_id ON DELETE CASCADE` FKs here
  (B1), not in the first deploy.** Rationale (rollback): if the cascade-drop
  ships in deploy 1 and we roll back to the pre-workspace binary, its
  `DELETE FROM users` silently stops cascading → orphans. Deferring the drop
  keeps deploy 1 rollback-safe; rolling back *past Migration B* is
  documented as unsupported. Identity-owned tables keep their cascade.
- Make `workspace_id` non-nullable **without a blocking rewrite** —
  `ADD CONSTRAINT … CHECK (workspace_id IS NOT NULL) NOT VALID` then
  `VALIDATE CONSTRAINT` (and/or PG12 `SET NOT NULL` backed by the validated
  CHECK), mirroring the repo's `CREATE INDEX CONCURRENTLY` on `usage_events`.
  **Gate each `VALIDATE` on a `count(*) WHERE workspace_id IS NULL = 0`
  precondition** so a stray rollout-window row triggers a re-backfill rather
  than wedging the migration. Add the `workspace_id` FKs. Keep `user_id`
  columns for audit (now without cascade); drop only once nothing reads them.

## 5. Edge cases and failure handling

- **Delete user account (rewritten — B1/B2).** User-delete must **not** rely
  on the (now-dropped) workspace-owned `ON DELETE CASCADE` from `users`. It:
  (1) revokes the user's identity-owned credentials — `user_sessions` and
  the `oauth_*` tables still cascade from `users`, so their live OAuth/MCP
  tokens die with them (the GDPR/security requirement);
  (2) removes the user's `workspace_members` rows and *detaches* — never
  deletes shared resources of workspaces where other members remain;
  (3) re-homes the existing imperative teardown bits that are *not* covered
  by FK cascade: the explicit `DELETE FROM usage_events WHERE user_id`
  (`user_data_rights.go`) becomes tenant-aware (NULL-out for audit, or
  delete only within a torn-down workspace — not shared usage), and the
  per-domain **SES deprovision hook** (`perDomainInTx`) must run for the
  domains of any workspace actually being torn down (a bare FK cascade would
  drop domain rows and orphan the SES sending identities);
  (4) tears down the user's **own default workspace** (sole member) via the
  internal workspace-teardown routine. Resolve the GDPR-erase vs
  workspace-owns-the-usage conflict explicitly. DB-backed tests: deleting a
  member of a multi-member workspace leaves its resources intact; deleting a
  user revokes their OAuth tokens (no orphan).
- **Last admin (B1 — corrected concurrency).** Cannot leave, be removed, or
  be demoted while the only admin — fail closed ("promote another member to
  admin first"). The earlier `count(*) … FOR UPDATE` guard was **wrong**
  (Postgres rejects `FOR UPDATE` with an aggregate, and locking different
  member rows doesn't prevent write-skew — two concurrent demotes both see
  count=2). Correct mechanism: **serialize on a single shared row** —
  `SELECT id FROM workspaces WHERE id=$1 FOR UPDATE` at the top of every
  membership-mutating tx, *then* a plain `count(*) WHERE role='admin'`; or
  `SERIALIZABLE` + 40001 retry (as `user_data_rights.go` already does). Test
  both orderings of two concurrent demotes/leaves.
- **Sole-admin of a *multi-member* workspace deleting their account**: must
  promote another member to admin first (fail closed) — otherwise the
  workspace would be left adminless. A *solo* default workspace has no other
  members, so account deletion simply tears it down. (Resolves Open Q4.)
- **Workspace teardown is internal-only in v1** (no public `DELETE` — §2):
  runs on account deletion of a sole member, cascading owned resources via
  the `workspace_id` FKs (and the SES hook above). `ws_system` is a
  protected sentinel — never torn down.
- **Member removed**: their session loses access on the next request
  (re-resolved). Workspace service **API keys** survive a *leave* but are
  revoked-by-default on an involuntary *remove* (§4.3.1); user-consented
  **OAuth/MCP tokens** are revoked for that workspace (B4). An **in-flight
  request** mid-removal completes under its already-resolved principal; the
  *next* request re-resolves and is denied — document this TOCTOU as
  acceptable (no admin op is reachable by the removed member anyway).
- **Agent address / domain uniqueness**: addresses and domains remain
  **globally unique** (the PK forbids per-workspace namespacing); a second
  workspace cannot claim one already owned → existing `conflict`. The
  re-keyed `ClaimOrCreateDomain` conflict/squat logic keys on
  `workspace_id` so a *different member of the same workspace* can re-claim
  (today's `user_id` predicate would 403 them).
- **Invitation**: full lifecycle/concurrency spec in §4.6 (accept-tx,
  idempotent double-accept = 200, email mismatch = 403, torn-down = 410,
  already-member = 409 `already_member`, rate-limited).
- **Admin-action audit log.** Invite / remove / role-change / rename leave
  zero forensic trail under the "admins are peers" model. Add an `audit_log`
  table (workspace_id, actor_user_id, action, target, created_at) written in
  the same tx as each admin mutation — for a trust-boundary feature, "who
  changed this membership" must be answerable. (New, small surface.)
- **Credential ceiling preserved**: agent-scoped key pinned to one agent +
  its workspace; **no API key or OAuth/MCP token carries admin authority**
  (§4.3.1) — admin is session-only in v1, so no leaked credential can reach
  member/billing/workspace-lifecycle ops.
- **Key revoke by non-creator member**: a member revoking another member's
  key → 403 (only own keys); admin may revoke any. Revoking an already-
  revoked key is idempotent.
- **Header spoofing**: `X-E2A-Workspace` is only honored after membership
  is verified; a non-member id → 403, never silent fallback. On key/OAuth
  auth the workspace is intrinsic, so the header is ignored (or 400 if
  present) — it is a session-only selector.

## 6. Scalability and extensibility notes

- Membership/role lookups are hot **only for session and OAuth auth** —
  key auth needs no `workspace_members` read (role is constant `member`,
  workspace intrinsic to the key). Index `workspace_members (user_id)
  INCLUDE (role)`; the `(workspace_id)` index is redundant with the PK
  `(workspace_id, user_id)`. The row is tiny and cacheable per request.
- `workspace_id` columns + indexes on owned tables keep per-workspace
  listing as fast as today's per-user listing.
- The role enum is a clean seam for a future `roles`/permissions table or
  per-agent assignment without reshaping ownership again.
- Keeping top-level routes (workspace via context) means SDK/MCP clients
  add one header, not a re-pathed surface — cheap to evolve.
- Personal-workspace backfill means the multi-user model degenerates
  gracefully to today's single-user UX for solo users.

## 7. Verification strategy

- **Migration**: idempotency test (run twice, assert stable); backfill
  correctness (each user → exactly one personal ws; every owned row
  mapped; counts conserved).
- **DB-backed authz matrix** (per `CLAUDE.md` schema-change rule — every
  package writing direct SQL against retrofitted tables): role × operation
  × scope, including intersection cases (member + account key, admin +
  agent key, OAuth token × each member op) and **header-present ×
  key-auth / × OAuth-auth** rows.
- **Multi-tenant teardown (B1/B2)**: deleting a member of a multi-member
  workspace leaves shared resources intact; deleting a user **revokes their
  OAuth/MCP tokens** (no orphaned bearer token) and runs the SES
  deprovision hook for any torn-down workspace's domains.
- **Removed-member credentials (B4)**: a removed member's surviving service
  **key** still authenticates (conscious, tested decision); their
  **OAuth/MCP token** is rejected on the next request (live-membership
  re-check).
- **Migration (B2/B3)**: idempotency (run twice, stable); backfill leaves
  **no** NULL `workspace_id` (system rows → `ws_system`); bootstrap-path,
  signup-in-window, and the boot/login NULL-minters (`EnsureSharedDomain`,
  `EnsureUserHasSigningSecret`) provision/own a workspace; the constraint
  flips (`account_usage` PK before its trigger; `idempotency_keys`,
  `usage_summaries`, `uniq_domains_primary`) land before dependent writes.
- **Storage trigger re-key**: `messages` writes still succeed (NULL-guard)
  and `account_usage` accrues to the right `workspace_id` after re-key.
- **Idempotency widening**: two members in one workspace replaying the same
  key collide as specified (§4.1).
- **Last-admin protection** — both orderings of two concurrent
  demotes/leaves under the shared-row lock (B1) — and sole-admin
  user-deletion.
- **Regression**: existing single-user flows unchanged through the
  personal workspace; contract tests; `make generate` + spec golden
  no-drift after handler changes (`X-E2A-Workspace` modeling, redocumented
  `scope` strings).
- **Manual**: dashboard workspace switcher, invite email round-trip
  against Mailpit, multi-workspace key isolation.

## 8. Open questions

1. *(resolved)* **Credential authority** — all API keys *and* OAuth/MCP
   tokens are member-capped; **admin is session-only in v1** (§4.3.1).
   Admin-over-MCP is deferred to its own design (§4.3.2). No headless admin
   credential in v1.
2. **Active-workspace for sessions** — `X-E2A-Workspace` header + context
   (recommended) vs path-scoped `/v1/workspaces/{id}/...` routes. Note:
   needs a `last_active_workspace_id` column on `user_sessions` (not
   present today) + spec-modeling of the header (else `TestSpecGoldenNoDrift`).
3. **`messages.workspace_id`** — derive via agent (recommended; the
   `agent_id ON DELETE CASCADE` invariant means messages never outlive
   their agent, so derivation can't orphan). No workspace-inbox endpoint is
   currently proposed; revisit only if one is.
4. **Delete user with sole-admin workspaces** — block-until-promoted /
   workspace-deleted (recommended) vs cascade-delete those workspaces.
   Includes the solo-personal-workspace path (no promote target → delete).
5. **Private ops repo (out of scope here, must stay in sync)** — re-key
   `account_limits` to `workspace_id`, map Stripe customer ↔ workspace,
   one-time re-map of existing customers from user → personal workspace,
   and reconcile seat count against `workspace_members`. Belongs in the
   proprietary billing design, not this OSS doc; flagged only so the two
   repos deploy compatibly with the schema change.
6. **Pricing model (proprietary, deferred)** — per-seat / per-workspace
   pricing, tiers, free→paid threshold, proration. Does not touch this
   repo beyond the generic seam in §4.7; the OSS server only enforces the
   caps it reads. Open sub-question for OSS: do we want a hard
   `max_members` cap column at all, or leave member count purely
   observable?
