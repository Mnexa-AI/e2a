# Attachment retrieval — metadata + signed download URL (§6a #5)

Status: **shipped** (native adapter). AgentDrive/object-storage adapter, outbound
presigned upload, and large-file attach-by-reference are **deferred seams**.

## Problem

`get_attachment` used to re-fetch the whole message, parse the MIME client-side,
and return the attachment **base64 in the tool response** — so binary bytes flowed
through the agent's context (a 1 MB PDF ≈ 350k+ tokens), and anything over a 2 MB
wall was unretrievable. Industry-standard for agent-facing email retrieval is
metadata in the payload + bytes by reference (a short-lived signed download URL).

## What shipped

A pluggable port with the **native** adapter wired by default (zero external
dependency); the bytes never traverse the model context and there is no size wall.

### The port — `internal/httpapi/attachments.go`
```go
type AttachmentStore interface {
    DownloadURL(agentEmail, messageID string, index int, ttl time.Duration) (url string, expiresAt time.Time, err error)
    VerifyDownload(token, messageID string, index int) bool
}
```
- **native adapter** (`nativeAttachmentStore`, default): mints an **HMAC-SHA256
  capability token** bound to `message_id|index|expiry` over the deployment signing
  secret (`cfg.Signing.HMACSecret`) and points the URL at e2a's own streaming route.
  Self-contained signer — deliberately **not** the HITL `approvaltoken` (whose action
  allowlist + payload are approve/reject-specific); same crypto family.
- Selected in `apiserver` from `SigningSecret`+`PublicURL`; nil → endpoints unwired.

### Authoritative attachment index — `internal/mailparse/attachments.go`
`Attachments(raw)` / `AttachmentAt(raw, i)` walk the MIME tree (depth-first, stable
order) and decode CTE → bytes. An attachment = a leaf with a filename or explicit
`Content-Disposition: attachment`; body text is excluded, named inline parts (cid
images) included. The **backend** owns this index (it backs both the read view and
the download route) — the MCP's prior TS parse could not drive a server route
consistently. `MessageView.attachments[]` (`{index, filename, content_type, size_bytes}`)
is parsed from `raw_message` server-side.

### Endpoints
1. `GET /v1/agents/{email}/messages/{id}/attachments/{index}` (Huma, **bearer**) →
   `{index, filename, content_type, size_bytes, download_url, expires_at}`.
   `?inline=true` adds base64 `data` for ≤256 KB (else 413 `attachment_too_large`).
   Auth via `resolveOwnedAgent` (agent-scope may read its own message).
2. `GET …/attachments/{index}/download?token=` (raw chi, **capability-token**, no
   bearer) → streams bytes with `Content-Type`, `Content-Disposition`,
   `Content-Length`, `X-Content-Type-Options: nosniff`. The token binds
   message+index; the path `{email}` binds the message to its owning agent
   (`GetMessage` is agent-keyed), so a valid token streams only what it minted.

### MCP / SDK
SDK `messages.getAttachment(email, id, index, {inline})` → `AttachmentView`. MCP
`get_attachment` returns the URL by default, `inline:true` adds base64 for small
files; the 2 MB wall + client-side re-parse are removed. `get_message` now reads
the server `attachments[]`.

## Constants / invariants
- Download token TTL **15 min**; inline cap **256 KB**.
- Fail closed: bad/expired/tampered/index-mismatched token → 403; index OOB → 404;
  draft (no `raw_message`) → 404.
- URL leakage bounded by the short TTL (same posture as the WS `?token=`).

## Edge cases handled
Index out of range (404), inline over cap (413, with directive), held drafts (404),
cross-agent read (403 via `resolveOwnedAgent`), token replay across messages/indices
(403), missing/empty filename (`Content-Disposition: attachment`, CRLF-stripped),
empty/unknown content-type (`application/octet-stream`).

## Deferred (designed seams, not built)
- **Object-storage / AgentDrive adapter** — `AttachmentStore` implementation
  returning a presigned URL. Gates on cross-product identity (shared IdP?) — out of
  scope for the native ship.
- **Outbound presigned upload** (symmetric `AttachmentUploader`).
- **Large-file forward via attach-by-reference** (`{source_message_id, index}` on
  send) — until then, small-file forward works via `inline:true`; large-file forward
  was already impossible under the old 2 MB wall, so this is strictly additive.

## Verification
- Unit: `mailparse` extractor (order, base64/QP decode, binary integrity, bounds,
  malformed). httpapi: metadata + signed URL, inline small / inline-too-large (413)
  / URL-still-works, index OOB (404), agent-scope cross-agent (403), download happy
  path streams bytes, bad/index-mismatched token (403) — all over a real httptest
  server. SDK + MCP: shape + forwarding + error surfacing.
- Local-service e2e (real binary + Postgres): seeded an inbound message with a real
  MIME attachment; over the wire confirmed `get_message` attachments[], metadata +
  download_url, the capability-token download stream (exact bytes + headers), inline
  base64, and the 404/403 negatives. Server logs clean.
