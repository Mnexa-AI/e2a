// Webhook signature verification for the /v1 webhooks resource (Slice 8b).
//
// e2a signs each subscriber delivery with HMAC-SHA256 keyed by the webhook's
// per-webhook signing_secret (whsec_...), Stripe-style:
//
//   X-E2A-Signature: t=<unix>,v1=<hex>[,v1=<hex>]
//
// During the 24h rotation grace each delivery carries two v1= pairs (one per
// active secret); accept if ANY matches. The timestamp guards replay — reject
// anything older than the tolerance (default 5 min). The signed payload is
// `${t}.${rawBody}` — pass the RAW request body bytes; re-stringifying the
// parsed JSON will not match (whitespace/key-order differ).

import { createHmac, timingSafeEqual } from "crypto";
import { E2AWebhookSignatureError } from "./errors.js";

export interface VerifySignatureOptions {
  /** Raw HTTP request body bytes. */
  rawBody: string | Buffer;
  /** Value of the X-E2A-Signature header. */
  header: string;
  /** Webhook signing secret (whsec_...). Pass an array to verify one handler
   *  against several endpoints' secrets. */
  secret: string | string[];
  /** Tolerance in seconds; defaults to 300. */
  toleranceSeconds?: number;
  /** Test-only clock override; defaults to Date.now(). */
  now?: () => number;
}

/**
 * Verify an X-E2A-Signature header. Returns true on success, false on any
 * failure (bad format, missing pair, signature mismatch, replay). Never throws.
 */
export function verifyWebhookSignature(opts: VerifySignatureOptions): boolean {
  // Guard a missing/non-string header (e.g. req.headers[...] is undefined) so a
  // missing X-E2A-Signature is a clean `false`, never a raw TypeError (WH-SIG-1).
  if (!opts.header || typeof opts.header !== "string") return false;
  const tolerance = opts.toleranceSeconds ?? 300;
  const nowMs = opts.now ? opts.now() : Date.now();

  let t = "";
  const v1s: string[] = [];
  for (const part of opts.header.split(",")) {
    const trimmed = part.trim();
    if (trimmed.startsWith("t=")) t = trimmed.slice(2);
    else if (trimmed.startsWith("v1=")) v1s.push(trimmed.slice(3));
  }
  if (!t || v1s.length === 0) return false;

  const ts = Number(t);
  if (!Number.isFinite(ts)) return false;
  if (Math.abs(nowMs / 1000 - ts) > tolerance) return false;

  const body = typeof opts.rawBody === "string" ? opts.rawBody : opts.rawBody.toString("utf8");
  const secrets = Array.isArray(opts.secret) ? opts.secret : [opts.secret];

  for (const secret of secrets) {
    const expectedHex = createHmac("sha256", secret).update(`${t}.${body}`).digest("hex");
    const expectedBytes = Buffer.from(expectedHex, "hex");
    for (const candidate of v1s) {
      if (candidate.length !== expectedHex.length) continue;
      const candidateBytes = Buffer.from(candidate, "hex");
      if (candidateBytes.length !== expectedBytes.length) continue;
      if (timingSafeEqual(candidateBytes, expectedBytes)) return true;
    }
  }
  return false;
}

/** A verified event envelope — the shape of a webhook delivery body, a
 *  `GET /v1/events/{id}` object, AND a WebSocket frame (all three channels
 *  share it). `data` is `unknown` at the envelope level so unknown/beta event
 *  types still parse; narrow on `type` with the `isEmail*` / `isDomain*`
 *  guards below to get the typed payload of a stable event. */
export interface WebhookEvent {
  id: string;
  type: string;
  /** Envelope schema version (currently "1"). Branch on it before parsing
   *  `data` if you need forward-compatibility across envelope revisions. */
  schema_version: string;
  created_at: string;
  data: unknown;
  [k: string]: unknown;
}

// ── Typed per-event `data` payloads (STABLE events) ─────────────────────────
//
// Mirrors the server's canonical structs (internal/eventpayload) and the
// OpenAPI component schemas (EmailReceivedData, …). Locked by the shared
// golden fixtures under internal/eventpayload/testdata — the server builders
// and these types are tested against the same files.
//
// The beta events (email.flagged, email.blocked, email.review_requested,
// email.review_approved, email.review_rejected) are intentionally NOT typed:
// their payloads are open/unstable — access `event.data` generically.

/** Metadata for one attachment (never the bytes). `index` is the stable
 *  0-based fetch key for `GET …/messages/{id}/attachments/{index}`. */
export interface AttachmentMeta {
  filename?: string;
  content_type?: string;
  size_bytes: number;
  index: number;
}

/** Typed payload of an `email.received` event. The event is a metadata-only
 *  notification — it does NOT carry the message body. `message_id` + `delivered_to`
 *  are the fetch keys; pass the event to {@link E2AClient.webhooks.fetchMessage}
 *  (or call `client.messages.get(delivered_to, message_id)`) to retrieve the full
 *  message (body + attachment bytes). `auth_headers` is the signed X-E2A-Auth-*
 *  attestation — verify it to independently confirm the inbound SPF/DKIM/DMARC
 *  verdict. */
export interface EmailReceivedData {
  message_id: string;
  /** The receiving agent's email — its id and address (an agent's id IS its email). */
  agent_email: string;
  /** Always "inbound" on this event. */
  direction: string;
  conversation_id?: string;
  /** Display/reply sender (prefers Reply-To). For the authenticated, gated
   *  identity use `authenticated_from`. */
  from: string;
  /** The From-header identity SPF/DKIM/DMARC verified — treat THIS (not
   *  `from`) as the gated identity. */
  authenticated_from: string;
  to: string[];
  cc?: string[];
  reply_to?: string[];
  /** The one agent address this per-agent copy was delivered to (scalar by
   *  construction — one event per delivery). The fetch key. */
  delivered_to: string;
  subject: string;
  /** Signed X-E2A-Auth-* attestation of the inbound auth verdict. May be an
   *  empty object on the WebSocket drain path; never absent. */
  auth_headers: Record<string, string>;
  received_at: string;
  /** Attachment METADATA (never bytes). Omitted when the message has none. */
  attachments?: AttachmentMeta[];
}

/** Typed payload of an `email.sent` event — an outbound send reached its sent
 *  state through provider acceptance or atomic local loopback delivery. */
export interface EmailSentData {
  message_id: string;
  agent_email: string;
  /** Always "outbound" on this event. */
  direction: string;
  conversation_id?: string;
  /** Provider-assigned (SES) id — the correlation key for the async
   *  delivered/bounced/complained feedback events. Absent for providerless
   *  local loopback delivery. */
  provider_message_id?: string;
  /** Open set; tolerate unknown values. Known values: smtp, loopback. */
  method: string;
  from: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  /** Open set; tolerate unknown values. Known values: send, reply, forward. */
  message_type: string;
}

/** Typed payload of an `email.failed` event — an outbound send terminally
 *  failed (retries exhausted / permanent reject). */
export interface EmailFailedData {
  message_id: string;
  agent_email: string;
  /** Always "outbound" on this event. */
  direction: string;
  conversation_id?: string;
  /** Open set; tolerate unknown values. Known values: smtp. */
  method: string;
  from: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  /** Open set; tolerate unknown values. Known values: send, reply, forward. */
  message_type: string;
  /** Human-readable terminal failure diagnostic. */
  reason: string;
  /** Optional machine-readable failure code. */
  reason_code?: string;
  /** Whether re-submitting could succeed. Present only when the send path
   *  genuinely knows; absent ≠ false. */
  retryable?: boolean;
}

/** Typed payload of an `email.delivered` event — the recipient's server
 *  accepted an outbound message, per recipient. The event TYPE is the
 *  outcome; there is no `status` field. */
export interface EmailDeliveredData {
  message_id: string;
  agent_email: string;
  /** Always "outbound" on this event. */
  direction: string;
  /** The one recipient address this per-recipient outcome is about. */
  delivered_to: string;
  subject?: string;
  /** Provider diagnostic (e.g. the remote SMTP response), when present. */
  smtp_detail?: string;
}

/** Typed payload of an `email.bounced` event — EmailDeliveredData's fields
 *  plus the SES bounce classification. */
export interface EmailBouncedData {
  message_id: string;
  agent_email: string;
  /** Always "outbound" on this event. */
  direction: string;
  /** The one recipient address this per-recipient outcome is about. */
  delivered_to: string;
  subject?: string;
  smtp_detail?: string;
  /** Normalized SES bounce classification. Only a permanent (hard) bounce
   *  auto-suppresses the address. Deliberately a closed set: exhaustive after
   *  server-side normalization ("undetermined" is the guaranteed catch-all). */
  bounce_type: "permanent" | "transient" | "undetermined";
  /** Raw SES bounceSubType (e.g. General, NoEmail, MailboxFull). */
  bounce_sub_type?: string;
}

/** Typed payload of an `email.complained` event — a recipient marked an
 *  outbound message as spam. `smtp_detail` carries the complaint feedback
 *  type when present. */
export interface EmailComplainedData {
  message_id: string;
  agent_email: string;
  /** Always "outbound" on this event. */
  direction: string;
  /** The one recipient address this per-recipient outcome is about. */
  delivered_to: string;
  subject?: string;
  smtp_detail?: string;
}

/** Typed payload of a `domain.sending_verified` event. */
export interface DomainSendingVerifiedData {
  domain: string;
  /** Open set; tolerate unknown values. Known values: verified. */
  sending_status: string;
}

/** Typed payload of a `domain.sending_failed` event. */
export interface DomainSendingFailedData {
  domain: string;
  /** Open set; tolerate unknown values. Known values: failed. */
  sending_status: string;
  reason?: string;
}

/** Typed payload of a `domain.suppression_added` event — an address was
 *  auto-suppressed after a hard bounce or complaint. Account-scoped despite
 *  the `domain.` prefix. */
export interface DomainSuppressionAddedData {
  address: string;
  /** Open set; tolerate unknown values. Known values: bounce, complaint. */
  source: string;
  reason?: string;
  /** The outbound message whose feedback triggered the suppression, when known. */
  message_id?: string;
}

// ── Discriminated narrowing guards ──────────────────────────────────────────
//
// `event.data` narrows to the typed payload:
//
//     const event = constructEvent(rawBody, header, secret);
//     if (isEmailReceived(event)) {
//       const msg = await client.webhooks.fetchMessage(event);
//     } else if (isEmailBounced(event)) {
//       console.log(event.data.bounce_type, event.data.delivered_to);
//     } // unknown/beta types: handle event.data generically.

/** Narrow a verified event to `email.received` with typed `data`. */
export function isEmailReceived(e: WebhookEvent): e is WebhookEvent & { type: "email.received"; data: EmailReceivedData } {
  return e.schema_version === "1" && e.type === "email.received";
}
/** Narrow a verified event to `email.sent` with typed `data`. */
export function isEmailSent(e: WebhookEvent): e is WebhookEvent & { type: "email.sent"; data: EmailSentData } {
  return e.schema_version === "1" && e.type === "email.sent";
}
/** Narrow a verified event to `email.failed` with typed `data`. */
export function isEmailFailed(e: WebhookEvent): e is WebhookEvent & { type: "email.failed"; data: EmailFailedData } {
  return e.schema_version === "1" && e.type === "email.failed";
}
/** Narrow a verified event to `email.delivered` with typed `data`. */
export function isEmailDelivered(e: WebhookEvent): e is WebhookEvent & { type: "email.delivered"; data: EmailDeliveredData } {
  return e.schema_version === "1" && e.type === "email.delivered";
}
/** Narrow a verified event to `email.bounced` with typed `data`. */
export function isEmailBounced(e: WebhookEvent): e is WebhookEvent & { type: "email.bounced"; data: EmailBouncedData } {
  return e.schema_version === "1" && e.type === "email.bounced";
}
/** Narrow a verified event to `email.complained` with typed `data`. */
export function isEmailComplained(e: WebhookEvent): e is WebhookEvent & { type: "email.complained"; data: EmailComplainedData } {
  return e.schema_version === "1" && e.type === "email.complained";
}
/** Narrow a verified event to `domain.sending_verified` with typed `data`. */
export function isDomainSendingVerified(e: WebhookEvent): e is WebhookEvent & { type: "domain.sending_verified"; data: DomainSendingVerifiedData } {
  return e.schema_version === "1" && e.type === "domain.sending_verified";
}
/** Narrow a verified event to `domain.sending_failed` with typed `data`. */
export function isDomainSendingFailed(e: WebhookEvent): e is WebhookEvent & { type: "domain.sending_failed"; data: DomainSendingFailedData } {
  return e.schema_version === "1" && e.type === "domain.sending_failed";
}
/** Narrow a verified event to `domain.suppression_added` with typed `data`. */
export function isDomainSuppressionAdded(e: WebhookEvent): e is WebhookEvent & { type: "domain.suppression_added"; data: DomainSuppressionAddedData } {
  return e.schema_version === "1" && e.type === "domain.suppression_added";
}

export interface ConstructEventOptions {
  toleranceSeconds?: number;
  now?: () => number;
}

function sigError(code: string, message: string): E2AWebhookSignatureError {
  return new E2AWebhookSignatureError({ code, message, status: 0, retryable: false });
}

/**
 * Verify a delivery and parse it into a typed {@link WebhookEvent} in one call
 * (Stripe's `constructEvent` shape). Throws {@link E2AWebhookSignatureError} on
 * a bad signature, a replay outside tolerance, or an unparseable body. This is
 * the recommended path — no separate verify step needed. Call it from your
 * webhook handler with the RAW request body.
 */
export function constructEvent(
  rawBody: string | Buffer,
  header: string,
  secret: string | string[],
  opts: ConstructEventOptions = {},
): WebhookEvent {
  if (!verifyWebhookSignature({ rawBody, header, secret, toleranceSeconds: opts.toleranceSeconds, now: opts.now })) {
    throw sigError("webhook_signature_invalid", "webhook signature verification failed");
  }
  const text = typeof rawBody === "string" ? rawBody : rawBody.toString("utf8");
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw sigError("webhook_body_invalid", "webhook body is not valid JSON");
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw sigError("webhook_body_invalid", "webhook event must be an object");
  }
  const envelope = parsed as Record<string, unknown>;
  if (
    typeof envelope.type !== "string" ||
    typeof envelope.id !== "string" ||
    typeof envelope.schema_version !== "string" ||
    typeof envelope.created_at !== "string" ||
    !envelope.data ||
    typeof envelope.data !== "object" ||
    Array.isArray(envelope.data)
  ) {
    throw sigError("webhook_body_invalid", "webhook event is missing required envelope fields");
  }
  return parsed as WebhookEvent;
}
