import type { components } from "./generated/types.js";
import { E2AApi, E2AApiError, envVar } from "./api.js";
import type { E2AApiOptions } from "./api.js";
import { InboundEmail } from "./inbound-email.js";
import type { WebhookPayload } from "./inbound-email.js";
import { WSStream } from "./ws.js";

type Schemas = components["schemas"];
type MessagePayload = Schemas["MessageDetail"] | WebhookPayload;

export interface E2AClientOptions extends E2AApiOptions {
  /**
   * Default agent email for message/WS operations. Falls back to the
   * `E2A_AGENT_EMAIL` environment variable when omitted.
   */
  agentEmail?: string;
}

/**
 * High-level e2a client wrapping {@link E2AApi}.
 *
 * Adds agent-email scoping and convenience methods on top of the raw API.
 */
export class E2AClient {
  readonly api: E2AApi;
  readonly agentEmail: string;

  constructor(opts: E2AClientOptions = {}) {
    this.api = new E2AApi(opts);
    this.agentEmail = opts.agentEmail || envVar("E2A_AGENT_EMAIL");
  }

  private requireEmail(override?: string): string {
    const email = override || this.agentEmail;
    if (!email) {
      throw new Error(
        "agentEmail is required. Pass it to E2AClient(), or provide it per-call.",
      );
    }
    return email;
  }

  // ── Webhook parsing ──────────────────────────────────────────────

  /**
   * Parse a webhook payload (or raw `MessageDetail`) into an {@link InboundEmail}.
   *
   * Returns an *unverified* InboundEmail — claim getters (sender,
   * subject, body, …) throw `UnverifiedEmailError` until you call
   * {@link InboundEmail.verifySignature}. For webhook handlers,
   * prefer {@link parseWebhook} which combines parse + verify.
   */
  async parse(body: Buffer | string | MessagePayload): Promise<InboundEmail> {
    const detail: MessagePayload =
      typeof body === "string"
        ? (JSON.parse(body) as MessagePayload)
        : Buffer.isBuffer(body)
          ? (JSON.parse(body.toString("utf-8")) as MessagePayload)
          : body;
    return InboundEmail.fromPayload(detail, this);
  }

  /**
   * Parse + HMAC-verify a webhook payload in one call.
   *
   * Recommended entry point for webhook handlers. Returns an
   * already-verified {@link InboundEmail} so getters work directly.
   * Throws on signature failure — let it bubble to a 401 response.
   *
   * `secret` defaults to the `E2A_HMAC_SECRET` environment variable.
   */
  async parseWebhook(
    body: Buffer | string | MessagePayload,
    secret?: string,
  ): Promise<InboundEmail> {
    const email = await this.parse(body);
    if (!email.verifySignature(secret)) {
      throw new Error("HMAC signature verification failed");
    }
    return email;
  }

  // ── Agents ──────────────────────────────────────────────────────

  async listAgents() {
    return this.api.listAgents();
  }

  /**
   * Register a new agent.
   *
   * For shared-domain agents, pass `slug` (just the local part, e.g. `"my-bot"`).
   * The server appends its configured shared domain automatically — do not
   * pass a full email. Slug registration only works on deployments where the
   * operator has enabled it; otherwise the request is rejected with 400.
   *
   * For custom-domain agents, pass `email` with the full address
   * (e.g. `"support@mycompany.com"`). The domain must be registered
   * and DNS-verified first.
   */
  async registerAgent(body: Schemas["RegisterAgentRequest"]) {
    return this.api.registerAgent(body);
  }

  async getAgent(email?: string) {
    return this.api.getAgent(this.requireEmail(email));
  }

  async deleteAgent(email?: string) {
    return this.api.deleteAgent(this.requireEmail(email));
  }

  /**
   * Update an agent's configuration. Pass any subset of fields; missing
   * fields keep their current value server-side. Most useful for
   * toggling HITL on/off or adjusting the approval window.
   */
  async updateAgent(
    body: Schemas["UpdateAgentRequest"],
    opts?: { agentEmail?: string },
  ) {
    return this.api.updateAgent(this.requireEmail(opts?.agentEmail), body);
  }

  // ── Messages ────────────────────────────────────────────────────

  async listMessages(opts?: {
    status?: "unread" | "read" | "all";
    pageSize?: number;
    token?: string;
    agentEmail?: string;
  }) {
    return this.api.listMessages(this.requireEmail(opts?.agentEmail), {
      status: opts?.status,
      pageSize: opts?.pageSize,
      token: opts?.token,
    });
  }

  /**
   * Fetch a message and return a parsed {@link InboundEmail}.
   *
   * The returned email is **pre-verified** — the REST API channel is
   * authenticated by the bearer token, so an additional HMAC verify on
   * the response would be redundant. This differs from {@link parse}
   * (webhook entry), which leaves the email unverified until you
   * explicitly verify it.
   *
   * For the raw `MessageDetail` JSON, use `client.api.getMessage()`.
   */
  async getMessage(
    messageId: string,
    agentEmail?: string,
  ): Promise<InboundEmail> {
    const detail = await this.api.getMessage(
      this.requireEmail(agentEmail),
      messageId,
    );
    return InboundEmail.fromPayload(detail, this, /*trusted=*/ true);
  }

  async reply(
    messageId: string,
    body: string,
    opts?: {
      htmlBody?: string;
      replyAll?: boolean;
      cc?: string[];
      bcc?: string[];
      conversationId?: string;
      attachments?: Schemas["internal_agent.Attachment"][];
      agentEmail?: string;
    },
  ) {
    const req: Schemas["ReplyToMessageRequest"] = { body };
    if (opts?.htmlBody) req.html_body = opts.htmlBody;
    if (opts?.replyAll) req.reply_all = opts.replyAll;
    if (opts?.cc) req.cc = opts.cc;
    if (opts?.bcc) req.bcc = opts.bcc;
    if (opts?.conversationId) req.conversation_id = opts.conversationId;
    if (opts?.attachments) req.attachments = opts.attachments;
    return this.api.replyToMessage(
      this.requireEmail(opts?.agentEmail),
      messageId,
      req,
    );
  }

  // ── Domains ─────────────────────────────────────────────────────

  async listDomains() {
    return this.api.listDomains();
  }

  async registerDomain(domain: string) {
    return this.api.registerDomain({ domain });
  }

  async deleteDomain(domain: string) {
    return this.api.deleteDomain(domain);
  }

  async verifyDomain(domain: string) {
    return this.api.verifyDomain(domain);
  }

  // ── Send ────────────────────────────────────────────────────────

  async send(
    to: string[],
    subject: string,
    body: string,
    opts?: {
      htmlBody?: string;
      cc?: string[];
      bcc?: string[];
      conversationId?: string;
      attachments?: Schemas["internal_agent.Attachment"][];
      agentEmail?: string;
    },
  ) {
    const req: Schemas["SendEmailRequest"] = {
      from: this.requireEmail(opts?.agentEmail),
      to,
      subject,
      body,
    };
    if (opts?.htmlBody) req.html_body = opts.htmlBody;
    if (opts?.cc) req.cc = opts.cc;
    if (opts?.bcc) req.bcc = opts.bcc;
    if (opts?.conversationId) req.conversation_id = opts.conversationId;
    if (opts?.attachments) req.attachments = opts.attachments;
    return this.api.sendEmail(req);
  }

  // ── HITL (human-in-the-loop approval) ───────────────────────────

  /**
   * List pending-approval messages across every agent owned by the
   * authenticated user, sorted by soonest-expiring first.
   */
  async listPendingMessages() {
    return this.api.listPendingMessages();
  }

  /** Fetch the full detail of one held outbound message. */
  async getPendingMessage(messageId: string) {
    return this.api.getPendingMessage(messageId);
  }

  /**
   * Approve a held outbound message. Pass overrides to approve with
   * edits; omit for approve-as-is.
   */
  async approveMessage(
    messageId: string,
    overrides: Schemas["ApprovePendingMessageRequest"] = {},
  ) {
    return this.api.approveMessage(messageId, overrides);
  }

  /**
   * Reject a held outbound message. The message is discarded; the
   * optional reason is stored for audit.
   */
  async rejectMessage(messageId: string, reason?: string) {
    return this.api.rejectMessage(messageId, reason);
  }

  // ── WebSocket ──────────────────────────────────────────────────

  /**
   * Listen for inbound mail via WebSocket. Returns a {@link WSStream},
   * which is both an `AsyncIterable<WSNotification>` and an
   * `EventEmitter` — pick whichever access pattern fits.
   *
   *     for await (const notif of client.listen()) {
   *       if (notif.subject.startsWith("URGENT")) {
   *         const email = await client.api.getMessage(notif.recipient, notif.message_id);
   *         // …
   *       }
   *     }
   *
   * Yielded notifications are lightweight metadata only — the body is
   * not auto-fetched. This matches the server's design (small WS
   * frames, explicit REST fetch) and lets callers skip messages
   * without a network round-trip.
   *
   * Reconnects with exponential backoff (1s → 30s by default).
   *
   * @param opts.agentEmail Override the client's default agent email.
   * @param opts.maxBackoffMs Cap on the reconnect delay (default 30s).
   */
  listen(opts: { agentEmail?: string; maxBackoffMs?: number } = {}): WSStream {
    const agentEmail = opts.agentEmail || this.agentEmail;
    if (!agentEmail) {
      throw new Error(
        "agentEmail is required. Pass it to E2AClient(), set E2A_AGENT_EMAIL, or pass it to listen({ agentEmail }).",
      );
    }
    return new WSStream({
      apiKey: this.api.apiKey,
      agentEmail,
      baseUrl: this.api.baseUrl,
      maxBackoffMs: opts.maxBackoffMs,
    });
  }
}

export { E2AApiError };
