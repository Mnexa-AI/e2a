# Trash: soft delete for inboxes and messages (30-day retention)

Status: implemented (2026-07-12)

## Goal

Gmail/Outlook-style deletion for agent inboxes and messages:

- Deleting an inbox (agent) or a message moves it to **trash** instead of
  destroying it.
- Trashed resources are reviewable in the dashboard and **restorable** for a
  retention window (default **30 days**), then purged permanently by the
  janitor.
- Permanent deletion ("delete forever") is available from the trash.

## Schema (migrations 063-064)

`deleted_at TIMESTAMPTZ NULL` on `agent_identities` and `messages`.
`NULL` = live; non-NULL = in trash since that instant. Partial indexes serve
the trash listings and the purge sweep; both are small (trash is a tiny
fraction of the table) so the write amplification is negligible.

`messages.send_claimed_at TIMESTAMPTZ NULL` is the short-lived lease for an
active async provider call. It is separate from `delivery_status = 'sending'`
because that status can survive a worker crash or span River retry handling.

## Lifecycle semantics

### Messages

- **Trash** (`SoftDeleteMessage`): sets `deleted_at = now()`. Only live
  (`expires_at > now()`, not deleted) messages can be trashed. A held
  outbound draft or held inbound message (`status = 'pending_review'`) can
  NOT be trashed — the review queue is its resolution surface (approve /
  reject first); attempting returns `ErrMessageHeld` → HTTP 409.
- **Hidden while trashed**: every agent-facing read path excludes
  `deleted_at IS NOT NULL` rows — list, conversations, reply/forward anchors,
  unread counts, per-agent dashboard stats. A pending webhook delivery for a
  trashed message stops being claimed (it resumes if the message is restored
  inside the retry window; otherwise the TTL prune drops it), and a
  queued-but-unsent async outbound send no-ops if its message (or its agent)
  was trashed before the worker claims it. The durable `sending` transition and
  trash are serialized: if the worker claims first, trash completes immediately
  but the provider outcome is still recorded on the hidden row; if trash wins,
  the worker records the queued send as canceled without submitting it.
  Retryable, side-effect-free provider failures release the claim back to
  `accepted` during River backoff. Permanent deletion is blocked only by a
  fresh `send_claimed_at` lease; stale leases recover crashed workers or failed
  terminal bookkeeping without blocking deletion forever. Two
  deliberate exceptions: the single-message GET returns trashed rows
  (annotated with `deleted_at`) so the trash view can open them, mirroring
  Gmail's "view message in trash"; and the In-Reply-To / References
  threading lookup (`LookupConversationID`) still resolves conversation ids
  off trashed anchors, so a reply arriving while the original sits in the
  trash keeps threading correctly.
- **Expiry clock pauses in trash**: messages already carry a natural TTL
  (`expires_at`, `MessageTTL` = 10 days). Restore shifts the deadline by the
  time spent in trash (`expires_at += now() - deleted_at`), so a restored
  message gets back exactly the active lifetime it had left when trashed and
  a restore can never resurrect an already-expired husk.
- **Purge**: the janitor's `DeleteExpiredMessages` now deletes
  `(deleted_at IS NULL AND expires_at <= now())` — the pre-existing rule —
  OR `(deleted_at <= now() - TrashRetention)`. While a row is in trash its
  natural expiry is suspended; the trash clock alone governs.
- **Delete forever** (`PurgeMessage`): hard DELETE, allowed only on rows
  already in trash (Gmail journey: delete → trash → delete forever). A fresh
  provider-call lease returns HTTP 409 `send_in_progress`; retry after it ends.

### Agents (inboxes)

- **Trash** (`SoftDeleteAgent`): sets `deleted_at = now()`. While trashed:
  - inbound SMTP lookup misses → mail to the address bounces as unknown
    recipient (`GetAgentByID`/`GetAgentByEmail` filter `deleted_at IS NULL`);
  - every /v1 per-agent operation 404s (resolveOwnedAgent uses the same
    lookup); agent-scoped keys bound to it stop resolving;
  - it disappears from `GET /v1/agents` and its held messages leave the
    review queue (`ListReviews` / `GetReviewWithContent` /
    `ListPendingOutboundForUser` join on non-deleted agents), so the HITL
    TTL sweep also skips them (`ListExpiredPending` / `ListExpiredReviews`
    join filter) — no auto-send on behalf of a trashed inbox.
  - The email address stays reserved (row keeps the PK) — recreating the
    same address conflicts until the trash entry is restored or purged,
    like Gmail address reuse rules. The messages stay attached untouched;
    restore brings the whole inbox back, messages included.
  - A trashed agent does NOT consume a `max_agents` plan slot
    (`usage.CountAgentsByUser` mirrors the live list's trash exclusion), so
    a replacement can be created immediately.
- **Message clocks pause with the inbox**: the janitor's natural-expiry arm
  skips messages whose agent is trashed, and `RestoreAgent` shifts every
  live message's `expires_at` — and a still-held draft's
  `approval_expires_at` — forward by the time spent in the trash. Restore
  therefore returns the inbox exactly as it was: no mail silently expired
  mid-window, and no held draft auto-resolves because its review TTL
  "lapsed" while the inbox was invisible.
- **Purge**: janitor `PurgeDeletedAgents` hard-deletes agents with
  `deleted_at <= now() - TrashRetention`, one agent per transaction, its
  messages deleted explicitly BEFORE the agent row (not via `ON DELETE
  CASCADE`) — the storage-metering trigger resolves the owning user through
  the agent row, so a cascade would leak the bytes in
  `account_usage.storage_bytes` forever. `DeleteAgent` (the permanent
  delete) drains the same way.
- **Delete forever**: `DELETE /v1/agents/{email}?permanent=true&confirm=DELETE`
  hard-deletes from either state (trash UI uses it on trashed inboxes; API
  callers keep a one-shot irreversible delete). A fresh provider-call lease on
  any attached message returns HTTP 409 `send_in_progress`.
- Domain deletion still counts trashed agents (`HasAgentsOnDomain` is
  unchanged): the FK requires it, and silently orphaning a restorable inbox
  would be worse. The error message tells the user to check the trash.

### Retention

`identity.TrashRetention` (exported var) = 30 days. A var, not a const, so a
deployment (or test) can tune it; no config knob until someone needs one.

## API surface (/v1)

- `AgentView`, `MessageView`, `MessageSummaryView` gain `deleted_at`
  (RFC3339Nano, omitted when live).
- `GET /v1/agents?deleted=true` — list trashed agents (same page envelope;
  the keyset cursor is bound to the view, so a live-list cursor replayed
  against the trash — or vice versa — is a 400 `invalid_cursor`).
- `DELETE /v1/agents/{email}?confirm=DELETE` — **now moves to trash**
  (breaking semantics change, documented; previously irreversible).
  `&permanent=true` — irreversible hard delete (any state).
- `POST /v1/agents/{email}/restore` — restore from trash → AgentView.
- `GET /v1/agents/{email}/messages?deleted=true` — the message trash
  (cursor carries the flag so a continuation can't flip views).
- `DELETE /v1/agents/{email}/messages/{id}` — move to trash (204).
  Held-for-review → 409 `message_held`. `?permanent=true&confirm=DELETE` —
  purge, only valid on a trashed message (otherwise 409 `not_in_trash`).
- `POST /v1/agents/{email}/messages/{id}/restore` — restore → MessageView.
- Message trash/restore (the REVERSIBLE ops) are per-agent operations
  (resolveOwnedAgent), so an agent-scoped credential can manage its own
  trash, like labels. The PERMANENT message purge is account-only — like
  every other irreversible delete on the surface, so a leaked or
  prompt-injected agent credential cannot destroy inbox evidence beyond
  recovery. Agent trash/restore/permanent-delete stay account-scoped
  (requireAccountScope).

No new webhook events in this slice (disposition events stay curated).

## Web UI

- Inbox messages view: per-message **Delete** (moves to trash); a **Trash**
  view filter; in trash: **Restore** and **Delete forever** (confirm).
- `/trash` page: deleted inboxes with restore / delete forever + retention
  copy; links to each live inbox's message trash.
- Inbox settings danger zone: delete copy now says trash + 30-day window.

## Out of scope (follow-ups)

- `email.message_deleted/restored` webhook events (event catalog is curated
  separately).
- CLI/MCP trash verbs and SDK ergonomic-layer helpers (generated SDK bases
  pick the endpoints up automatically).
- Per-account configurable retention.
