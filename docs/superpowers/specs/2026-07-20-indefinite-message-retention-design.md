# Indefinite Message Retention

## Summary

E2a will retain live inbound and outbound message data indefinitely. A message
will no longer have a natural time-to-live. Moving a message or agent inbox to
trash starts the existing, deployment-configurable trash-retention window,
which remains 30 days by default. A trash purge, explicit permanent deletion,
permanent agent deletion, or account deletion remains destructive.

The `messages.expires_at` column and the corresponding public API field will
become nullable. `NULL` means that a live message never expires. This expresses
the policy directly and avoids a far-future sentinel date.

## Scope

This change applies to all message-owned data:

- inbound and outbound envelopes and metadata;
- inbound raw RFC 822 content;
- outbound raw MIME and held-draft text, HTML, and attachment data;
- attachments stored in `messages.attachments_json`;
- authentication evidence, labels, delivery state, and review state stored on
  the message row.

This change does not alter retention for webhook events or deliveries, OAuth
sessions and grants, API keys, usage records, inbound intake staging rows,
logs, backups, suppressions, unsubscribe mappings, or other non-message data.
Their existing policies remain independent.

## Data model and migration

A forward-only, idempotent migration will:

1. remove the `NOT NULL` constraint from `messages.expires_at`;
2. drop its timestamp default so omitted values become `NULL`; and
3. set `expires_at = NULL` for every existing message that is still present,
   including messages currently in trash.

Clearing `expires_at` on trashed rows is safe because trash purge is governed
solely by `deleted_at` and the configured trash-retention duration. Existing
messages that were already purged or outbound content that was already
scrubbed cannot be recovered by the migration.

The migration will remove the existing message-expiry index because no message
query or cleanup path will use it after this change. The migration must not
rewrite any large message payload columns; it updates only the timestamp
column. Migration and runtime SQL will be covered by database-backed tests.

## Runtime behavior

### Message creation and reads

Every inbound and outbound creation path will store `expires_at = NULL`.
`identity.Message.ExpiresAt` and related internal/export models will represent
the field as an optional timestamp.

Live-message reads, lists, conversations, reply and forward targets, label
updates, review reads, and delivery-status reads will no longer require
`expires_at > now()`. They will continue to enforce ownership, agent state,
trash visibility, and held-message rules exactly as they do today.

### Outbound content

Terminal outbound transitions will stop clearing `body_text`, `body_html`, and
`attachments_json`. This includes human approval, human rejection, TTL
approval, TTL rejection, loopback delivery, and worker-driven terminal paths.
Where a sent message also contains canonical raw MIME, retaining the draft
columns may duplicate content, but it preserves the exact accepted outbound
data and keeps behavior consistent across sent, rejected, and failed messages.
Storage metering will continue to count every retained content column using
the existing transactional trigger.

### Trash, restore, and deletion

Soft deletion continues to set `deleted_at`. The janitor will delete message
rows only when they, or their owning agent, have remained in trash longer than
the configured trash retention. The default remains 30 days through
`trash.retention_days` / `E2A_TRASH_RETENTION_DAYS`.

Restore will clear the relevant `deleted_at` value but will no longer shift
`expires_at`; there is no live-message expiry clock to pause or resume. A held
message's `approval_expires_at` remains a separate review deadline and retains
its existing pause/resume behavior while its message or agent is in trash.

Explicit permanent deletion and account deletion remain unchanged and remove
message data immediately according to their existing authorization and
confirmation rules.

## API and client contract

The public message and export schemas will keep the `expires_at` property but
make its value nullable. A live or trashed message retained under the new
policy returns `expires_at: null`. Keeping the property present avoids
conflating omission with an unknown value.

The Huma handler types, committed OpenAPI document, generated TypeScript and
Python SDK bases, handwritten SDK layers, CLI, MCP server, and web dashboard
must accept and preserve the nullable value. Documentation will define `null`
as “does not expire.” The OpenAPI compatibility gate may classify the type
change as breaking; the change is intentional while `/v1` remains a release
candidate and must be reflected across all client surfaces in the same change.

## Janitor and failure behavior

The message janitor remains responsible for trash purges but loses its natural
expiry arm. Cleanup remains batched and idempotent. A janitor failure continues
to be logged and retried on its normal schedule; it must not affect live
message availability.

No fallback expiry is introduced. If `expires_at` is unexpectedly non-null on
a legacy or manually modified row, runtime reads still treat the message as
live and the janitor does not delete it through natural expiry. This makes the
new indefinite-retention policy authoritative rather than dependent on a
perfect migration.

## Testing

Implementation will proceed test-first and cover:

- every inbound and outbound creation path stores `expires_at = NULL`;
- messages with null or legacy past timestamps remain readable and are not
  naturally purged;
- the migration preserves existing timestamps without a full-table rewrite,
  and runtime behavior ignores those legacy values;
- outbound content survives approve, reject, TTL resolution, loopback, send,
  and failure transitions;
- live messages survive janitor runs;
- messages and agents in trash are purged after the configured window;
- restore keeps indefinite message retention while correctly shifting an
  unresolved hold's `approval_expires_at`;
- account export and API serialization return `expires_at: null`;
- OpenAPI generation and TypeScript/Python generated clients remain in sync;
- storage metering includes all retained outbound content; and
- documentation and repository text-integrity checks contain no stale claim
  that messages expire after 10 or 30 days or that terminal bodies are
  scrubbed.

## Operational consequences

Message storage will grow without a time-based bound. Operators must provision
and monitor Postgres storage and backup growth. Account quotas, explicit user
deletion, trash purge, and operator-managed backup retention remain the
available bounds. This policy intentionally favors complete message history
over automatic storage reclamation.
