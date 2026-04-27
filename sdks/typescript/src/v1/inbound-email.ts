import { createHash, createHmac, timingSafeEqual } from "node:crypto";
import {
  simpleParser,
  type ParsedMail,
  type Attachment as ParsedAttachment,
} from "mailparser";
import type { components } from "./generated/types.js";
import type { E2AClient } from "./client.js";

type Schemas = components["schemas"];

// Replay window matches the Go server's headers.DefaultMaxAge.
const REPLAY_WINDOW_MS = 5 * 60 * 1000;
// Tolerance for clock skew where the timestamp appears slightly in the
// future relative to local time. Matches the server's negative-skew
// allowance.
const FUTURE_SKEW_MS = 30 * 1000;

export interface Attachment {
  filename: string;
  contentType: string;
  data: Buffer;
  size: number;
}

export interface WebhookPayload {
  message_id: string;
  conversation_id?: string;
  from: string;
  /** Parsed To: header from the original message — every fan-out delivery sees the same list. */
  to: string[];
  /** Parsed Cc: header. Empty when the message had no CCs. */
  cc?: string[];
  /** Per-delivery target — this agent's address. */
  recipient: string;
  subject?: string;
  raw_message?: string;
  auth_headers?: Record<string, string>;
  received_at?: string;
}

/**
 * Parsed e2a authentication headers matching the server contract.
 *
 * IMPORTANT: `verified` reflects the value of the
 * `X-E2A-Auth-Verified` header — i.e. the *server's claim*. It is not
 * a cryptographic verification. Anyone who can POST to your webhook
 * URL can set this to `true`. Use {@link InboundEmail.verifySignature}
 * before making security decisions.
 */
export interface AuthHeaders {
  verified: boolean;
  sender: string;
  entityType: string;
  domainCheck: string;
  delegation: string;
  signature: string;
  timestamp: string;
  messageId: string;
  bodyHash: string;
}

function parseAuthHeaders(headers: Record<string, string>): AuthHeaders {
  return {
    verified: (headers["X-E2A-Auth-Verified"] || "").toLowerCase() === "true",
    sender: headers["X-E2A-Auth-Sender"] || "",
    entityType: headers["X-E2A-Auth-Entity-Type"] || "",
    domainCheck: headers["X-E2A-Auth-Domain-Check"] || "",
    delegation: headers["X-E2A-Auth-Delegation"] || "",
    signature: headers["X-E2A-Auth-Signature"] || "",
    timestamp: headers["X-E2A-Auth-Timestamp"] || "",
    messageId: headers["X-E2A-Auth-Message-Id"] || "",
    bodyHash: headers["X-E2A-Auth-Body-Hash"] || "",
  };
}

/**
 * Reconstruct the byte string fed to HMAC. Field order must match the
 * Go server's headers.canonicalString — changing it is a wire contract
 * change that requires coordinated server + SDK release.
 */
function canonicalString(h: AuthHeaders): string {
  return [
    h.verified ? "true" : "false",
    h.sender,
    h.entityType,
    h.domainCheck,
    h.delegation,
    h.timestamp,
    h.messageId,
    h.bodyHash,
  ].join("\n");
}

function constantTimeEqualHex(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  return timingSafeEqual(Buffer.from(a, "hex"), Buffer.from(b, "hex"));
}

function verifyAuthHeaders(
  h: AuthHeaders,
  rawMessage: Buffer,
  secret: string,
): boolean {
  if (!h.signature) return false;

  // Bind to the actual body bytes the recipient received.
  const actualBodyHash = createHash("sha256").update(rawMessage).digest("hex");
  if (!constantTimeEqualHex(h.bodyHash, actualBodyHash)) return false;

  // Replay protection: reject obviously old or future-skewed timestamps.
  const ts = Date.parse(h.timestamp);
  if (Number.isNaN(ts)) return false;
  const age = Date.now() - ts;
  if (age < -FUTURE_SKEW_MS || age > REPLAY_WINDOW_MS) return false;

  const expected = createHmac("sha256", secret)
    .update(canonicalString(h))
    .digest("hex");
  return constantTimeEqualHex(h.signature, expected);
}

type MessagePayload = Schemas["MessageDetail"] | WebhookPayload;

/**
 * A parsed inbound email with convenience methods.
 *
 * Returned by {@link E2AClient.getMessage} and {@link E2AClient.parse}.
 * The raw `MessageDetail` is available from {@link E2AClient.api.getMessage}
 * for callers that need the exact server response (e.g. `--json` output).
 */
export class InboundEmail {
  readonly messageId: string;
  readonly conversationId: string | null;
  readonly sender: string;
  /** Per-delivery target — this agent's address. Always one value. */
  readonly recipient: string;
  /** Parsed To: header — all addresses from the original message. */
  readonly to: string[];
  /** Parsed Cc: header. Empty when the message had no CCs. */
  readonly cc: string[];
  readonly subject: string;
  readonly textBody: string;
  readonly htmlBody: string | null;
  readonly attachments: Attachment[];
  readonly auth: AuthHeaders;
  readonly receivedAt: string | null;
  readonly rawMessage: Buffer;
  private readonly _client: E2AClient;

  constructor(opts: {
    messageId: string;
    conversationId: string | null;
    sender: string;
    recipient: string;
    to: string[];
    cc: string[];
    subject: string;
    textBody: string;
    htmlBody: string | null;
    attachments: Attachment[];
    auth: AuthHeaders;
    receivedAt: string | null;
    rawMessage: Buffer;
    client: E2AClient;
  }) {
    this.messageId = opts.messageId;
    this.conversationId = opts.conversationId;
    this.sender = opts.sender;
    this.recipient = opts.recipient;
    this.to = opts.to;
    this.cc = opts.cc;
    this.subject = opts.subject;
    this.textBody = opts.textBody;
    this.htmlBody = opts.htmlBody;
    this.attachments = opts.attachments;
    this.auth = opts.auth;
    this.receivedAt = opts.receivedAt;
    this.rawMessage = opts.rawMessage;
    this._client = opts.client;
  }

  /**
   * The server's *claim* that the sender's domain passed SPF/DKIM.
   *
   * IMPORTANT: This reflects the `X-E2A-Auth-Verified` header value,
   * **not** a cryptographic check. Anyone who can POST to your
   * webhook URL can set this to `true`. Call {@link verifySignature}
   * and trust its return value before making security decisions.
   */
  get isVerified(): boolean {
    return this.auth.verified;
  }

  /**
   * Cryptographically verify the auth headers were issued by an e2a
   * instance holding `secret` and are bound to this exact message.
   *
   * Checks:
   * 1. `SHA-256(rawMessage)` matches `X-E2A-Auth-Body-Hash`
   * 2. `HMAC-SHA256(secret, canonical)` matches `X-E2A-Auth-Signature`
   * 3. `X-E2A-Auth-Timestamp` is within the 5-minute replay window
   *
   * Returns `true` if all three pass. Treat `false` as untrusted —
   * the {@link isVerified} claim alone is not a security guarantee.
   */
  verifySignature(secret: string): boolean {
    return verifyAuthHeaders(this.auth, this.rawMessage, secret);
  }

  /** Reply to this email. */
  async reply(
    body: string,
    opts?: {
      htmlBody?: string;
      replyAll?: boolean;
      cc?: string[];
      bcc?: string[];
      conversationId?: string;
      attachments?: Schemas["internal_agent.Attachment"][];
    },
  ) {
    return this._client.reply(this.messageId, body, {
      ...opts,
      agentEmail: this.recipient,
    });
  }

  /**
   * Build an InboundEmail from a raw `MessageDetail` response.
   *
   * Decodes the base64 `raw_message`, parses MIME headers/body/attachments.
   */
  static async fromPayload(
    detail: MessagePayload,
    client: E2AClient,
  ): Promise<InboundEmail> {
    let rawBuf = Buffer.alloc(0);
    if (detail.raw_message) {
      try {
        rawBuf = Buffer.from(detail.raw_message, "base64");
      } catch {
        rawBuf = Buffer.from(detail.raw_message);
      }
    }

    const { subject, textBody, htmlBody, attachments } =
      await parseRawEmail(rawBuf);

    // MessageDetail uses created_at; WebhookPayload uses received_at
    const d = detail as Record<string, unknown>;
    const receivedAt = (d.created_at as string | undefined) ??
      (d.received_at as string | undefined) ?? null;

    // The server emits `to`/`cc` as parsed address arrays and `recipient` as
    // the per-delivery target string. We trust those over re-parsing the raw
    // RFC 2822 headers, which is both wasteful and lossy under group syntax.
    return new InboundEmail({
      messageId: detail.message_id ?? "",
      conversationId: detail.conversation_id ?? null,
      sender: detail.from ?? "",
      recipient: detail.recipient ?? "",
      to: detail.to ?? [],
      cc: detail.cc ?? [],
      subject: subject || detail.subject || "",
      textBody,
      htmlBody,
      attachments,
      auth: parseAuthHeaders(detail.auth_headers ?? {}),
      receivedAt,
      rawMessage: rawBuf,
      client,
    });
  }

  static async fromMessageDetail(
    detail: MessagePayload,
    client: E2AClient,
  ): Promise<InboundEmail> {
    return InboundEmail.fromPayload(detail, client);
  }
}

async function parseRawEmail(
  raw: Buffer,
): Promise<{
  subject: string;
  textBody: string;
  htmlBody: string | null;
  attachments: Attachment[];
}> {
  try {
    const parsed: ParsedMail = await simpleParser(raw);
    const attachments: Attachment[] = (parsed.attachments || []).map(
      (att: ParsedAttachment) => ({
        filename: att.filename || "unnamed",
        contentType: att.contentType,
        data: att.content,
        size: att.size,
      }),
    );
    return {
      subject: parsed.subject || "",
      textBody: parsed.text || "",
      htmlBody: parsed.html || null,
      attachments,
    };
  } catch {
    return { subject: "", textBody: "", htmlBody: null, attachments: [] };
  }
}
