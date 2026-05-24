# Backend work for the Loft redesign

UI ships first via graceful degradation. These changes can land independently after PR 2 of the web migration. Each ticket lists: the UI feature it enables, the data model change, the API change, and an effort tier (S / M / L).

---

## Must-add (UI shows placeholders today)

### 1. `GET /api/dashboard/stats` — workspace-level aggregates

- **UI:** Dashboard stats strip — `Inbound today`, `Outbound today`, `Pending review`, `Delivery success %`. Today renders `—` in all four cards.
- **Reads from:** `usage_summaries` (existing) + `webhook_deliveries` (existing). No new tables.
- **Response shape:**
  ```json
  {
    "today": { "inbound": 184, "outbound": 139, "inbound_delta_pct": 12, "outbound_delta_pct": 4 },
    "pending": { "count": 2, "oldest_seconds": 2820 },
    "delivery_success_pct": 99.6,
    "sample_window_days": 7
  }
  ```
- **Prerequisite:** Force `E2A_USAGE_TRACKING=true` on the hosted deployment. Currently opt-in via env var; the no-op tracker is the default.
- **Effort:** M

### 2. Enriched `DashboardAgent`

- **UI:** Per-agent stats row on dashboard cards — `Inbound·7d`, `Outbound·7d`, `Pending`, `Last delivery`. Today renders `—`.
- **Model:** Add to `identity.AgentIdentity` and the `/api/dashboard/agents` response shape:
  ```go
  type AgentIdentity struct {
      // existing fields…
      Inbound7d        int        `json:"inbound_7d"`
      Outbound7d       int        `json:"outbound_7d"`
      PendingCount     int        `json:"pending_count"`
      LastDeliveryAt   *time.Time `json:"last_delivery_at,omitempty"`
      WebhookHealthy   bool       `json:"webhook_healthy"`
  }
  ```
- **Implementation choice:** denormalize (write on every inbound/outbound) vs. JOIN at read time. Recommend denormalize — cheap, prevents N+1 on dashboards with many agents.
- **Effort:** M

### 3. `APIKey.last_used_at` + `expires_at`

- **UI:** API keys table — `Last used` column (currently hidden), and an `Expires` column we want to add.
- **Model:** `api_keys.last_used_at` already exists and is updated on every API call — just needs to be returned by `ListAPIKeys` (currently dropped from the SELECT). Add `expires_at TIMESTAMPTZ NULL` column.
- **Endpoint:** `GET /api/keys` includes both. `POST /api/keys` accepts optional `expires_at` ISO timestamp.
- **Auth check:** `AuthenticateRequest` rejects expired keys with 401.
- **Effort:** S

### 4. Per-record DNS verification

- **UI:** Get-started + Domains pages show MX/SPF/DKIM with per-record found/missing status. Today shows only one global verified/pending state.
- **Endpoint:** `POST /api/v1/domains/{domain}/verify` returns:
  ```json
  { "mx": "found", "spf": "found", "dkim": "missing", "verified": false }
  ```
  instead of just a bool.
- **Effort:** M

### 5. Per-domain DKIM key generation [related to #4]

- **UI:** Get-started DNS table shows the actual DKIM public key TXT record the user should add. Today the table only lists MX + SPF; DKIM row is hidden.
- **Model:** Add to `domains`:
  ```sql
  ALTER TABLE domains
    ADD COLUMN dkim_selector TEXT,
    ADD COLUMN dkim_public_key TEXT,
    ADD COLUMN dkim_private_key BYTEA;
  ```
- **At domain registration:** generate a 2048-bit RSA keypair, store both halves.
- **Outbound mail:** update `outbound.Sender` to sign with the per-domain key instead of the single deployment-level key. **Major correctness improvement** — strict receivers (Gmail, Microsoft 365) may reject mail signed with e2a's deployment key for a user's custom domain.
- **Effort:** L

---

## Should-add (UI works without these but data is missing)

### 6. `reviewed_by_user_id` on HITL messages

- **UI:** Pending detail panel shows "approved by Jamie 14m ago". Today shows only the timestamp.
- **Model:** Add `reviewed_by_user_id TEXT NULL` to `messages`. Set in `ApproveAndSend` / `RejectPending`.
- **Endpoint:** `PendingMessageDetail` includes `reviewed_by_user_id` and (JOIN'd) `reviewed_by_name`.
- **Effort:** S

### 7. Domain enrichment

- **UI:** Domains list shows `is_primary` chip, agent count, last-checked timestamp.
- **Model:** Add to `domains`:
  ```sql
  ALTER TABLE domains
    ADD COLUMN is_primary BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN last_checked_at TIMESTAMPTZ;
  ```
  Enforce: at most one primary per user. `agent_count` computed at read time.
- **Endpoint:** `DomainInfo` includes `is_primary`, `agent_count`, `last_checked_at`.
- **Effort:** S

### 8. `PATCH /api/auth/me`

- **UI:** Settings → Profile → Edit name button. Today disabled with a tooltip.
- **Endpoint:** Accepts `{ "name": "Jamie" }`; updates `users.name`. Returns the updated `UserInfo`.
- **Validation:** 1–80 chars, no leading/trailing whitespace.
- **Effort:** S

---

## Deferred (UI degrades gracefully indefinitely)

### 9. Organizations / workspaces

- **UI:** Sidebar shows a workspace card today (user name + `usr_…` id). No real org switching.
- **Scope:** Major migration. New `orgs` and `org_members` tables, change every `WHERE user_id = $1` to `WHERE org_id = $1`, invite flow, RBAC.
- **Recommendation:** Defer until product genuinely needs multi-tenant. Treat the static workspace card as the canonical state until then.
- **Effort:** L+

### 10. Billing (Stripe)

- **UI:** Intentionally omitted from this redesign — Settings → Usage shows raw counters without plan caps.
- **Recommendation:** Add when ready. Until then, no UI work needed.
- **Effort:** L

### 11. API key scopes

- **UI:** Intentionally omitted — no Scopes column in the redesigned table.
- **If/when added:** `api_keys.scopes TEXT[]`, enforce in `AuthenticateRequest`. Add column to the UI.
- **Effort:** M

### 12. Notification preferences

- **UI:** Settings → Notifications shows three toggles as "Coming soon".
- **Model:** New `notification_prefs` table (`user_id`, `key`, `enabled`). Notification dispatch worker.
- **Effort:** M

### 13. Search (⌘K)

- **UI:** Topbar shows ⌘K hint but the search box is non-functional (or hidden behind `NEXT_PUBLIC_SEARCH_ENABLED`).
- **Scope:** Full-text over agents + messages + domains. Postgres `tsvector` or external (Meilisearch / Typesense).
- **Recommendation:** Either build it or remove the topbar search affordance entirely.
- **Effort:** M

---

## Already supported but unsurfaced

Consider exposing in a future PR:

- **OAuth 2.1 authorization server** — `/api/oauth/*` endpoints exist. Add a "Connected apps" section to Settings.
- **Signing secrets** — `/api/v1/users/me/signing-secrets` is fully wired. Surfaced as the standalone **Webhook secrets** page (top-level nav next to API keys); the original redesign mock placed it inside Settings but it was promoted to its own route for parity with /api-keys.
- **User data export** — `GET /api/v1/users/me/export` exists. The redesign's Settings → Danger zone Export button maps to it.
- **Account deletion** — `DELETE /api/v1/users/me` exists. The Delete account button maps to it.

---

## Ordering recommendation

If you can ship backend PRs alongside the UI:

- **Backend PR A** (lands with web PR 2): items 1, 3, 6, 7, 8. Small additions that make the new UI feel complete.
- **Backend PR B** (follow-up): items 2, 4, 5. Per-agent stats, per-record DNS, per-domain DKIM.
- Items 9–13: defer or design separately.
