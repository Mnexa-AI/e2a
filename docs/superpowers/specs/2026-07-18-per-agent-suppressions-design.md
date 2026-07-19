# E2A-Managed Per-Agent Unsubscribe Design

**Status:** Approved
**Date:** 2026-07-18

## Problem statement

An e2a account can contain many agents, with each agent email address
representing an independent sending identity. The current suppression list is
keyed by `(user_id, recipient_address)`, so an unsubscribe from one agent
prevents every agent in the account from emailing that recipient.

e2a must support a suppression scoped to the exact sending agent address. If
`recipient@example.com` unsubscribes from
`sender@tenant-a.mail.example.com`, that agent must be unable to send the
recipient any outbound email, while another agent in the same account remains
allowed unless an account-wide suppression also applies.

An e2a account owner should not need to build a parallel consent service. For
an eligible single-recipient message, the application opts into unsubscribe
support in the send request. e2a then owns the unsubscribe token, RFC 8058
headers, visible body link, public confirmation endpoint, scoped suppression,
future-send enforcement, webhook notification, and authenticated management
API/SDK.

## Goals and non-goals

Goals:

- Add agent-scoped recipient suppressions without weakening existing
  account-wide bounce and complaint protection.
- Preserve the existing `/v1/account/suppressions` contract and behavior.
- Provide account-admin CRUD for suppressions nested under an agent.
- Provide an opt-in, e2a-managed unsubscribe experience on every outbound API
  shape: new send, reply, and forward.
- Add RFC 8058 one-click headers and a visible body link before DKIM signing,
  with a scanner-safe confirmation flow.
- Notify the account when a recipient creates an agent suppression.
- Enforce the union of account and sending-agent suppressions on every outbound
  path, including the final queued-worker check.
- Make writes idempotent, tenant-isolated, normalized, and pagination-safe.
- Preserve agent suppressions through trash, restore, permanent deletion, and
  later recreation of the same address.
- Keep generated SDKs and account export consistent with the API and storage
  model.

Non-goals:

- Automatically classifying a message as marketing or transactional. The
  application explicitly opts each message into unsubscribe support.
- Topic-level preferences within one agent, such as product updates versus job
  notifications. The initial consent boundary is the full agent address.
- Partial delivery of a multi-recipient request. A single suppressed recipient
  continues to reject the whole logical send.
- Mapping e2a agents to Amazon SES tenant resources.
- Changing SES provider-side suppression behavior.

## Relevant context and constraints

- Existing suppressions are account-wide and are automatically added after a
  hard bounce or complaint.
- `domain.suppression_added` is a stable, frozen, account-scoped event. Agent
  suppressions must not change its payload or routing semantics.
- An agent identity's ID is its normalized full email address and is immutable.
- Agents support soft deletion, restoration, permanent deletion, and address
  recreation. Recipient consent must not be erased by these lifecycle actions.
- Suppression enforcement currently exists at direct acceptance, human and
  magic-link approval, TTL auto-approval, test-send acceptance, and immediately
  before provider I/O.
- The async worker currently reloads the owning user but must also carry the
  sending agent address into the effective-suppression lookup.
- Account and agent-scoped API keys exist. Suppression administration remains
  account-scoped; an agent credential cannot remove its own recipient blocks.
- RFC 8058 one-click unsubscribe uses an unauthenticated POST carrying a bearer
  token. Link scanners may issue GET requests, so GET must never mutate consent.
- A MIME message has one shared `List-Unsubscribe` URI. The initial managed
  experience therefore requires exactly one normalized envelope recipient.

## Proposed design

### 1. Add an agent suppression storage model

Migration `068_per_agent_suppressions.sql` creates separate
`agent_suppressions` and `agent_unsubscribe_tokens` tables:

```sql
CREATE TABLE agent_suppressions (
    id                TEXT PRIMARY KEY,
    user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id          TEXT NOT NULL,
    address           TEXT NOT NULL,
    reason            TEXT NOT NULL DEFAULT '',
    source            TEXT NOT NULL CHECK (source IN ('unsubscribe','manual')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, agent_id, address)
);

CREATE TABLE agent_unsubscribe_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    address    TEXT NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_unsubscribe_tokens_scope_idx
    ON agent_unsubscribe_tokens (user_id, agent_id, address);

CREATE INDEX agent_suppressions_list_idx
    ON agent_suppressions (user_id, agent_id, created_at DESC, address DESC);
```

The existing `suppressions` table and its `(user_id,address)` uniqueness remain
unchanged. This avoids a rolling-deploy incompatibility: old application
instances continue using `ON CONFLICT (user_id,address)` successfully after the
migration, while upgraded instances can use the new table.

Do not add a foreign key from `agent_suppressions.agent_id` to
`agent_identities`. The API and store validate ownership when creating a row,
while retaining the address string lets an unsubscribe survive permanent agent
deletion and apply again if the account recreates the same address. `user_id`
uses `ON DELETE CASCADE`, so account deletion removes all agent suppressions.

The public bearer token is an opaque 256-bit HMAC of a versioned, unambiguous
encoding of `(account, agent, recipient)`, using the active deployment signing
secret. The table stores only a SHA-256 hash of that token mapped to its scope.
The same active secret deterministically produces the same token, so storage
grows with relationships rather than messages. A signing-secret rotation may
add one new mapping per active relationship; old mappings remain valid without
retaining the old secret. Tokens do not expire: an unsubscribe link in an old
delivered email must continue to honor the recipient's choice. Removing a
suppression does not invalidate the token; clicking the old email can opt out
again. Token mapping insertion is idempotent and race-safe under `token_hash`
uniqueness. The plaintext token is never persisted or logged.

The storage DTO gains optional `agent_email`. Account list methods filter
only the existing table. New agent methods use only `agent_suppressions` and
always filter both `user_id` and `agent_id`, preventing cross-account reads or
mutations even though agent IDs are globally address-like identifiers.

### 2. Add an opt-in send contract

Every outbound request shape (new send, reply, and forward) gains the additive
object:

```json
{
  "unsubscribe": {
    "mode": "managed"
  }
}
```

Omitting `unsubscribe` preserves current bytes and behavior; omission means
only that e2a-managed unsubscribe was not requested, not that e2a has classified
the message as transactional. When the object is present, `mode` is required
and `managed` is the only accepted initial value. Empty objects, `null`, unknown
modes, and unsupported fields use the API's native schema-validation response,
`422 invalid_request`. This is a
per-message choice because e2a cannot infer whether the application is sending
subscribed or transactional content. An agent-wide default is deferred until
e2a has an explicit content/purpose model.

When `unsubscribe.mode` is `managed`, e2a normalizes and deduplicates To/CC/BCC
before validation and requires exactly one envelope recipient. Otherwise it
returns `400 invalid_request` with a stable explanatory message; e2a does not
generate one shared token for multiple recipients or split one logical send
into hidden fanout. The option is supported with literal content, templates,
attachments, HITL, replies, and forwards.

After template rendering and after the final normalized recipient set is known,
e2a derives the tuple's opaque token, idempotently persists its hash mapping,
and builds an absolute HTTPS URL from configured `HTTP.APIURL`:

```text
https://api.e2a.dev/u/{token}
```

If the public API URL is unavailable or non-HTTPS in production, startup fails
instead of silently disabling managed unsubscribe. If token storage fails, the
opted-in send is rejected rather than delivering a broken consent experience.
The URL is appended as a standard visible footer—“Unsubscribe from
emails sent by {agent address}”—to both the plain-text and HTML alternatives.
e2a then injects:

```text
List-Unsubscribe: <https://api.e2a.dev/u/{token}>
List-Unsubscribe-Post: List-Unsubscribe=One-Click
```

The footer and headers are added before DKIM signing. The DKIM header whitelist
must include both list headers, as required by RFC 8058. The internal SES
configuration-set header remains post-DKIM.

For direct sends, replies, and forwards, final-recipient binding happens before
durable acceptance and MIME composition. For HITL, the request persists the
unsubscribe intent but does not mint a token until approval has applied all
recipient overrides. An override that produces zero or multiple recipients, or
a token-store failure, leaves the message unsent under the existing approval
failure posture. The final composed raw message is persisted and reused by the
async worker, so retries do not change its URL or signed bytes.

### 3. Host the public unsubscribe flow

Register a narrow public route outside authenticated `/v1` management APIs:

```http
GET  /u/{token}
POST /u/{token}
```

`GET` performs a constant-time hash lookup and renders a minimal confirmation
page identifying the sender address; it never changes state, which prevents
mail security scanners from unsubscribing recipients. The form posts back to
the same URL. `POST` accepts both the RFC 8058 field
`List-Unsubscribe=One-Click` and the browser confirmation form as bounded
`application/x-www-form-urlencoded` or `multipart/form-data` input. The entire
request body is capped at 1 KiB. The route requires no login, cookies,
JavaScript, redirects, or CSRF token, and returns `200` for a valid token even
when the suppression already exists. Invalid tokens return a generic `404`
without revealing tuple data. Responses use `Cache-Control: no-store` and a
restrictive content-security policy.

A valid POST idempotently inserts an `agent_suppressions` row with
`source=unsubscribe`, blank reason, and no required message foreign key. It
never writes the account-wide suppression table. The token is the authority to
add only that exact account-agent-recipient tuple; it cannot list or remove any
suppression.

### 4. Add nested agent suppression operations

Add account-admin-only operations:

```http
GET    /v1/agents/{email}/suppressions
POST   /v1/agents/{email}/suppressions
DELETE /v1/agents/{email}/suppressions/{address}?confirm=DELETE
```

The create body is:

```json
{
  "address": "recipient@example.com",
  "reason": "recipient opted out"
}
```

Authenticated management creation always records `source=manual`; only the
public recipient flow records `source=unsubscribe`, keeping provenance
trustworthy. Bounce and complaint suppressions remain feedback-owned and
account-wide. Creation normalizes both addresses and is idempotent: creating an
existing `(account, agent, recipient)` suppression returns `200` with the
existing resource rather than duplicating it. A new row also returns `200`, so
retries do not need to branch on whether another request won the insert race.

The nested list uses the standard `{items,next_cursor}` envelope and existing
suppression cursor ordering. Its signed cursor includes the agent address in
addition to the keyset position, and the handler rejects a cursor minted for a
different agent. The response includes `agent_email` so an item remains
self-describing in logs, exports, and generic clients. Deletion removes only the
exact agent-scoped row; it never clears an overlapping account-wide suppression.

The existing account list and delete endpoints continue to operate only on the
existing `suppressions` table. This is required for backward compatibility:
deleting `/v1/account/suppressions/{address}` cannot silently remove
agent-specific consent records.

### 5. Compute effective suppressions for one sender

Replace account-only enforcement lookups with an effective lookup accepting
`user_id`, `agent_id`, and all To/CC/BCC recipients:

```sql
SELECT address FROM suppressions
 WHERE user_id = $1 AND address = ANY($2)
UNION
SELECT address FROM agent_suppressions
 WHERE user_id = $1 AND agent_id = $3 AND address = ANY($2)
```

An account row and an agent row both block. Agent scope can never override or
bypass an account suppression. The result is deduplicated by recipient before
building the existing `422 recipient_suppressed` error.

All outbound paths pass the exact sending agent:

1. Direct send, reply, and forward acceptance.
2. Human review approval after applying recipient overrides.
3. Magic-link approval.
4. TTL auto-approval.
5. Platform test send.
6. Async worker immediately before provider I/O.

The final worker's `SendJob` gains `AgentID`, loaded from the message row. A
suppression created after acceptance but before submission therefore still
prevents delivery. Store failures retain the existing posture: acceptance may
fail open because the final worker rechecks; approval and the worker fail
closed/retry without provider I/O.

An agent suppression blocks every outbound message type from that agent,
including replies. Other agents remain allowed. Introducing purpose-specific
or reply exemptions without an explicit message classification would create an
unsafe, surprising bypass and is deferred.

### 6. Feedback, events, export, and lifecycle

Hard-bounce and complaint feedback continues to call the account-wide insertion
path and emit the existing `domain.suppression_added` event unchanged. Manual
agent suppression creation does not emit that event. A dedicated agent
suppression event replaces neither that event nor its payload.

The first successful insert of an agent suppression emits the beta webhook
event `agent.suppression_added`. Its data contains `agent_email`, `address`,
and `source` (`unsubscribe | manual`). An idempotent repeat POST does not emit
a duplicate event. This lets the account owner immediately update application
state without making the application the enforcement authority.
Webhook delivery failure never rolls back the suppression.

Account export unions agent rows into the existing suppressions collection and
includes optional `agent_email`, so data-rights handling preserves scope without
changing the stable export envelope. This interior-shape change advances the
export `schema_version` to `3`; v3 consumers distinguish account-wide rows
(no `agent_email`) from exact-agent rows (`agent_email` present). Account deletion removes both tables
through their user cascades. Agent trash, restore, and permanent deletion do not
mutate either table.

Amazon SES may independently suppress a provider submission. The e2a contract
guarantees its own pre-provider policy decision, not that SES will attempt a
recipient that SES itself refuses. Agent unsubscribes are not added to SES, so
they remain isolated within e2a.

## Edge cases and failure handling

- The same recipient may have account and agent rows; either blocks, deleting
  one does not delete the other, and errors list the recipient once.
- Agent A's suppression never affects agent B under the same account.
- A suppression under another account never affects the caller, even for the
  same agent-like or recipient address.
- A trashed agent cannot be addressed by the live nested API, but its rows are
  retained and become effective again on restore.
- Permanent deletion retains rows; recreating the exact address restores their
  effect. A different agent address does not inherit them.
- Recipient matching follows e2a's existing normalized exact-address behavior.
  Provider-specific alias folding (Gmail dots, plus tags) is not introduced.
- One suppressed address in To/CC/BCC rejects the entire operation, preserving
  current atomic send semantics and avoiding partial conversation state.
- Concurrent duplicate creates collapse through the new table's unique
  constraint.
- Concurrent deletion and send are serialized only by normal transaction
  visibility; the final worker recheck closes the acceptance-to-provider race.
- Invalid or non-owned agents return the same non-enumerating `404 not_found`
  posture as other nested agent operations.
- An opted-in multi-recipient message is rejected before durable acceptance;
  callers send one message per recipient with their existing idempotency-key
  strategy.
- GET requests from scanners never suppress. Valid repeated POSTs are harmless;
  concurrent POSTs create one row and one logical event.
- A removed suppression can be recreated by the original bearer URL. This is
  intentional: an old delivered unsubscribe link remains authoritative.
- Token hashes are never returned by management APIs or exports. Account
  deletion removes them; agent deletion retains them just like consent rows.

## Scalability and extensibility

The effective lookup remains a bounded indexed union over the request's capped
recipient set. The existing `(user_id,address)` uniqueness supports account
matches; the new `(user_id,agent_id,address)` uniqueness supports agent matches.
The `(user_id,agent_id,created_at DESC,address DESC)` index matches the keyset
list order, including its deterministic address tie-breaker.

Separate tables deliberately keep deliverability feedback, agent consent, and
public bearer capabilities as different storage concepts while presenting the
first two as effective suppressions at send time. Token reuse keeps the public
flow O(unique agent-recipient relationships), and the send-path lookup is an
indexed tuple read. A future domain or tenant scope requires a deliberate
schema and API design rather than prematurely introducing a generic scope
framework. The public nesting establishes the agent as the current tenant
boundary without claiming that all future tenancy models are
one-agent-per-tenant.

## Verification strategy

Use test-first slices with explicit failing tests before implementation:

1. Migration/store tests: existing account-write compatibility after migration,
   agent uniqueness, account and agent isolation, overlapping scopes,
   normalization, pagination, deletion, and persistence across agent hard
   deletion/recreation; token uniqueness, hash-only persistence, reuse, and
   concurrent creation.
2. Public-flow tests: GET never mutates, valid browser and RFC 8058 POSTs,
   idempotence, invalid-token non-enumeration, no-store/security headers, and
   suppression/event creation for the exact tuple only.
3. HTTP tests: create/list/delete success, idempotent create, account-only auth,
   foreign/missing agent non-enumeration, validation, confirmation guard, and
   proof that account endpoints cannot mutate agent rows.
4. Compose/send tests: omitted opt-in is byte-compatible; opted-in literal,
   template, attachment, reply, forward, and HITL messages contain both body
   links and both list headers; DKIM covers the list headers; multi-recipient
   opted-in requests fail before acceptance; configuration/storage failures do
   not deliver broken links.
5. Direct-send tests: same-agent refusal, sibling-agent allowance, account-wide
   refusal for both, To/CC/BCC coverage, and duplicate effective-row collapse.
6. HITL tests: human, magic-link, and TTL auto-approval apply the sending agent
   after final recipient edits and preserve their existing failure semantics.
7. Async-worker tests: an agent suppression added after acceptance prevents
   provider I/O for that agent but not a sibling agent.
8. Feedback/event tests: bounces and complaints remain account-wide,
   `domain.suppression_added` remains byte-for-byte contract compatible, and
   `agent.suppression_added` emits once with the documented scope.
9. Contract tests: OpenAPI generation, compatibility checks, generated
   TypeScript/Python SDK freshness, SDK unit tests, and documentation examples.
10. Regression: affected Go packages, race-sensitive worker tests, full Go test
   suite, repository build, and existing conformance checks.

## Rollout and compatibility

The migration only creates new tables and indexes; existing rows, constraints,
queries, REST operations, and SDK operations retain account behavior. New nested
operations and optional response/export fields are additive under the
repository's compatibility rules.

Deploy the schema before application instances that write agent suppressions.
Mixed-version application instances continue to read and enforce account rows;
only upgraded instances understand agent rows. Rollout should therefore be
completed promptly, and agent-suppression API traffic should begin only after
all send workers are upgraded so no old worker can bypass a newly created agent
suppression.

## Developer journey

The complete integration path is:

1. Create an agent address for each independent sending identity.
2. Send subscribed content to one recipient with
   `unsubscribe: {"mode":"managed"}`; no URL, token, footer, or header
   plumbing is required in the application.
3. The mailbox provider performs one-click POST, or the recipient follows the
   visible link and confirms. e2a creates the exact agent-recipient suppression.
4. The account receives `agent.suppression_added` and may mirror the preference
   for product UX, while e2a remains the delivery enforcement point.
5. Future sends from that agent return the existing
   `422 recipient_suppressed`; another agent remains allowed.
6. The application can list, add, or remove agent suppressions through
   generated SDK operations. Account-wide bounce/complaint safety remains
   unchanged.

## Open questions

None for the initial slice. Agent suppressions block all outbound message types
from the exact agent address. Content categories, agent-wide unsubscribe
defaults, multi-recipient personalized fanout, and SES tenant integration are
explicit follow-ups.
