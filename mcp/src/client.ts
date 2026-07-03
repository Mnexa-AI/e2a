import { E2AClient, E2AError } from "@e2a/sdk/v1";
import type {
  AccountView,
  AgentView,
  MessageView,
  AttachmentView,
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
  ProtectionConfigView,
  CreateWebhookRequest,
  UpdateWebhookRequest,
  TestWebhookRequest,
  Page,
} from "@e2a/sdk/v1";
import type { McpConfig } from "./config.js";
import type { Scope } from "./tools/tiers.js";

// Outbound drafts held for human review surface in the message list with
// this status (the hold vocabulary was unified on `pending_review` across
// both directions — see api/openapi.yaml). There is no dedicated status
// query param for them, so the review queue filters outbound rows on this value.
export const PENDING_REVIEW_STATUS = "pending_review";

const DEFAULT_LIST_LIMIT = 1000;

/** Per-call options for unsafe writes. */
export interface SendOpts {
  idempotencyKey?: string;
}

// ── Templates (beta) wire shapes ──────────────────────────────────
//
// The /v1/templates + /v1/starter-templates surface is beta and not yet
// covered by the hand-written SDK ergonomic layer (only the generated base
// was regenerated), so the MCP client calls it directly over fetch and
// works in the WIRE shape (snake_case), which conveniently matches the MCP
// tool argument style. When the SDK grows a `templates` resource, swap
// these raw calls for it and delete the fetch path.

/** Bearer + base URL for the raw templates path. Mirrors what the wrapped
 *  E2AClient was constructed with (its copies are private). */
export interface RawApiCreds {
  apiKey: string;
  baseUrl?: string;
}

export interface TemplateWire {
  id: string;
  name: string;
  alias?: string;
  subject: string;
  body: string;
  html_body?: string;
  created_at: string;
  updated_at: string;
}

export interface StarterTemplateWire {
  alias: string;
  name: string;
  description: string;
  version: string;
  subject: string;
  variables: Array<{
    name: string;
    required: boolean;
    raw: boolean;
    description: string;
    example: string;
  }>;
  // Present on the detail view (GET /v1/starter-templates/{alias}) only.
  body?: string;
  html_body?: string;
}

export interface CreateTemplateBody {
  name?: string;
  alias?: string;
  subject?: string;
  body?: string;
  html_body?: string;
  from_starter?: string;
}

export interface UpdateTemplateBody {
  name?: string;
  alias?: string;
  subject?: string;
  body?: string;
  html_body?: string;
}

export interface ValidateTemplateBody {
  subject?: string;
  body?: string;
  html_body?: string;
  test_data?: Record<string, unknown>;
}

export interface ValidateTemplateResult {
  valid: boolean;
  errors: Array<{ part: string; message: string }>;
  rendered?: { subject: string; body: string; html_body?: string };
  suggested_data?: Record<string, string>;
}

interface WirePage<T> {
  items: T[];
  next_cursor: string | null;
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
  /**
   * Raw credentials for the beta templates surface (no SDK resource yet —
   * see the "Templates (beta) wire shapes" note above). Optional so stub /
   * test constructions are unchanged; the real construction sites
   * (makeClient, http-server's buildClient) always pass it.
   */
  private readonly rawCreds?: RawApiCreds;

  constructor(sdk: E2AClient, agentEmail = "", scope: Scope = "account", rawCreds?: RawApiCreds) {
    this.sdk = sdk;
    this.agentEmail = agentEmail;
    this.scope = scope;
    this.rawCreds = rawCreds;
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

  // Consume the generated UpdateAgentRequest directly (no hand-declared
  // duplicate, no cast): a field removed/renamed at the API layer is a compile
  // error here. Post-reshape the only mutable agent field is the display name;
  // screening config lives on the protection sub-resource below.
  updateAgent(patch: UpdateAgentRequest, explicitAddress?: string): Promise<AgentView> {
    return this.sdk.agents.update(this.resolveAddress(explicitAddress), patch);
  }

  getProtection(explicitAddress?: string): Promise<ProtectionConfigView> {
    return this.sdk.agents.getProtection(this.resolveAddress(explicitAddress));
  }

  updateProtection(
    config: ProtectionConfigView,
    explicitAddress?: string,
  ): Promise<ProtectionConfigView> {
    return this.sdk.agents.replaceProtection(this.resolveAddress(explicitAddress), config);
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
      // Literal content — required on the wire unless a template reference
      // (templateId XOR templateAlias) is used; the server enforces the
      // mutual exclusivity and returns 400 invalid_request on conflicts.
      subject?: string;
      body?: string;
      htmlBody?: string;
      templateId?: string;
      templateAlias?: string;
      templateData?: Record<string, unknown>;
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

  // getAttachment (§6a #5): metadata + a short-lived download_url for one
  // attachment. inline:true also returns base64 `data` for small attachments.
  getAttachment(
    messageId: string,
    index: number,
    opts: { inline?: boolean } = {},
    explicitAddress?: string,
  ): Promise<AttachmentView> {
    return this.sdk.messages.getAttachment(this.resolveAddress(explicitAddress), messageId, index, opts);
  }

  // Cursor pagination (§6a #3): returns ONE page + next_cursor. `limit` is the
  // page size; pass a prior response's next_cursor as `cursor` for the next page.
  listMessages(params: {
    direction?: "inbound" | "outbound" | "all";
    readStatus?: "unread" | "read" | "all";
    sort?: "asc" | "desc";
    from?: string;
    subjectContains?: string;
    conversationId?: string;
    since?: string;
    until?: string;
    labels?: Array<string>;
    cursor?: string;
    limit?: number;
    explicitAddress?: string;
  }): Promise<Page<MessageSummaryView>> {
    const { explicitAddress, cursor, ...rest } = params;
    return this.sdk.messages.list(this.resolveAddress(explicitAddress), rest).page(cursor);
  }

  // ── Conversations ───────────────────────────────────────────────

  listConversations(
    params: { since?: string; until?: string; cursor?: string; limit?: number },
    explicitAddress?: string,
  ): Promise<Page<ConversationSummaryView>> {
    const { cursor, ...rest } = params;
    return this.sdk.conversations.list(this.resolveAddress(explicitAddress), rest).page(cursor);
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

  // ── Review queue (pending outbound) ─────────────────────────────

  // Pending drafts surface as outbound messages with status=pending_review.
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
        // Held drafts carry the review-hold lifecycle in review_status
        // (read_status is the inbox read-state, "" for outbound). MSG-1.
        if (r.reviewStatus === PENDING_REVIEW_STATUS) out.push(r);
      }
    }
    return out;
  }

  // Resolve the owning agent of a pending OUTBOUND draft by scanning the queue.
  // get_pending_message is a RUNTIME-tier tool (agent-visible), so it must hit
  // the agent-reachable GET /v1/agents/{email}/messages/{id} — NOT the account-
  // only /v1/reviews/{id}, which 403s an agent-scoped credential. For a pinned
  // session this is one list call.
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
    // Runtime-tier (agent-visible): use the agent-reachable messages path, not
    // the account-only /v1/reviews/{id}. Resolve the owning inbox first.
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
    return opts
      ? this.sdk.reviews.approve(messageId, overrides, opts)
      : this.sdk.reviews.approve(messageId, overrides);
  }

  async rejectMessage(messageId: string, reason?: string): Promise<RejectResultView> {
    return this.sdk.reviews.reject(
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

  // ── Templates (beta) ────────────────────────────────────────────
  //
  // Raw-fetch path: the hand-written SDK layer has no `templates` resource
  // yet (only the generated base was regenerated for the beta), so these
  // hit the wire directly and speak snake_case. Errors are mapped to
  // E2AError so runTool surfaces the machine-branchable [code] exactly as
  // for SDK-backed tools. No retry layer — acceptable for the beta; the
  // SDK resource will bring retries when it lands.

  private async rawRequest<T>(method: string, path: string, body?: unknown): Promise<T> {
    if (!this.rawCreds) {
      throw new Error(
        "template operations are unavailable on this connection (no direct API credentials were provided at session construction).",
      );
    }
    const base = (this.rawCreds.baseUrl ?? "https://api.e2a.dev").replace(/\/+$/, "");
    let res: Response;
    try {
      res = await fetch(base + path, {
        method,
        headers: {
          authorization: `Bearer ${this.rawCreds.apiKey}`,
          accept: "application/json",
          ...(body !== undefined ? { "content-type": "application/json" } : {}),
        },
        ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
        signal: AbortSignal.timeout(30_000),
      });
    } catch (e) {
      throw new E2AError({
        code: "connection_error",
        message: e instanceof Error ? e.message : String(e),
        status: 0,
        retryable: true,
        cause: e,
      });
    }
    const text = await res.text();
    let parsed: unknown;
    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      parsed = undefined;
    }
    if (!res.ok) {
      // The /v1 error envelope is { error: { code, message, ... } }.
      const env = (parsed as { error?: { code?: string; message?: string } } | undefined)?.error;
      throw new E2AError({
        code: env?.code || (res.status >= 500 ? "internal_error" : "error"),
        message: env?.message || `e2a API error (${res.status})`,
        status: res.status,
        retryable: res.status === 408 || res.status === 429 || res.status >= 500,
      });
    }
    return parsed as T;
  }

  listTemplates(): Promise<WirePage<TemplateWire>> {
    return this.rawRequest("GET", "/v1/templates");
  }

  getTemplate(id: string): Promise<TemplateWire> {
    return this.rawRequest("GET", `/v1/templates/${encodeURIComponent(id)}`);
  }

  createTemplate(body: CreateTemplateBody): Promise<TemplateWire> {
    return this.rawRequest("POST", "/v1/templates", body);
  }

  updateTemplate(id: string, patch: UpdateTemplateBody): Promise<TemplateWire> {
    return this.rawRequest("PATCH", `/v1/templates/${encodeURIComponent(id)}`, patch);
  }

  async deleteTemplate(id: string): Promise<void> {
    await this.rawRequest<undefined>("DELETE", `/v1/templates/${encodeURIComponent(id)}`);
  }

  validateTemplate(body: ValidateTemplateBody): Promise<ValidateTemplateResult> {
    return this.rawRequest("POST", "/v1/templates/validate", body);
  }

  listStarterTemplates(): Promise<WirePage<StarterTemplateWire>> {
    return this.rawRequest("GET", "/v1/starter-templates");
  }

  getStarterTemplate(alias: string): Promise<StarterTemplateWire> {
    return this.rawRequest("GET", `/v1/starter-templates/${encodeURIComponent(alias)}`);
  }

  // ── Events ──────────────────────────────────────────────────────

  listEvents(params: {
    type?: string;
    agentId?: string;
    conversationId?: string;
    messageId?: string;
    since?: string;
    until?: string;
    cursor?: string;
    limit?: number;
  }): Promise<Page<EventJSON>> {
    const { cursor, ...rest } = params;
    return this.sdk.events.list(rest).page(cursor);
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
  return new McpClient(sdk, cfg.agentEmail ?? "", "account", {
    apiKey: cfg.apiKey,
    ...(cfg.baseUrl ? { baseUrl: cfg.baseUrl } : {}),
  });
}
