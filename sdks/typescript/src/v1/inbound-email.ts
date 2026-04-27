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
 * Thrown when accessing claim fields on an InboundEmail before
 * {@link InboundEmail.verifySignature} has succeeded.
 *
 * This is a security feature: the SDK refuses to expose
 * attacker-controllable fields (sender, recipient, body, subject, …)
 * until you've cryptographically verified the payload. Catch this only
 * to handle a known unverified path; treat its presence in production
 * as a bug to fix by calling verifySignature() or using
 * {@link E2AClient.parseWebhook} (which verifies for you).
 *
 * For inspection without verifying (e.g. forensics on a malformed
 * delivery), use {@link InboundEmail.unverifiedPayload} — explicit,
 * named, and documented as attacker-controllable.
 */
export class UnverifiedEmailError extends Error {
  constructor(message?: string) {
    super(
      message ??
        "Call verifySignature(secret) before accessing this field. " +
          "For inspection without verification, use .unverifiedPayload.",
    );
    this.name = "UnverifiedEmailError";
  }
}

/** Read an env var if `process.env` is reachable (Node), else "". */
function envHmacSecret(): string {
  if (typeof process !== "undefined" && process.env && process.env.E2A_HMAC_SECRET) {
    return process.env.E2A_HMAC_SECRET;
  }
  return "";
}

/**
 * A parsed inbound email with convenience methods.
 *
 * Field access is gated behind {@link verifySignature}: getters like
 * `sender`, `recipient`, `textBody` throw {@link UnverifiedEmailError}
 * until verify succeeds. Recommended entry point for webhook handlers
 * is {@link E2AClient.parseWebhook}, which combines parse + verify.
 *
 * Always-available (un-gated) members: `auth`, `rawMessage`,
 * `isVerified`, `verified`, `verifySignature`, `unverifiedPayload`.
 *
 * Gated (require verify first): `messageId`, `conversationId`,
 * `sender`, `recipient`, `to`, `cc`, `subject`, `textBody`, `htmlBody`,
 * `attachments`, `receivedAt`, `reply()`.
 */
export class InboundEmail {
  // Stored as private fields; public getters check this._verified.
  private readonly _messageId: string;
  private readonly _conversationId: string | null;
  private readonly _sender: string;
  private readonly _recipient: string;
  private readonly _to: string[];
  private readonly _cc: string[];
  private readonly _subject: string;
  private readonly _textBody: string;
  private readonly _htmlBody: string | null;
  private readonly _attachments: Attachment[];
  /** Always accessible — verifySignature itself reads this. */
  readonly auth: AuthHeaders;
  /** Always accessible — verifySignature itself reads this. */
  readonly rawMessage: Buffer;
  private readonly _receivedAt: string | null;
  private readonly _client: E2AClient;
  private _verified = false;

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
    /** REST-fetched messages are pre-verified (channel auth via API key). */
    trusted?: boolean;
  }) {
    this._messageId = opts.messageId;
    this._conversationId = opts.conversationId;
    this._sender = opts.sender;
    this._recipient = opts.recipient;
    this._to = opts.to;
    this._cc = opts.cc;
    this._subject = opts.subject;
    this._textBody = opts.textBody;
    this._htmlBody = opts.htmlBody;
    this._attachments = opts.attachments;
    this.auth = opts.auth;
    this.rawMessage = opts.rawMessage;
    this._receivedAt = opts.receivedAt;
    this._client = opts.client;
    this._verified = !!opts.trusted;
  }

  /** True if {@link verifySignature} has succeeded on this instance. */
  get verified(): boolean {
    return this._verified;
  }

  /**
   * The server's *claim* that the sender's domain passed SPF/DKIM.
   *
   * IMPORTANT: this reflects the `X-E2A-Auth-Verified` header — anyone
   * who can POST to your webhook can set it to `true`. Call
   * {@link verifySignature} and check {@link verified} for security
   * decisions.
   */
  get isVerified(): boolean {
    return this.auth.verified;
  }

  /**
   * Inspect the parsed payload **without** HMAC verification. Returned
   * fields are attacker-controllable until verifySignature succeeds —
   * never feed into security or identity decisions. Useful only for
   * debugging delivery issues.
   */
  get unverifiedPayload(): {
    messageId: string;
    conversationId: string | null;
    sender: string;
    recipient: string;
    to: string[];
    cc: string[];
    subject: string;
    textBody: string;
    htmlBody: string | null;
    receivedAt: string | null;
    attachmentsCount: number;
  } {
    return {
      messageId: this._messageId,
      conversationId: this._conversationId,
      sender: this._sender,
      recipient: this._recipient,
      to: [...this._to],
      cc: [...this._cc],
      subject: this._subject,
      textBody: this._textBody,
      htmlBody: this._htmlBody,
      receivedAt: this._receivedAt,
      attachmentsCount: this._attachments.length,
    };
  }

  /**
   * Cryptographically verify the auth headers and unlock field access.
   *
   * On success, transitions this instance to "verified" so subsequent
   * getters work. `secret` defaults to the `E2A_HMAC_SECRET`
   * environment variable when omitted.
   *
   * Returns `true` if HMAC + body-hash + timestamp checks all pass.
   * Returns `false` for any tampering / expired / wrong-secret —
   * instance stays unverified and field access keeps throwing.
   *
   * Throws if no secret is available (neither passed nor in env).
   */
  verifySignature(secret?: string): boolean {
    const resolved = secret ?? envHmacSecret();
    if (!resolved) {
      throw new Error(
        "verifySignature requires a secret. Pass it explicitly or set E2A_HMAC_SECRET in the environment.",
      );
    }
    const ok = verifyAuthHeaders(this.auth, this.rawMessage, resolved);
    if (ok) this._verified = true;
    return ok;
  }

  // --- Gated claim getters ---

  private requireVerified(): void {
    if (!this._verified) throw new UnverifiedEmailError();
  }

  get messageId(): string { this.requireVerified(); return this._messageId; }
  get conversationId(): string | null { this.requireVerified(); return this._conversationId; }
  get sender(): string { this.requireVerified(); return this._sender; }
  get recipient(): string { this.requireVerified(); return this._recipient; }
  get to(): string[] { this.requireVerified(); return this._to; }
  get cc(): string[] { this.requireVerified(); return this._cc; }
  get subject(): string { this.requireVerified(); return this._subject; }
  get textBody(): string { this.requireVerified(); return this._textBody; }
  get htmlBody(): string | null { this.requireVerified(); return this._htmlBody; }
  get attachments(): Attachment[] { this.requireVerified(); return this._attachments; }
  get receivedAt(): string | null { this.requireVerified(); return this._receivedAt; }

  /** Reply to this email. Requires the email to be verified first. */
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
    // Accessing this.messageId / this.recipient would throw too — but
    // doing the check up front gives a clearer error for callers.
    this.requireVerified();
    return this._client.reply(this._messageId, body, {
      ...opts,
      agentEmail: this._recipient,
    });
  }

  /**
   * Build an InboundEmail from a raw `MessageDetail` response.
   *
   * Decodes the base64 `raw_message`, parses MIME headers/body/attachments.
   */
  /**
   * Build an InboundEmail from a raw `MessageDetail` response.
   *
   * `trusted` marks the result as already-verified — used by the REST
   * polling path (`client.getMessage`), which fetched data over the
   * authenticated API channel. The webhook path leaves `trusted=false`
   * (default) so callers must verify before reading claim fields.
   */
  static async fromPayload(
    detail: MessagePayload,
    client: E2AClient,
    trusted: boolean = false,
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
      trusted,
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
