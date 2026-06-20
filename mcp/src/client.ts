import { E2AClient } from "@e2a/sdk/v1";
import type {
  AccountView,
  AgentView,
  MessageView,
  MessageSummaryView,
  SendResultView,
  RejectResultView,
  UpdateMessageResultView,
  ConversationSummaryView,
  ConversationDetailView,
  DomainView,
  VerifyDomainView,
  EventJSON,
  RedeliverView,
  WebhookView,
  WebhookDeliveryView,
  CreateWebhookResponse,
  RotateSecretResponse,
  TestWebhookResponse,
  Attachment,
  UpdateAgentRequest,
  CreateWebhookRequest,
  UpdateWebhookRequest,
  TestWebhookRequest,
} from "@e2a/sdk/v1";
import type { McpConfig } from "./config.js";
import type { Scope } from "./tools/tiers.js";

// Outbound drafts held for human approval surface in the message list
// with this status (see api/openapi.yaml listMessages: "Held outbound
// drafts appear as status=pending_approval"). There is no dedicated
// status query param for them, so HITL listing filters outbound rows
// on this value.
export const PENDING_APPROVAL_STATUS = "pending_approval";

const DEFAULT_LIST_LIMIT = 1000;

/** Per-call options for unsafe writes. */
export interface SendOpts {
  idempotencyKey?: string;
}

/**
 * Thin MCP-facing wrapper over the namespaced v1 {@link E2AClient}.
 *
 * The MCP tools and the HTTP session layer used to lean on the retired
 * flat SDK surface (`client.send`, `client.api.*`, `client.agentEmail`).
 * The v1 client is namespaced (`client.agents`, `.messages`, …) and
 * every per-agent method takes an explicit address as its first arg.
 *
 * This wrapper concentrates the address-resolution policy (a single
 * default agent email pinned at session init from the credential) and
 * the small amount of shape-mapping the tools need, so each tool stays a
 * thin pass-through and the tool contracts (names, schemas) are
 * unchanged. The session prefetch in http-server.ts still constructs one
 * of these per session and may re-pin a resolved single-agent default.
 */
export class McpClient {
  readonly sdk: E2AClient;
  /** Default per-agent address; mirrors the old flat `agentEmail`. */
  readonly agentEmail: string;
  /**
   * The connecting credential's scope (§6a tier-gating signal). Resolved at
   * session init from whoami (GET /account). Defaults to "account" (full
   * surface) so direct constructions / tests are unchanged; buildSessionClient
   * sets the real value per credential.
   */
  readonly scope: Scope;

  constructor(sdk: E2AClient, agentEmail = "", scope: Scope = "account") {
    this.sdk = sdk;
    this.agentEmail = agentEmail;
    this.scope = scope;
  }

  // resolveAddress picks the explicit per-call address, falling back to
  // the pinned default. Throws a directive error when neither resolves.
  private resolveAddress(explicit?: string): string {
    const addr = explicit || this.agentEmail;
    if (!addr) {
      throw new Error(
        "email is required — pass it explicitly, or connect with an agent-scoped credential (which resolves the agent for you). Run list_agents to see your agents.",
      );
    }
    return addr;
  }

  // ── Account / identity ──────────────────────────────────────────

  // whoami → GET /account (§6a): the authenticated principal (user + scope;
  // agent_address only for agent-scoped credentials) + plan/limits. NOT an
  // agent — discover agents via list_agents.
  whoami(): Promise<AccountView> {
    return this.sdk.account.get();
  }

  // ── Agents ──────────────────────────────────────────────────────

  async listAgents(): Promise<AgentView[]> {
    return this.sdk.agents.list().toArray({ limit: DEFAULT_LIST_LIMIT });
  }

  getAgent(address: string): Promise<AgentView> {
    return this.sdk.agents.get(address);
  }

  // create_agent takes a full email (§6a / AG-1/2): a custom-domain agent on a
  // verified owned domain, or an email on the shared domain. slug/agent_mode/
  // webhook_url are gone. Returns the full AgentView.
  createAgent(body: { email: string; name?: string }): Promise<AgentView> {
    return this.sdk.agents.create(body);
  }

  updateAgent(
    patch: {
      hitlEnabled?: boolean;
      hitlTtlSeconds?: number;
      hitlExpirationAction?: string;
      hitlMode?: string;
      inboundPolicy?: string;
      inboundAllowlist?: Array<string>;
    },
    explicitAddress?: string,
  ): Promise<AgentView> {
    return this.sdk.agents.update(this.resolveAddress(explicitAddress), patch as UpdateAgentRequest);
  }

  async deleteAgent(explicitAddress?: string): Promise<string> {
    const address = this.resolveAddress(explicitAddress);
    await this.sdk.agents.delete(address);
    return address;
  }

  // ── Messages ────────────────────────────────────────────────────

  send(
    body: {
      to: Array<string>;
      subject: string;
      body: string;
      htmlBody?: string;
      cc?: Array<string>;
      bcc?: Array<string>;
      attachments?: Array<Attachment>;
      conversationId?: string;
    },
    opts: SendOpts = {},
    explicitAddress?: string,
  ): Promise<SendResultView> {
    return this.sdk.messages.send(this.resolveAddress(explicitAddress), body, opts);
  }

  reply(
    messageId: string,
    body: {
      body: string;
      htmlBody?: string;
      replyAll?: boolean;
      cc?: Array<string>;
      bcc?: Array<string>;
      attachments?: Array<Attachment>;
      conversationId?: string;
    },
    opts: SendOpts = {},
    explicitAddress?: string,
  ): Promise<SendResultView> {
    return this.sdk.messages.reply(
      this.resolveAddress(explicitAddress),
      messageId,
      body,
      opts,
    );
  }

  forward(
    messageId: string,
    to: Array<string>,
    body: {
      body: string; // required (MSG-3): forward must carry a note; subject is derived
      htmlBody?: string;
      cc?: Array<string>;
      bcc?: Array<string>;
      attachments?: Array<Attachment>;
      conversationId?: string;
    },
    opts: SendOpts = {},
    explicitAddress?: string,
  ): Promise<SendResultView> {
    return this.sdk.messages.forward(
      this.resolveAddress(explicitAddress),
      messageId,
      { to, ...body },
      opts,
    );
  }

  updateMessageLabels(
    messageId: string,
    body: { addLabels?: Array<string>; removeLabels?: Array<string> },
    explicitAddress?: string,
  ): Promise<UpdateMessageResultView> {
    return this.sdk.messages.updateLabels(
      this.resolveAddress(explicitAddress),
      messageId,
      body,
    );
  }

  getMessage(messageId: string, explicitAddress?: string): Promise<MessageView> {
    return this.sdk.messages.get(this.resolveAddress(explicitAddress), messageId);
  }

  async listMessages(params: {
    readStatus?: "unread" | "read" | "all";
    sort?: "asc" | "desc";
    from?: string;
    subjectContains?: string;
    conversationId?: string;
    since?: string;
    until?: string;
    labels?: Array<string>;
    limit?: number;
    explicitAddress?: string;
  }): Promise<MessageSummaryView[]> {
    const { explicitAddress, limit, ...rest } = params;
    return this.sdk.messages
      .list(this.resolveAddress(explicitAddress), rest)
      .toArray({ limit: limit ?? DEFAULT_LIST_LIMIT });
  }

  // ── Conversations ───────────────────────────────────────────────

  listConversations(
    params: { since?: string; until?: string; limit?: number },
    explicitAddress?: string,
  ): Promise<ConversationSummaryView[]> {
    return this.sdk.conversations.list(this.resolveAddress(explicitAddress), params).toArray({ limit: params.limit ?? 200 });
  }

  getConversation(
    conversationId: string,
    explicitAddress?: string,
  ): Promise<ConversationDetailView> {
    return this.sdk.conversations.get(
      this.resolveAddress(explicitAddress),
      conversationId,
    );
  }

  // ── HITL (pending outbound) ─────────────────────────────────────

  // Pending drafts surface as outbound messages with status=pending_approval.
  // There is no dedicated "pending" status filter, so we list outbound and
  // filter on the status field. Searches across every owned agent when no
  // default address is pinned so the queue is visible without a default.
  async listPendingMessages(): Promise<MessageSummaryView[]> {
    const addresses = this.agentEmail
      ? [this.agentEmail]
      : (await this.listAgents()).map((a) => a.email);
    const out: MessageSummaryView[] = [];
    for (const address of addresses) {
      const rows = await this.sdk.messages
        .list(address, { direction: "outbound" })
        .toArray({ limit: DEFAULT_LIST_LIMIT });
      for (const r of rows) {
        // Held drafts carry the HITL lifecycle in hitl_status (read_status is
        // the inbox read-state, "" for outbound). MSG-1.
        if (r.hitlStatus === PENDING_APPROVAL_STATUS) out.push(r);
      }
    }
    return out;
  }

  // Resolve the agent that owns a pending message by scanning the pending
  // queue. The approve/reject endpoints are agent-scoped, so we need the
  // owning address; for a pinned-default session this is one list call.
  private async ownerOfPending(messageId: string): Promise<string> {
    const addresses = this.agentEmail
      ? [this.agentEmail]
      : (await this.listAgents()).map((a) => a.email);
    for (const address of addresses) {
      const rows = await this.sdk.messages
        .list(address, { direction: "outbound" })
        .toArray({ limit: DEFAULT_LIST_LIMIT });
      if (rows.some((r) => r.messageId === messageId)) return address;
    }
    throw new Error(
      `pending message ${messageId} not found on any owned agent (it may have already been approved, rejected, or expired).`,
    );
  }

  async getPendingMessage(messageId: string): Promise<MessageView> {
    const address = await this.ownerOfPending(messageId);
    return this.sdk.messages.get(address, messageId);
  }

  async approveMessage(
    messageId: string,
    overrides: {
      subject?: string;
      body?: string;
      htmlBody?: string;
      to?: Array<string>;
      cc?: Array<string>;
      bcc?: Array<string>;
      attachments?: Array<Attachment>;
    },
    opts?: SendOpts,
  ): Promise<SendResultView> {
    const address = await this.ownerOfPending(messageId);
    return opts
      ? this.sdk.messages.approve(address, messageId, overrides, opts)
      : this.sdk.messages.approve(address, messageId, overrides);
  }

  async rejectMessage(messageId: string, reason?: string): Promise<RejectResultView> {
    const address = await this.ownerOfPending(messageId);
    return this.sdk.messages.reject(
      address,
      messageId,
      reason !== undefined ? { reason } : {},
    );
  }

  // ── Domains ─────────────────────────────────────────────────────

  listDomains(): Promise<DomainView[]> {
    return this.sdk.domains.list().toArray({ limit: DEFAULT_LIST_LIMIT });
  }

  getDomain(domain: string): Promise<DomainView> {
    return this.sdk.domains.get(domain);
  }

  registerDomain(domain: string): Promise<DomainView> {
    return this.sdk.domains.create({ domain });
  }

  verifyDomain(domain: string): Promise<VerifyDomainView> {
    return this.sdk.domains.verify(domain);
  }

  async deleteDomain(domain: string): Promise<void> {
    await this.sdk.domains.delete(domain);
  }

  // ── Webhooks ────────────────────────────────────────────────────

  listWebhooks(): Promise<WebhookView[]> {
    return this.sdk.webhooks.list().toArray({ limit: DEFAULT_LIST_LIMIT });
  }

  getWebhook(id: string): Promise<WebhookView> {
    return this.sdk.webhooks.get(id);
  }

  createWebhook(body: {
    url: string;
    events: Array<string>;
    description?: string;
    filters?: { agentIds?: Array<string>; conversationIds?: Array<string>; labels?: Array<string> };
  }): Promise<CreateWebhookResponse> {
    return this.sdk.webhooks.create(body as CreateWebhookRequest);
  }

  updateWebhook(
    id: string,
    patch: {
      url?: string;
      events?: Array<string>;
      description?: string;
      enabled?: boolean;
      filters?: { agentIds?: Array<string>; conversationIds?: Array<string>; labels?: Array<string> };
    },
  ): Promise<WebhookView> {
    return this.sdk.webhooks.update(id, patch as UpdateWebhookRequest);
  }

  async deleteWebhook(id: string): Promise<void> {
    await this.sdk.webhooks.delete(id);
  }

  rotateWebhookSecret(id: string): Promise<RotateSecretResponse> {
    return this.sdk.webhooks.rotateSecret(id);
  }

  testWebhook(id: string, body: { event?: string }): Promise<TestWebhookResponse> {
    return this.sdk.webhooks.test(id, body as TestWebhookRequest);
  }

  // Per-delivery debugging (status/attempts/last_error/last_status_code) for one
  // webhook. Single page by contract (no cursor); GET /v1/webhooks/{id}/deliveries.
  listWebhookDeliveries(
    id: string,
    params: { status?: "pending" | "delivered" | "failed"; limit?: number },
  ): Promise<WebhookDeliveryView[]> {
    return this.sdk.webhooks
      .deliveries(id, params)
      .toArray({ limit: params.limit ?? DEFAULT_LIST_LIMIT });
  }

  // ── Events ──────────────────────────────────────────────────────

  listEvents(params: {
    type?: string;
    agentId?: string;
    conversationId?: string;
    messageId?: string;
    since?: string;
    until?: string;
    limit?: number;
  }): Promise<EventJSON[]> {
    const { limit, ...rest } = params;
    return this.sdk.events.list(rest).toArray({ limit: limit ?? DEFAULT_LIST_LIMIT });
  }

  getEvent(id: string): Promise<EventJSON> {
    return this.sdk.events.get(id);
  }

  redeliverEvent(id: string, webhookId?: string): Promise<RedeliverView> {
    return this.sdk.events.redeliver(
      id,
      webhookId !== undefined ? { webhookId } : {},
    );
  }
}

export function makeClient(cfg: McpConfig): McpClient {
  const sdk = new E2AClient({
    apiKey: cfg.apiKey,
    ...(cfg.baseUrl ? { baseUrl: cfg.baseUrl } : {}),
  });
  return new McpClient(sdk, cfg.agentEmail ?? "");
}
