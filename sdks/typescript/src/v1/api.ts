import type { components } from "./generated/types.js";

type Schemas = components["schemas"];

export interface E2AApiOptions {
  /**
   * API key. Falls back to the `E2A_API_KEY` environment variable when
   * omitted. Throws at construction if neither is set.
   */
  apiKey?: string;
  /** Base URL. Defaults to "https://e2a.dev". */
  baseUrl?: string;
  /** Request timeout in ms. Defaults to 30 000. */
  timeout?: number;
}

/**
 * Per-call options for side-effectful sends (sendEmail, replyToMessage).
 *
 * `idempotencyKey` is sent on the `Idempotency-Key` header. The server
 * caches the response keyed by (user, key) and replays it on retry,
 * preventing double-sends when the caller (or its retry layer, or the
 * network) repeats the request. When omitted the SDK generates a
 * fresh UUIDv4 per call — devs get the protection by default. To get
 * real benefit across retries, the *caller* must supply a stable key
 * that survives their retry loop (the per-call default does not).
 */
export interface SendOptions {
  idempotencyKey?: string;
}

function newIdempotencyKey(): string {
  // crypto.randomUUID() is available in Node 19+ and all modern
  // browsers. Falls back to a Math.random hex if absent (deprecated
  // runtimes only) — better than throwing at request time.
  const c: { randomUUID?: () => string } | undefined =
    typeof globalThis !== "undefined" ? (globalThis as { crypto?: { randomUUID?: () => string } }).crypto : undefined;
  if (c?.randomUUID) return c.randomUUID();
  return (
    Date.now().toString(16) +
    "-" +
    Math.random().toString(16).slice(2) +
    Math.random().toString(16).slice(2)
  );
}

function idempotencyHeaders(opts: SendOptions): Record<string, string> {
  return { "Idempotency-Key": opts.idempotencyKey ?? newIdempotencyKey() };
}

/**
 * Read an env var if `process.env` is reachable (Node), else "".
 * Exported so the high-level client can share the same browser-safe
 * lookup for `E2A_AGENT_EMAIL`.
 */
export function envVar(name: string): string {
  if (typeof process !== "undefined" && process.env && process.env[name]) {
    return process.env[name] as string;
  }
  return "";
}

/**
 * Raw typed client for the /api/v1 endpoints.
 *
 * Every method maps 1-to-1 to an API route and returns the JSON response
 * body typed against the OpenAPI-generated schemas.
 */
export class E2AApi {
  readonly baseUrl: string;
  readonly apiKey: string;
  private readonly timeout: number;

  constructor(opts: E2AApiOptions = {}) {
    const apiKey = opts.apiKey || envVar("E2A_API_KEY");
    if (!apiKey) {
      throw new Error(
        "apiKey is required. Pass it to E2AApi() or set E2A_API_KEY in the environment.",
      );
    }
    this.apiKey = apiKey;
    this.baseUrl = (opts.baseUrl ?? "https://e2a.dev").replace(/\/+$/, "");
    this.timeout = opts.timeout ?? 30_000;
  }

  // ── Agents ──────────────────────────────────────────────────────

  async listAgents(): Promise<Schemas["ListAgentsResponse"]> {
    return this.request("GET", "/api/v1/agents");
  }

  async registerAgent(
    body: Schemas["RegisterAgentRequest"],
  ): Promise<Schemas["RegisterAgentResponse"]> {
    return this.request("POST", "/api/v1/agents", body);
  }

  async getAgent(email: string): Promise<Schemas["Agent"]> {
    return this.request("GET", `/api/v1/agents/${encodeURIComponent(email)}`);
  }

  async deleteAgent(email: string): Promise<void> {
    await this.raw("DELETE", `/api/v1/agents/${encodeURIComponent(email)}`);
  }

  /**
   * Update an agent's configuration. Only the fields present in `body`
   * are changed, so callers can PATCH a single setting (for example,
   * toggle HITL on) without re-supplying the others.
   */
  async updateAgent(
    email: string,
    body: Schemas["UpdateAgentRequest"],
  ): Promise<Schemas["Agent"]> {
    return this.request(
      "PUT",
      `/api/v1/agents/${encodeURIComponent(email)}`,
      body,
    );
  }

  /**
   * Send a test email from the platform to the agent's own address.
   * Useful for verifying inbound delivery is wired up correctly.
   * Requires the agent's domain to be verified. If the agent has HITL
   * enabled the response is 202 and the message is held for approval.
   */
  async sendTestEmail(
    email: string,
  ): Promise<{ status?: string; message_id?: string }> {
    return this.request(
      "POST",
      `/api/v1/agents/${encodeURIComponent(email)}/test`,
    );
  }

  // ── Messages ────────────────────────────────────────────────────

  async listMessages(
    email: string,
    opts?: {
      status?: string;
      pageSize?: number;
      token?: string;
      /**
       * Sort by created_at. Defaults server-side to `"desc"` (newest
       * first). Pass `"asc"` for FIFO polling semantics — process the
       * oldest unread message first, drain in arrival order.
       */
      sort?: "asc" | "desc";
      /**
       * Server-side search filters. All optional; substring filters
       * are case-insensitive (Postgres ILIKE) and capped at 200 chars
       * by the server. `since` / `until` accept RFC3339 timestamps
       * (`new Date().toISOString()` works). Filters are encoded into
       * `next_token`, so continuation requests must reuse the same
       * filter values or restart the query.
       */
      from?: string;
      subjectContains?: string;
      conversationId?: string;
      since?: string;
      until?: string;
      /**
       * AND-match filter on labels. Emitted as repeated `labels=X`
       * query params to mirror the server-side parser. Empty / undefined
       * skips the filter entirely.
       */
      labels?: string[];
    },
  ): Promise<Schemas["ListMessagesResponse"]> {
    const params = new URLSearchParams();
    if (opts?.status) params.set("status", opts.status);
    if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
    if (opts?.sort) params.set("sort", opts.sort);
    if (opts?.from) params.set("from", opts.from);
    if (opts?.subjectContains) params.set("subject_contains", opts.subjectContains);
    if (opts?.conversationId) params.set("conversation_id", opts.conversationId);
    if (opts?.since) params.set("since", opts.since);
    if (opts?.until) params.set("until", opts.until);
    if (opts?.labels) {
      for (const l of opts.labels) params.append("labels", l);
    }
    if (opts?.token) params.set("token", opts.token);
    const qs = params.toString();
    const path = `/api/v1/agents/${encodeURIComponent(email)}/messages${qs ? `?${qs}` : ""}`;
    return this.request("GET", path);
  }

  async getMessage(
    email: string,
    messageId: string,
  ): Promise<Schemas["MessageDetail"]> {
    return this.request(
      "GET",
      `/api/v1/agents/${encodeURIComponent(email)}/messages/${encodeURIComponent(messageId)}`,
    );
  }

  async replyToMessage(
    email: string,
    messageId: string,
    body: Schemas["ReplyToMessageRequest"],
    opts: SendOptions = {},
  ): Promise<Schemas["SendEmailResponse"]> {
    return this.request(
      "POST",
      `/api/v1/agents/${encodeURIComponent(email)}/messages/${encodeURIComponent(messageId)}/reply`,
      body,
      { extraHeaders: idempotencyHeaders(opts) },
    );
  }

  async forwardMessage(
    email: string,
    messageId: string,
    body: Schemas["ForwardMessageRequest"],
    opts: SendOptions = {},
  ): Promise<Schemas["SendEmailResponse"]> {
    return this.request(
      "POST",
      `/api/v1/agents/${encodeURIComponent(email)}/messages/${encodeURIComponent(messageId)}/forward`,
      body,
      { extraHeaders: idempotencyHeaders(opts) },
    );
  }

  async updateMessageLabels(
    email: string,
    messageId: string,
    body: Schemas["UpdateMessageRequest"],
  ): Promise<Schemas["UpdateMessageResponse"]> {
    return this.request(
      "PATCH",
      `/api/v1/agents/${encodeURIComponent(email)}/messages/${encodeURIComponent(messageId)}`,
      body,
    );
  }

  // ── Conversations ───────────────────────────────────────────────

  /**
   * List conversations for an agent — one row per non-empty
   * conversation_id, with aggregated counts and the latest message's
   * subject/sender. Sorted by `last_message_at` DESC. The response is
   * hard-capped at 100 entries server-side; pagination is intentionally
   * deferred for slice 1.
   */
  async listConversations(
    email: string,
    opts?: {
      pageSize?: number;
      /** RFC3339; only conversations whose latest message is >= since. */
      since?: string;
      /** RFC3339; only conversations whose latest message is < until. */
      until?: string;
    },
  ): Promise<Schemas["ListConversationsResponse"]> {
    const params = new URLSearchParams();
    if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
    if (opts?.since) params.set("since", opts.since);
    if (opts?.until) params.set("until", opts.until);
    const qs = params.toString();
    const path = `/api/v1/agents/${encodeURIComponent(email)}/conversations${qs ? `?${qs}` : ""}`;
    return this.request("GET", path);
  }

  async getConversation(
    email: string,
    conversationId: string,
  ): Promise<Schemas["ConversationDetail"]> {
    return this.request(
      "GET",
      `/api/v1/agents/${encodeURIComponent(email)}/conversations/${encodeURIComponent(conversationId)}`,
    );
  }

  // ── Domains ─────────────────────────────────────────────────────

  async listDomains(): Promise<Schemas["ListDomainsResponse"]> {
    return this.request("GET", "/api/v1/domains");
  }

  async registerDomain(
    body: Schemas["RegisterDomainRequest"],
  ): Promise<Schemas["Domain"]> {
    return this.request("POST", "/api/v1/domains", body);
  }

  async deleteDomain(domain: string): Promise<void> {
    await this.raw("DELETE", `/api/v1/domains/${encodeURIComponent(domain)}`);
  }

  async verifyDomain(
    domain: string,
  ): Promise<Schemas["VerifyDomainResponse"]> {
    return this.request(
      "POST",
      `/api/v1/domains/${encodeURIComponent(domain)}/verify`,
    );
  }

  // ── Send ────────────────────────────────────────────────────────

  async sendEmail(
    body: Schemas["SendEmailRequest"],
    opts: SendOptions = {},
  ): Promise<Schemas["SendEmailResponse"]> {
    return this.request("POST", "/api/v1/send", body, {
      extraHeaders: idempotencyHeaders(opts),
    });
  }

  // ── HITL (human-in-the-loop approval) ───────────────────────────

  /**
   * List pending-approval messages across every agent owned by the
   * authenticated user. Sorted by soonest-expiring first. Body and
   * attachments are omitted from list rows; use getPendingMessage
   * for the full detail.
   */
  async listPendingMessages(): Promise<Schemas["ListPendingMessagesResponse"]> {
    return this.request(
      "GET",
      "/api/v1/pending",
    );
  }

  /**
   * Fetch the full detail of a held outbound message, including the
   * composed body and attachments while the row is still pending.
   */
  async getPendingMessage(
    messageID: string,
  ): Promise<Schemas["PendingMessageDetail"]> {
    return this.request(
      "GET",
      `/api/v1/messages/${encodeURIComponent(messageID)}`,
    );
  }

  /**
   * Approve a held outbound message. Pass overrides (subject,
   * recipients, body, attachments) to send with edits; omit for
   * approve-as-is. On success the server hands the message to the
   * upstream relay and scrubs body columns.
   *
   * `agentEmail` must be the message's owning agent email —
   * available on the listPendingMessages response (`agent_id`) and
   * on the inbound webhook payload. Mismatching email returns 404
   * (anti-cross-agent guard).
   *
   * Approve fires a real SES send, so it accepts an
   * `idempotencyKey` like sendEmail / replyToMessage. Without one,
   * a transient retry after the first success could double-send the
   * same email. Supply a stable key derived from the review event
   * (e.g. the dashboard click id) to make retries safe; omit to let
   * the SDK generate a fresh UUIDv4 per call (network-layer safety
   * only — does not survive an explicit retry loop).
   */
  async approveMessage(
    agentEmail: string,
    messageID: string,
    overrides: Schemas["ApprovePendingMessageRequest"] = {},
    opts: SendOptions = {},
  ): Promise<Schemas["ApprovePendingMessageResponse"]> {
    return this.request(
      "POST",
      `/api/v1/agents/${encodeURIComponent(agentEmail)}/messages/${encodeURIComponent(messageID)}/approve`,
      overrides,
      { extraHeaders: idempotencyHeaders(opts) },
    );
  }

  /**
   * Reject a held outbound message. The message is not sent; body
   * columns are scrubbed and the optional reason is stored for audit.
   * `agentEmail` requirements match approveMessage.
   */
  async rejectMessage(
    agentEmail: string,
    messageID: string,
    reason?: string,
  ): Promise<Schemas["RejectPendingMessageResponse"]> {
    return this.request(
      "POST",
      `/api/v1/agents/${encodeURIComponent(agentEmail)}/messages/${encodeURIComponent(messageID)}/reject`,
      { reason: reason ?? "" } satisfies Schemas["RejectPendingMessageRequest"],
    );
  }

  // ── Webhooks (top-level webhooks-as-a-resource) ─────────────────

  async listWebhooks(): Promise<Schemas["ListWebhooksResponse"]> {
    return this.request("GET", "/api/v1/webhooks");
  }

  async createWebhook(
    body: Schemas["CreateWebhookRequest"],
  ): Promise<Schemas["WebhookResponse"]> {
    return this.request("POST", "/api/v1/webhooks", body);
  }

  async getWebhook(id: string): Promise<Schemas["WebhookResponse"]> {
    return this.request("GET", `/api/v1/webhooks/${encodeURIComponent(id)}`);
  }

  async updateWebhook(
    id: string,
    body: Schemas["UpdateWebhookRequest"],
  ): Promise<Schemas["WebhookResponse"]> {
    return this.request(
      "PATCH",
      `/api/v1/webhooks/${encodeURIComponent(id)}`,
      body,
    );
  }

  async deleteWebhook(id: string): Promise<void> {
    await this.raw("DELETE", `/api/v1/webhooks/${encodeURIComponent(id)}`);
  }

  async rotateWebhookSecret(
    id: string,
  ): Promise<Schemas["RotateWebhookSecretResponse"]> {
    return this.request(
      "POST",
      `/api/v1/webhooks/${encodeURIComponent(id)}/rotate-secret`,
    );
  }

  async testWebhook(
    id: string,
    body: Schemas["TestWebhookRequest"] = { event: "" },
  ): Promise<Schemas["TestWebhookResponse"]> {
    return this.request(
      "POST",
      `/api/v1/webhooks/${encodeURIComponent(id)}/test`,
      body,
    );
  }

  async listWebhookDeliveries(
    id: string,
    opts?: { limit?: number; status?: "pending" | "delivered" | "failed" },
  ): Promise<Schemas["ListWebhookDeliveriesResponse"]> {
    const params = new URLSearchParams();
    if (opts?.limit) params.set("limit", String(opts.limit));
    if (opts?.status) params.set("status", opts.status);
    const qs = params.toString();
    const path = `/api/v1/webhooks/${encodeURIComponent(id)}/deliveries${qs ? `?${qs}` : ""}`;
    return this.request("GET", path);
  }

  // ── Events (slice 6/7: customer-facing event log + replay) ──────

  /**
   * List webhook events in reverse-chronological order. Each event is a
   * durable record of something the gateway emitted. Use this for
   * reconciliation when a webhook receiver was down.
   */
  async listEvents(opts?: {
    type?: string;
    agentId?: string;
    conversationId?: string;
    messageId?: string;
    since?: string;
    until?: string;
    pageSize?: number;
    token?: string;
  }): Promise<Schemas["ListEventsResponse"]> {
    const params = new URLSearchParams();
    if (opts?.type) params.set("type", opts.type);
    if (opts?.agentId) params.set("agent_id", opts.agentId);
    if (opts?.conversationId) params.set("conversation_id", opts.conversationId);
    if (opts?.messageId) params.set("message_id", opts.messageId);
    if (opts?.since) params.set("since", opts.since);
    if (opts?.until) params.set("until", opts.until);
    if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
    if (opts?.token) params.set("token", opts.token);
    const qs = params.toString();
    return this.request("GET", `/api/v1/events${qs ? `?${qs}` : ""}`);
  }

  /**
   * Fetch a single event by id. Returns 410 Gone if the event is past
   * the 30-day retention boundary.
   */
  async getEvent(id: string): Promise<Schemas["WebhookEvent"]> {
    return this.request("GET", `/api/v1/events/${encodeURIComponent(id)}`);
  }

  /**
   * Replay an event to a webhook. Pass `{ webhookId }` to target one
   * subscriber; omit to fan out to every originally-matched webhook.
   * Reuses the original event id so customer-side dedup discards the
   * replay if already processed — replay is recovery, not re-delivery.
   */
  async redeliverEvent(
    id: string,
    opts?: { webhookId?: string },
  ): Promise<Schemas["RedeliverResponse"]> {
    const body = opts?.webhookId ? { webhook_id: opts.webhookId } : {};
    return this.request("POST", `/api/v1/events/${encodeURIComponent(id)}/redeliver`, body);
  }

  /**
   * Bulk-replay every event a webhook originally matched since `since`
   * (RFC3339). Window capped at 7 days by the server. Idempotent —
   * events with a pending delivery for this webhook are skipped.
   */
  async redeliverWebhookSince(
    webhookId: string,
    since: string,
  ): Promise<Schemas["RedeliverSinceResponse"]> {
    return this.request(
      "POST",
      `/api/v1/webhooks/${encodeURIComponent(webhookId)}/redeliver-since`,
      { since },
    );
  }

  // ── Deployment info ─────────────────────────────────────────────

  /**
   * Fetch deployment-specific configuration (shared domain, public URL)
   * for the deployment this client is pointed at. Unauthenticated.
   */
  async getInfo(): Promise<Schemas["DeploymentInfo"]> {
    return E2AApi.fetchInfo(this.baseUrl, this.timeout);
  }

  /**
   * Fetch deployment info without constructing a full client. Useful in
   * the login/discovery path before an API key is available — CLIs hit
   * this during `e2a login` to populate the rest of their config from a
   * single base URL.
   */
  static async fetchInfo(
    baseUrl: string,
    timeoutMs = 30_000,
  ): Promise<Schemas["DeploymentInfo"]> {
    const url = baseUrl.replace(/\/+$/, "");
    const resp = await fetch(`${url}/api/v1/info`, {
      signal: AbortSignal.timeout(timeoutMs),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => `HTTP ${resp.status}`);
      throw new E2AApiError(resp.status, text.trim());
    }
    return resp.json() as Promise<Schemas["DeploymentInfo"]>;
  }

  // ── Internal ────────────────────────────────────────────────────

  /** Low-level fetch that returns the parsed JSON body. */
  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
    opts: { extraHeaders?: Record<string, string> } = {},
  ): Promise<T> {
    const resp = await this.raw(method, path, body, opts);
    return resp.json() as Promise<T>;
  }

  /** Lowest-level fetch — returns the raw Response. */
  async raw(
    method: string,
    path: string,
    body?: unknown,
    opts: { extraHeaders?: Record<string, string> } = {},
  ): Promise<Response> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.apiKey}`,
      ...(opts.extraHeaders ?? {}),
    };
    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
    }
    const resp = await fetch(`${this.baseUrl}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(this.timeout),
    });
    if (!resp.ok) {
      const text = await resp.text().catch(() => `HTTP ${resp.status}`);
      throw new E2AApiError(resp.status, text.trim());
    }
    return resp;
  }
}

export class E2AApiError extends Error {
  readonly statusCode: number;

  constructor(statusCode: number, message: string) {
    super(`e2a API error (${statusCode}): ${message}`);
    this.statusCode = statusCode;
    this.name = "E2AApiError";
  }
}
