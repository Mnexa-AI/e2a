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
    opts?: { status?: string; pageSize?: number; token?: string },
  ): Promise<Schemas["ListMessagesResponse"]> {
    const params = new URLSearchParams();
    if (opts?.status) params.set("status", opts.status);
    if (opts?.pageSize) params.set("page_size", String(opts.pageSize));
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
  ): Promise<Schemas["SendEmailResponse"]> {
    return this.request(
      "POST",
      `/api/v1/agents/${encodeURIComponent(email)}/messages/${encodeURIComponent(messageId)}/reply`,
      body,
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
  ): Promise<Schemas["SendEmailResponse"]> {
    return this.request("POST", "/api/v1/send", body);
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
      "/api/v1/messages?status=pending_approval",
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
   */
  async approveMessage(
    messageID: string,
    overrides: Schemas["ApprovePendingMessageRequest"] = {},
  ): Promise<Schemas["ApprovePendingMessageResponse"]> {
    return this.request(
      "POST",
      `/api/v1/messages/${encodeURIComponent(messageID)}/approve`,
      overrides,
    );
  }

  /**
   * Reject a held outbound message. The message is not sent; body
   * columns are scrubbed and the optional reason is stored for audit.
   */
  async rejectMessage(
    messageID: string,
    reason?: string,
  ): Promise<Schemas["RejectPendingMessageResponse"]> {
    return this.request(
      "POST",
      `/api/v1/messages/${encodeURIComponent(messageID)}/reject`,
      { reason: reason ?? "" } satisfies Schemas["RejectPendingMessageRequest"],
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
  ): Promise<T> {
    const resp = await this.raw(method, path, body);
    return resp.json() as Promise<T>;
  }

  /** Lowest-level fetch — returns the raw Response. */
  async raw(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<Response> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.apiKey}`,
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
