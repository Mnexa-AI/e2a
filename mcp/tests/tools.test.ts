import { describe, expect, it, beforeEach, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { E2AConnectionError, E2AError } from "@e2a/sdk/v1";
import type { McpClient } from "../src/client.js";
import { buildServer } from "../src/server.js";
import { ADMIN_TOOLS, assertToolTiersComplete, toolNamesForScope, RUNTIME_TOOLS } from "../src/tools/tiers.js";
import { registerMessageTools } from "../src/tools/messages.js";
import { registerAgentTools } from "../src/tools/agents.js";
import { registerDomainTools } from "../src/tools/domains.js";
import { registerReviewTools } from "../src/tools/review.js";
import { registerWebhookTools } from "../src/tools/webhooks.js";
import { registerEventTools } from "../src/tools/events.js";
import { registerTemplateTools } from "../src/tools/templates.js";
import { registerApiKeyTools } from "../src/tools/apikeys.js";
import { CodedError, runTool } from "../src/tools/util.js";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";

// Build a small RFC822 blob with one attachment so the MessageView's
// `rawMessage` decodes to a known attachment set (the v1 MessageView no
// longer carries decoded attachments — the tools parse rawMessage).
function rawWith(text: string, filename: string, contentType: string, body: Buffer): string {
  const b64 = body.toString("base64");
  const rfc822 = [
    "From: alice@example.com",
    "To: bot@example.com",
    "Subject: hi",
    'Content-Type: multipart/mixed; boundary="BNDRY"',
    "",
    "--BNDRY",
    "Content-Type: text/plain",
    "",
    text,
    "--BNDRY",
    `Content-Type: ${contentType}`,
    "Content-Transfer-Encoding: base64",
    `Content-Disposition: attachment; filename="${filename}"`,
    "",
    b64,
    "--BNDRY--",
    "",
  ].join("\r\n");
  // The server base64-encodes raw_message on the wire; the fixture must match
  // so the tool's decode path is exercised (a plaintext blob would hide it).
  return Buffer.from(rfc822, "utf8").toString("base64");
}

const pdfBytes = Buffer.from("%PDF-1.4 fake pdf bytes");

// Minimal stub of McpClient — only the methods our tools call. The
// wrapper concentrates SDK calls and address resolution, so tests stub
// it directly rather than the namespaced SDK underneath.
function makeStubClient(
  overrides: Partial<{ agentEmail: string; scope: "account" | "agent" }> = {},
): McpClient {
  const stub = {
    agentEmail: overrides.agentEmail ?? "bot@example.com",
    // scope drives §6a tier-gating in buildServer. Default to account (full
    // surface) so behavior tests see every tool; gating tests pass "agent".
    scope: overrides.scope ?? "account",
    send: vi.fn(async () => ({ messageId: "msg_sent", status: "sent" })),
    reply: vi.fn(async () => ({ messageId: "msg_reply", status: "sent" })),
    forward: vi.fn(async () => ({ messageId: "msg_fwd", status: "sent" })),
    updateMessageLabels: vi.fn(async () => ({ messageId: "msg_in", labels: ["urgent"] })),
    // Cursor-paginated lists return a Page { items, next_cursor }.
    listConversations: vi.fn(async () => ({ items: [{ conversationId: "conv_1" }], next_cursor: undefined })),
    getConversation: vi.fn(async () => ({ conversationId: "conv_1", messages: [] })),
    listMessages: vi.fn(async () => ({ items: [], next_cursor: undefined })),
    listAgents: vi.fn(async () => ({ items: [{ email: "bot@example.com" }], next_cursor: undefined })),
    restoreAgent: vi.fn(async (addr?: string) => ({ email: addr ?? "bot@example.com" })),
    listAllAgents: vi.fn(async () => [{ email: "bot@example.com" }]),
    // whoami → client.whoami() returns an AccountView (the authenticated
    // account identity), NOT an agent record. No default-agent resolution.
    whoami: vi.fn(async () => ({
      user: "owner@example.com",
      scope: "account",
      agentAddress: undefined,
      plan: "pro",
      limits: { messagesPerDay: 1000 },
    })),
    // create_agent now takes { email, name? } and returns the full AgentView.
    createAgent: vi.fn(async (body: { email: string; name?: string }) => ({
      id: body.email,
      email: body.email,
      ...(body.name !== undefined ? { name: body.name } : {}),
      domain: body.email.split("@")[1],
    })),
    getAgent: vi.fn(async (email: string) => ({
      id: email,
      email,
      hitlEnabled: false,
    })),
    updateAgent: vi.fn(async (body: Record<string, unknown>) => ({
      id: "bot@example.com",
      email: "bot@example.com",
      ...body,
    })),
    getProtection: vi.fn(async (_addr?: string) => ({
      inbound: { gate: { policy: "open", allowlist: [], action: "flag" }, scan: { sensitivity: "off" } },
      outbound: { gate: { policy: "open", allowlist: [], action: "flag" }, scan: { sensitivity: "off" } },
      holds: { ttlSeconds: 604800, onExpiry: "reject", suppressNotifications: false },
    })),
    updateProtection: vi.fn(async (config: unknown, _addr?: string) => config),
    deleteAgent: vi.fn(async (addr?: string) => ({ deleted: true, email: addr ?? "bot@example.com", messagesDeleted: 0 })),
    listDomains: vi.fn(async () => ({
      items: [{ domain: "mail.acme.com", verified: true, verificationToken: "tok1" }],
      next_cursor: undefined,
    })),
    registerDomain: vi.fn(async (domain: string) => ({
      domain,
      verified: false,
      verificationToken: "tok_new",
      dnsRecords: {
        mx: { host: domain, value: "mx.e2a.dev", priority: 10 },
        txt: { host: domain, value: "e2a-verify=tok_new" },
      },
    })),
    verifyDomain: vi.fn(async (domain: string) => ({
      domain,
      verified: true,
      verificationToken: "tok_new",
    })),
    getDomain: vi.fn(async (domain: string) => ({
      domain,
      verified: true,
      sendingStatus: "verified",
    })),
    deleteDomain: vi.fn(async (domain: string) => ({ deleted: true, domain })),
    deleteWebhook: vi.fn(async (id: string) => ({ deleted: true, id })),
    listWebhookDeliveries: vi.fn(
      async (id: string, _params: { status?: string; cursor?: string; limit?: number }) => ({
        items: [{ id: "whd_1", webhookId: id, status: "delivered", attempts: 1 }],
        next_cursor: undefined,
      }),
    ),
    // Stand-in for McpClient.getMessage() which returns a v1
    // MessageView. Attachments are decoded by the tool from
    // `rawMessage`; the default raw carries one small PDF.
    getMessage: vi.fn(async (id: string, _addr?: string) => ({
      id,
      conversationId: "conv_x",
      from_: "alice@example.com",
      deliveredTo: "bot@example.com",
      to: ["bot@example.com"],
      cc: [],
      replyTo: [],
      subject: "hi",
      readStatus: "read",
      // Inbound messages carry decoded text in `parsed`, NOT `body` (the server
      // only sets `body` for outbound held drafts). Match the real wire shape.
      parsed: { text: "hello world" },
      body: undefined,
      createdAt: "2026-05-20T10:00:00Z",
      rawMessage: rawWith("hello world", "report.pdf", "application/pdf", pdfBytes),
      // Server-authoritative attachment metadata (MessageView.attachments).
      attachments: [
        { index: 0, filename: "report.pdf", contentType: "application/pdf", sizeBytes: 23 },
      ],
    })),
    restoreMessage: vi.fn(async (id: string, _addr?: string) => ({ id })),
    getAttachment: vi.fn(async (id: string, index: number, opts?: { inline?: boolean }) => ({
      index,
      filename: "report.pdf",
      contentType: "application/pdf",
      sizeBytes: 23,
      downloadUrl: `https://api.test/v1/agents/bot@example.com/messages/${id}/attachments/${index}/download?token=tok`,
      expiresAt: "2026-05-20T10:15:00Z",
      ...(opts?.inline ? { data: Buffer.from("%PDF-1.4 fake pdf bytes").toString("base64") } : {}),
    })),
    listReviews: vi.fn(async () => []),
    getReview: vi.fn(async (id: string) => ({
      messageId: id,
      reviewStatus: "pending_review",
    })),
    approveReview: vi.fn(async () => ({ messageId: "msg_x", status: "sent" })),
    rejectReview: vi.fn(async () => ({ messageId: "msg_x", status: "rejected" })),
    // Templates (beta) — SDK-backed: list methods return a Page { items,
    // next_cursor } (cursor-paginated) and rows are camelCase SDK views, like
    // every other tool.
    listTemplates: vi.fn(async () => ({
      items: [
        {
          id: "tmpl_1",
          name: "Welcome",
          alias: "welcome",
          subject: "Welcome, {{name}}!",
          createdAt: "2026-06-01T00:00:00Z",
          updatedAt: "2026-06-01T00:00:00Z",
        },
      ],
      next_cursor: undefined,
    })),
    getTemplate: vi.fn(async (id: string) => ({
      id,
      name: "Welcome",
      subject: "Welcome, {{name}}!",
      text: "Hi {{name}}",
      createdAt: "2026-06-01T00:00:00Z",
      updatedAt: "2026-06-01T00:00:00Z",
    })),
    createTemplate: vi.fn(async (body: Record<string, unknown>) => ({
      id: "tmpl_new",
      name: body.name ?? "Starter name",
      ...body,
      createdAt: "2026-06-01T00:00:00Z",
      updatedAt: "2026-06-01T00:00:00Z",
    })),
    updateTemplate: vi.fn(async (id: string, patch: Record<string, unknown>) => ({
      id,
      name: "Welcome",
      subject: "Welcome, {{name}}!",
      text: "Hi {{name}}",
      ...patch,
      createdAt: "2026-06-01T00:00:00Z",
      updatedAt: "2026-06-02T00:00:00Z",
    })),
    deleteTemplate: vi.fn(async (id: string) => ({ deleted: true, id })),
    validateTemplate: vi.fn(async () => ({
      valid: true,
      errors: [],
      rendered: { subject: "Welcome, Ada!", text: "Hi Ada" },
      suggestedData: { name: "Ada" },
    })),
    listStarterTemplates: vi.fn(async () => ({
      items: [
        {
          alias: "approval-request",
          name: "Approval request",
          description: "Ask a human to approve an action.",
          version: "1",
          subject: "Approval needed: {{action}}",
          variables: [
            { name: "approve_url", required: true, raw: false, description: "Confirmation-page URL", example: "https://x/approve" },
          ],
        },
      ],
      next_cursor: undefined,
    })),
    // API keys — list is metadata-only; create is the wrapper's agent-scoped
    // minter (scope hardwired inside McpClient.createAgentApiKey, so the stub
    // signature has no scope param, mirroring the real wrapper).
    listApiKeys: vi.fn(async () => ({
      items: [
        {
          id: "key_1",
          name: "prod bot",
          keyPrefix: "e2a_agt_abc1",
          scope: "agent",
          agentEmail: "bot@example.com",
          createdAt: "2026-06-01T00:00:00Z",
        },
      ],
      next_cursor: undefined,
    })),
    createAgentApiKey: vi.fn(async (body: { agentEmail: string; name?: string; expiresAt?: Date }) => ({
      id: "key_new",
      name: body.name ?? "",
      keyPrefix: "e2a_agt_new1",
      scope: "agent",
      agentEmail: body.agentEmail,
      createdAt: "2026-06-01T00:00:00Z",
      ...(body.expiresAt ? { expiresAt: body.expiresAt.toISOString() } : {}),
      key: "e2a_agt_new1_PLAINTEXT_ONCE",
    })),
    deleteApiKey: vi.fn(async (id: string) => ({ deleted: true, id })),
    getStarterTemplate: vi.fn(async (alias: string) => ({
      alias,
      name: "Approval request",
      description: "Ask a human to approve an action.",
      version: "1",
      subject: "Approval needed: {{action}}",
      text: "Approve: {{approve_url}}",
      html: "<a href=\"{{approve_url}}\">Approve</a>",
      variables: [
        { name: "approve_url", required: true, raw: false, description: "Confirmation-page URL", example: "https://x/approve" },
      ],
    })),
  };
  return stub as unknown as McpClient;
}

async function connect(stub: McpClient): Promise<Client> {
  const server = buildServer({ client: stub, version: "0.0.0-test" });
  const client = new Client({ name: "test-client", version: "0.0.0" });
  const [clientT, serverT] = InMemoryTransport.createLinkedPair();
  await Promise.all([server.connect(serverT), client.connect(clientT)]);
  return client;
}

describe("e2a MCP server", () => {
  let stub: McpClient;
  let client: Client;

  beforeEach(async () => {
    stub = makeStubClient();
    client = await connect(stub);
  });

  it("registers exactly the v1 tool set", async () => {
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name).sort();
    expect(names).toEqual(
      [
        "send_message",
        "reply_to_message",
        "forward_message",
        "update_message_labels",
        "list_conversations",
        "get_conversation",
        "list_messages",
        "get_message",
        "get_attachment",
        "restore_message",
        "list_agents",
        "get_agent",
        "whoami",
        "create_agent",
        "update_agent",
        "delete_agent",
        "restore_agent",
        "get_protection",
        "update_protection",
        "list_domains",
        "register_domain",
        "verify_domain",
        "get_domain",
        "delete_domain",
        "list_reviews",
        "get_review",
        "approve_review",
        "reject_review",
        "list_webhooks",
        "get_webhook",
        "create_webhook",
        "update_webhook",
        "delete_webhook",
        "rotate_webhook_secret",
        "test_webhook",
        "list_webhook_deliveries",
        "list_events",
        "get_event",
        "redeliver_event",
        "list_templates",
        "get_template",
        "create_template",
        "update_template",
        "delete_template",
        "validate_template",
        "list_starter_templates",
        "get_starter_template",
        "list_api_keys",
        "create_api_key",
        "delete_api_key",
      ].sort(),
    );
  });

  it("documents the fail-closed configuration for reviewing every outbound message", async () => {
    const { tools } = await client.listTools();
    const tool = tools.find((candidate) => candidate.name === "update_protection");
    const description = tool?.description ?? "";
    const properties = (tool?.inputSchema as {
      properties?: Record<string, { description?: string }>;
    })?.properties ?? {};

    expect(description).toContain("outbound_gate_policy=allowlist");
    expect(description).toContain("outbound_gate_allowlist=[]");
    expect(description).toContain("outbound_gate_action=review");
    // The hold-nothing claim is scoped to the recipient GATE — the content scan
    // (when enabled) can still independently hold or block (screenOutbound runs
    // both and applies the more severe action).
    expect(description).toMatch(/open.*review.*gate will hold nothing/i);
    expect(description).toMatch(/content scanning.*hold or block/i);
    // Fail-closed recipe: the gate guarantees review; the scan's block threshold
    // refuses outright (blocked, not held).
    expect(description).toMatch(/blocked, not held/i);
    expect(properties.outbound_gate_policy?.description).toMatch(/open.*every recipient/i);
    expect(properties.holds_on_expiry?.description).toMatch(/reject.*explicit human approval/i);
  });

  // The real backend status vocabulary (internal/httpapi/outbound.go
  // SendResultView) includes "accepted" — the async-outbound success status
  // that REPLACES "sent" for queue-first delivery. A model that doesn't know
  // "accepted" is a terminal success can mistake it for an ambiguous/failed
  // result and re-send without reusing idempotency_key, causing a real
  // duplicate send. Guard that all three send-shaped tool descriptions
  // document it as success and tell the model not to retry.
  it("documents `accepted` as a terminal success status on send/reply/forward (no-retry guard)", async () => {
    const { tools } = await client.listTools();
    const byName = new Map(tools.map((t) => [t.name, t]));
    for (const name of ["send_message", "reply_to_message", "forward_message"]) {
      const description = byName.get(name)?.description ?? "";
      expect(description, `${name} description`).toContain("accepted");
      expect(description, `${name} description`).toMatch(/do NOT re-send/i);
      expect(description, `${name} description`).toMatch(/pending_review/);
    }
  });

  // ── §6a scope/tier gating ──────────────────────────────────────────
  // account scope sees the full surface; agent scope sees only the runtime tier.

  it("every registered tool has exactly one tier (drift guard)", () => {
    // Collect the TRUE registered set by running the register*Tools functions
    // against a name-recording fake server — BEFORE gating, so an untiered tool
    // (which the gate would otherwise silently hide) is still caught.
    const names: string[] = [];
    const recorder = {
      registerTool: (name: string) => {
        names.push(name);
        return undefined;
      },
    } as unknown as McpServer;
    const stub = makeStubClient();
    registerMessageTools(recorder, stub);
    registerAgentTools(recorder, stub);
    registerDomainTools(recorder, stub);
    registerReviewTools(recorder, stub);
    registerWebhookTools(recorder, stub);
    registerEventTools(recorder, stub);
    registerTemplateTools(recorder, stub);
    registerApiKeyTools(recorder, stub);

    expect(names).toHaveLength(50);
    // Throws if any registered tool is untiered / double-tiered / phantom.
    expect(() => assertToolTiersComplete(names)).not.toThrow();
  });

  it("unrecognized scope falls back to the runtime tier (least privilege)", () => {
    expect(toolNamesForScope("bogus")).toBe(RUNTIME_TOOLS);
    expect(toolNamesForScope("")).toBe(RUNTIME_TOOLS);
    expect(toolNamesForScope("agent")).toBe(RUNTIME_TOOLS);
    expect(RUNTIME_TOOLS.size).toBe(14);
    expect(ADMIN_TOOLS.size).toBe(36);
    expect(toolNamesForScope("account").size).toBe(50);
  });

  it("account scope exposes all 50 tools (runtime + admin)", async () => {
    const acct = await connect(makeStubClient({ scope: "account" }));
    const { tools } = await acct.listTools();
    expect(tools).toHaveLength(50);
  });

  it("agent scope exposes only the 14 runtime tools — admin tools hidden", async () => {
    const ag = await connect(makeStubClient({ scope: "agent" }));
    const names = new Set((await ag.listTools()).tools.map((t) => t.name));
    expect(names.size).toBe(14);
    // Runtime tools present (an agent can send + read its own pending queue,
    // but NOT approve/reject — that's an account-owner action, see below):
    for (const n of [
      "whoami", "get_agent", "list_messages", "get_message",
      "get_attachment", "update_message_labels", "list_conversations",
      "get_conversation", "send_message", "reply_to_message", "forward_message",
      "list_reviews", "get_review", "restore_message",
    ]) {
      expect(names.has(n), `runtime tool ${n} should be visible to agent scope`).toBe(true);
    }
    // Admin tools hidden — incl. approve/reject: self-approval would defeat HITL.
    for (const n of [
      "list_agents", "create_agent", "update_agent", "delete_agent", "restore_agent",
      "get_protection", "update_protection",
      "approve_review", "reject_review",
      "list_domains", "get_domain", "register_domain", "verify_domain", "delete_domain",
      "list_webhooks", "get_webhook", "create_webhook", "update_webhook",
      "delete_webhook", "rotate_webhook_secret", "test_webhook", "list_webhook_deliveries",
      "list_events", "get_event", "redeliver_event",
      // Templates (beta) are account-scope end to end (requireAccountUser).
      "list_templates", "get_template", "create_template", "update_template",
      "delete_template", "validate_template", "list_starter_templates", "get_starter_template",
      // API keys: credential management is never an agent-scope capability.
      "list_api_keys", "create_api_key", "delete_api_key",
    ]) {
      expect(names.has(n), `admin tool ${n} must be hidden from agent scope`).toBe(false);
    }
  });

  it("agent scope cannot call a hidden admin tool (errors + handler never runs)", async () => {
    const agentStub = makeStubClient({ scope: "agent" });
    const ag = await connect(agentStub);
    let errored = false;
    try {
      const r = await ag.callTool({ name: "create_agent", arguments: { email: "x@y.dev" } });
      errored = (r as { isError?: boolean })?.isError === true;
    } catch {
      errored = true; // unknown-tool protocol error
    }
    expect(errored, "calling a hidden admin tool must error").toBe(true);
    // The wrapper method must never have been reached — hidden means uncallable,
    // not merely unlisted.
    expect((agentStub.createAgent as unknown as { mock: { calls: unknown[] } }).mock.calls)
      .toHaveLength(0);
  });

  // ── §6a tool annotations (#2) ───────────────────────────────────────

  it("every tool carries MCP annotations with the correct hints", async () => {
    const { tools } = await client.listTools(); // account scope → all 50
    const byName = new Map(tools.map((t) => [t.name, t.annotations ?? {}]));

    // Every tool has an annotations object.
    for (const t of tools) {
      expect(t.annotations, `${t.name} should carry annotations`).toBeDefined();
    }

    // Reads → readOnlyHint.
    for (const n of ["list_messages", "get_message", "whoami", "list_domains", "get_event", "list_webhook_deliveries", "list_templates", "get_template", "validate_template", "list_starter_templates", "get_starter_template", "list_api_keys"]) {
      expect(byName.get(n)?.readOnlyHint, `${n} readOnlyHint`).toBe(true);
    }
    // Deletes → destructive + idempotent.
    for (const n of ["delete_agent", "delete_domain", "delete_webhook", "delete_template", "delete_api_key"]) {
      expect(byName.get(n)?.destructiveHint, `${n} destructiveHint`).toBe(true);
      expect(byName.get(n)?.idempotentHint, `${n} idempotentHint`).toBe(true);
    }
    // Idempotent non-destructive updates.
    for (const n of ["update_agent", "update_webhook", "update_message_labels", "verify_domain", "register_domain", "update_template"]) {
      expect(byName.get(n)?.idempotentHint, `${n} idempotentHint`).toBe(true);
      expect(byName.get(n)?.destructiveHint, `${n} destructiveHint`).toBe(false);
    }
    // Non-destructive writes (create/send) are explicitly non-destructive,
    // and NOT read-only.
    for (const n of ["create_agent", "send_message", "approve_review", "create_webhook", "create_template", "create_api_key"]) {
      expect(byName.get(n)?.destructiveHint, `${n} destructiveHint`).toBe(false);
      expect(byName.get(n)?.readOnlyHint ?? false, `${n} not read-only`).toBe(false);
    }
  });

  it("send_message forwards args to client.send", async () => {
    await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "hi",
        text: "hello",
        cc: ["bob@example.com"],
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      { to: ["alice@example.com"], subject: "hi", text: "hello", cc: ["bob@example.com"] },
      {},
      undefined,
    );
  });

  it("reply_to_message forwards args to client.reply", async () => {
    await client.callTool({
      name: "reply_to_message",
      arguments: {
        message_id: "msg_in",
        text: "thanks",
        reply_all: true,
      },
    });
    expect(stub.reply).toHaveBeenCalledWith(
      "msg_in",
      { text: "thanks", replyAll: true },
      {},
      undefined,
    );
  });

  it("forward_message forwards args to client.forward", async () => {
    await client.callTool({
      name: "forward_message",
      arguments: {
        message_id: "msg_in",
        to: ["destination@example.com"],
        text: "FYI",
      },
    });
    expect(stub.forward).toHaveBeenCalledWith(
      "msg_in",
      ["destination@example.com"],
      { text: "FYI" },
      {},
      undefined,
    );
  });

  it("update_message_labels forwards args to client.updateMessageLabels", async () => {
    await client.callTool({
      name: "update_message_labels",
      arguments: {
        message_id: "msg_in",
        add_labels: ["urgent"],
        remove_labels: ["unread"],
      },
    });
    expect(stub.updateMessageLabels).toHaveBeenCalledWith(
      "msg_in",
      { addLabels: ["urgent"], removeLabels: ["unread"] },
      undefined,
    );
  });

  it("list_conversations surfaces next_cursor when more pages remain", async () => {
    (stub.listConversations as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      items: [{ conversationId: "conv_1" }],
      next_cursor: "c_next",
    });
    const res = await client.callTool({ name: "list_conversations", arguments: {} });
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.conversations).toEqual([{ conversation_id: "conv_1" }]);
    expect(payload.next_cursor).toBe("c_next");
  });

  it("list_conversations forwards cursor/limit + filters to client.listConversations", async () => {
    await client.callTool({
      name: "list_conversations",
      arguments: { limit: 20, cursor: "c_prev", since: "2026-05-01T00:00:00Z" },
    });
    expect(stub.listConversations).toHaveBeenCalledWith(
      { limit: 20, cursor: "c_prev", since: "2026-05-01T00:00:00Z" },
      undefined,
    );
  });

  it("get_conversation forwards args to client.getConversation", async () => {
    await client.callTool({
      name: "get_conversation",
      arguments: { conversation_id: "conv_1" },
    });
    expect(stub.getConversation).toHaveBeenCalledWith("conv_1", undefined);
  });

  it("list_messages forwards filters + cursor/limit", async () => {
    await client.callTool({
      name: "list_messages",
      arguments: {
        read_status: "unread",
        from_: "alice@example.com",
        limit: 10,
        cursor: "c_prev",
      },
    });
    expect(stub.listMessages).toHaveBeenCalledWith({
      readStatus: "unread",
      from_: "alice@example.com",
      limit: 10,
      cursor: "c_prev",
    });
  });

  it("list_messages rejects the removed from spelling", async () => {
    const res = await client.callTool({
      name: "list_messages",
      arguments: { from: "alice@example.com" },
    });

    expect(res.isError).toBe(true);
    expect(stub.listMessages).not.toHaveBeenCalled();
  });

  it("list_messages forwards deleted:true for the trash", async () => {
    await client.callTool({ name: "list_messages", arguments: { deleted: true } });
    expect(stub.listMessages).toHaveBeenCalledWith({ deleted: true });
  });

  it("restore_message restores through the bound agent by default", async () => {
    const res = await client.callTool({ name: "restore_message", arguments: { message_id: "msg_1" } });
    expect(stub.restoreMessage).toHaveBeenCalledWith("msg_1", undefined);
    expect(JSON.parse((res.content as Array<{ text: string }>)[0]!.text).id).toBe("msg_1");
  });

  it("list_messages surfaces next_cursor when more pages remain", async () => {
    (stub.listMessages as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      items: [{ messageId: "m1" }],
      next_cursor: "c_next",
    });
    const res = await client.callTool({ name: "list_messages", arguments: {} });
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.messages).toEqual([{ message_id: "m1" }]);
    expect(payload.next_cursor).toBe("c_next");
  });

  it("list_messages omits next_cursor on the last page", async () => {
    (stub.listMessages as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      items: [{ messageId: "m1" }],
      next_cursor: undefined,
    });
    const res = await client.callTool({ name: "list_messages", arguments: {} });
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload).not.toHaveProperty("next_cursor");
  });

  it("get_message uses the env agent email when omitted and returns parsed shape", async () => {
    const res = await client.callTool({
      name: "get_message",
      arguments: { message_id: "msg_abc" },
    });
    // McpClient.getMessage resolves the address internally; the tool
    // passes the explicit arg (undefined here → pinned default in the
    // wrapper). The MCP server unwraps the MessageView to plain JSON,
    // decoding attachment metadata from rawMessage.
    expect(stub.getMessage).toHaveBeenCalledWith("msg_abc", undefined);
    const content = res.content as Array<{ type: string; text: string }>;
    const parsed = JSON.parse(content[0]!.text) as Record<string, unknown>;
    expect(parsed.id).toBe("msg_abc");
    expect(parsed.from_).toBe("alice@example.com");
    expect(parsed).not.toHaveProperty("from");
    expect(parsed.text).toBe("hello world");
    // Critical: attachments surfaced as metadata-only (no `data`)
    // — bytes blow the LLM's context if returned here. Same reason
    // raw_message is omitted from this response entirely.
    expect(parsed.attachments).toEqual([
      {
        index: 0,
        filename: "report.pdf",
        content_type: "application/pdf",
        size_bytes: 23,
      },
    ]);
    expect(parsed).not.toHaveProperty("raw_message");
    expect((parsed.attachments as Array<{ data?: unknown }>)[0]!.data).toBeUndefined();
  });

  it("get_attachment returns metadata + a download_url (no bytes by default)", async () => {
    const res = await client.callTool({
      name: "get_attachment",
      arguments: { message_id: "msg_abc", attachment_index: 0 },
    });
    // Forwards (message, index, opts, email) to the wrapper.
    expect(stub.getAttachment).toHaveBeenCalledWith("msg_abc", 0, {}, undefined);
    const parsed = JSON.parse((res.content as Array<{ text: string }>)[0]!.text) as Record<string, unknown>;
    expect(parsed.filename).toBe("report.pdf");
    expect(parsed.content_type).toBe("application/pdf");
    expect(parsed.size_bytes).toBe(23);
    expect(parsed.download_url).toContain("/attachments/0/download?token=");
    expect(parsed.expires_at).toBeTruthy();
    expect(parsed).not.toHaveProperty("data"); // bytes by reference, not in context
  });

  it("get_attachment inline:true returns base64 data (for small re-attach/forward)", async () => {
    const res = await client.callTool({
      name: "get_attachment",
      arguments: { message_id: "msg_abc", attachment_index: 0, inline: true },
    });
    expect(stub.getAttachment).toHaveBeenCalledWith("msg_abc", 0, { inline: true }, undefined);
    const parsed = JSON.parse((res.content as Array<{ text: string }>)[0]!.text) as Record<string, unknown>;
    expect(Buffer.from(parsed.data as string, "base64").toString()).toBe("%PDF-1.4 fake pdf bytes");
  });

  it("get_attachment surfaces a server error (e.g. out-of-range/too-large) as isError", async () => {
    // The size cap + index bounds are now SERVER concerns (413/404); the tool
    // forwards and surfaces the structured code.
    (stub.getAttachment as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({ code: "attachment_not_found", message: "no attachment at that index", status: 404, retryable: false }),
    );
    const res = await client.callTool({
      name: "get_attachment",
      arguments: { message_id: "msg_abc", attachment_index: 5 },
    });
    expect(res.isError).toBe(true);
    expect((res.content as Array<{ text: string }>)[0]!.text).toContain("[attachment_not_found]");
  });

  it("whoami calls client.whoami and returns the AccountView", async () => {
    // whoami no longer auto-resolves a 'default' agent. It returns the
    // authenticated account identity (user/scope/agent_address/plan/limits)
    // straight from client.whoami() — NOT an agent record.
    const res = await client.callTool({ name: "whoami", arguments: {} });
    expect(stub.whoami).toHaveBeenCalledOnce();
    const content = res.content as Array<{ type: string; text: string }>;
    const parsed = JSON.parse(content[0]!.text) as Record<string, unknown>;
    expect(parsed.user).toBe("owner@example.com");
    expect(parsed.scope).toBe("account");
    expect(parsed.plan).toBe("pro");
  });

  it("create_agent forwards email only when name omitted", async () => {
    await client.callTool({
      name: "create_agent",
      arguments: { email: "new-bot@agents.example.com" },
    });
    // v1 agent-create takes email + optional name. slug / agent_mode /
    // webhook_url were dropped — only email reaches the SDK here.
    expect(stub.createAgent).toHaveBeenCalledWith({ email: "new-bot@agents.example.com" });
  });

  it("create_agent forwards email and name", async () => {
    await client.callTool({
      name: "create_agent",
      arguments: {
        email: "cloud-bot@agents.example.com",
        name: "Cloud Bot",
      },
    });
    // Both email + name reach the SDK; the returned AgentView is surfaced.
    expect(stub.createAgent).toHaveBeenCalledWith({
      email: "cloud-bot@agents.example.com",
      name: "Cloud Bot",
    });
  });

  it("get_agent forwards email to client.getAgent and surfaces the AgentView", async () => {
    const res = await client.callTool({
      name: "get_agent",
      arguments: { email: "bot@example.com" },
    });
    expect(stub.getAgent).toHaveBeenCalledWith("bot@example.com");
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.email).toBe("bot@example.com");
  });

  it("list_webhook_deliveries forwards id + filters to client.listWebhookDeliveries", async () => {
    const res = await client.callTool({
      name: "list_webhook_deliveries",
      arguments: { id: "wh_abc", status: "failed", limit: 10 },
    });
    expect(stub.listWebhookDeliveries).toHaveBeenCalledWith("wh_abc", {
      status: "failed",
      limit: 10,
    });
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.deliveries[0].webhook_id).toBe("wh_abc");
  });

  it("update_agent sends the name and uses bound agent by default", async () => {
    await client.callTool({
      name: "update_agent",
      arguments: { name: "Renamed Bot" },
    });
    expect(stub.updateAgent).toHaveBeenCalledWith(
      { name: "Renamed Bot" },
      undefined, // no explicit email → wrapper resolves the bound agent
    );
  });

  it("update_agent threads explicit email", async () => {
    await client.callTool({
      name: "update_agent",
      arguments: { email: "other@example.com", name: "Other" },
    });
    expect(stub.updateAgent).toHaveBeenCalledWith(
      { name: "Other" },
      "other@example.com",
    );
  });

  it("update_protection read-modify-writes only the provided fields", async () => {
    await client.callTool({
      name: "update_protection",
      arguments: {
        inbound_scan_sensitivity: "high",
        outbound_gate_policy: "allowlist",
        holds_suppress_notifications: true,
      },
    });
    // Reads current config, then writes back with only the provided fields changed.
    expect(stub.getProtection).toHaveBeenCalled();
    const [cfg, addr] = stub.updateProtection.mock.calls.at(-1)!;
    expect(cfg.inbound.scan.sensitivity).toBe("high");
    expect(cfg.outbound.gate.policy).toBe("allowlist");
    // Untouched sections keep their current value.
    expect(cfg.inbound.gate.policy).toBe("open");
    expect(cfg.holds.onExpiry).toBe("reject");
    expect(cfg.holds.suppressNotifications).toBe(true);
    expect(addr).toBeUndefined();
  });

  it("delete_agent requires confirm:true — server-side schema rejects when omitted", async () => {
    // The Zod schema marks `confirm` as required-literal(true); the MCP
    // server's validator surfaces that as an isError content before any
    // runTool body runs, so deleteAgent must NOT have been called.
    const res = await client.callTool({
      name: "delete_agent",
      arguments: { email: "bot@example.com" },
    });
    expect(res.isError).toBe(true);
    expect(stub.deleteAgent).not.toHaveBeenCalled();
  });

  it("delete_agent forwards on explicit confirm:true", async () => {
    const res = await client.callTool({
      name: "delete_agent",
      arguments: { email: "bot@example.com", confirm: true },
    });
    expect(stub.deleteAgent).toHaveBeenCalledWith("bot@example.com");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(JSON.parse(content[0]!.text)).toEqual({
      deleted: true,
      email: "bot@example.com",
      messages_deleted: 0,
    });
  });

  it("delete_agent passes undefined when email omitted (wrapper resolves bound agent)", async () => {
    await client.callTool({
      name: "delete_agent",
      arguments: { confirm: true },
    });
    expect(stub.deleteAgent).toHaveBeenCalledWith(undefined);
  });

  it("list_agents forwards deleted:true for the trash", async () => {
    await client.callTool({ name: "list_agents", arguments: { deleted: true } });
    expect(stub.listAgents).toHaveBeenCalledWith({ deleted: true });
  });

  it("restore_agent restores the requested trashed agent", async () => {
    const res = await client.callTool({
      name: "restore_agent",
      arguments: { email: "bot@example.com" },
    });
    expect(stub.restoreAgent).toHaveBeenCalledWith("bot@example.com");
    expect(JSON.parse((res.content as Array<{ text: string }>)[0]!.text).email).toBe("bot@example.com");
  });

  // ── Domain tools ────────────────────────────────────────────────

  it("list_domains forwards to client.listDomains", async () => {
    const res = await client.callTool({ name: "list_domains", arguments: {} });
    expect(stub.listDomains).toHaveBeenCalledOnce();
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toContain("mail.acme.com");
  });

  it("register_domain returns the DNS records the user must publish", async () => {
    const res = await client.callTool({
      name: "register_domain",
      arguments: { domain: "mail.acme.com" },
    });
    expect(stub.registerDomain).toHaveBeenCalledWith("mail.acme.com");
    const content = res.content as Array<{ type: string; text: string }>;
    // The returned shape must surface the DNS records so the LLM can
    // hand them to a DNS-provider MCP. If a future SDK change drops
    // them from the response, this test trips immediately.
    expect(content[0]?.text).toContain("dns_records");
    expect(content[0]?.text).toContain("mx.e2a.dev");
    expect(content[0]?.text).toContain("tok_new");
  });

  it("verify_domain forwards the domain and surfaces verified flag", async () => {
    const res = await client.callTool({
      name: "verify_domain",
      arguments: { domain: "mail.acme.com" },
    });
    expect(stub.verifyDomain).toHaveBeenCalledWith("mail.acme.com");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toContain('"verified": true');
  });

  it("get_domain forwards the domain and surfaces sending_status (poll target)", async () => {
    const res = await client.callTool({
      name: "get_domain",
      arguments: { domain: "mail.acme.com" },
    });
    expect(stub.getDomain).toHaveBeenCalledWith("mail.acme.com");
    const content = res.content as Array<{ type: string; text: string }>;
    // get_domain is the sending_status poll target after verify_domain.
    expect(content[0]?.text).toContain("mail.acme.com");
    expect(content[0]?.text).toContain("sending_status");
  });

  it("delete_domain requires confirm:true — schema validator catches the omission", async () => {
    const res = await client.callTool({
      name: "delete_domain",
      arguments: { domain: "mail.acme.com" },
    });
    expect(res.isError).toBe(true);
    expect(stub.deleteDomain).not.toHaveBeenCalled();
  });

  it("delete_domain forwards on explicit confirm:true", async () => {
    const res = await client.callTool({
      name: "delete_domain",
      arguments: { domain: "mail.acme.com", confirm: true },
    });
    expect(stub.deleteDomain).toHaveBeenCalledWith("mail.acme.com");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(JSON.parse(content[0]!.text)).toEqual({ deleted: true, domain: "mail.acme.com" });
  });

  it("delete_webhook returns the REST deletion receipt unchanged", async () => {
    const res = await client.callTool({
      name: "delete_webhook",
      arguments: { id: "wh_1", confirm: true },
    });
    expect(stub.deleteWebhook).toHaveBeenCalledWith("wh_1");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(JSON.parse(content[0]!.text)).toEqual({ deleted: true, id: "wh_1" });
  });

  // ── API keys (admin tier; create is agent-scope-only by construction) ─────

  it("list_api_keys forwards cursor/limit and returns metadata rows", async () => {
    const res = await client.callTool({
      name: "list_api_keys",
      arguments: { cursor: "c1", limit: 10 },
    });
    expect(stub.listApiKeys).toHaveBeenCalledWith({ cursor: "c1", limit: 10 });
    const content = res.content as Array<{ type: string; text: string }>;
    const body = JSON.parse(content[0]!.text) as { api_keys: Array<{ key_prefix: string }>; next_cursor?: string };
    expect(body.api_keys[0]?.key_prefix).toBe("e2a_agt_abc1");
    expect(body.next_cursor).toBeUndefined();
  });

  it("create_api_key mints via createAgentApiKey (scope hardwired, never an input)", async () => {
    const res = await client.callTool({
      name: "create_api_key",
      arguments: {
        agent_email: "bot@example.com",
        name: "ci runner",
        expires_at: "2027-01-01T00:00:00Z",
      },
    });
    expect(stub.createAgentApiKey).toHaveBeenCalledWith({
      agentEmail: "bot@example.com",
      name: "ci runner",
      expiresAt: new Date("2027-01-01T00:00:00Z"),
    });
    // The one-time plaintext key is surfaced in the result.
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/PLAINTEXT_ONCE/);
  });

  it("create_api_key rejects a scope argument — account-scoped keys cannot be requested", async () => {
    const res = await client.callTool({
      name: "create_api_key",
      arguments: { agent_email: "bot@example.com", scope: "account" },
    });
    // strict schema: unknown key `scope` is a validation error, not silently
    // stripped — the ONLY scope this tool can mint is agent (set in the wrapper).
    expect(res.isError).toBe(true);
    expect(stub.createAgentApiKey).not.toHaveBeenCalled();
  });

  it("create_api_key requires agent_email (there is no unbound/account form)", async () => {
    const res = await client.callTool({
      name: "create_api_key",
      arguments: { name: "oops" },
    });
    expect(res.isError).toBe(true);
    expect(stub.createAgentApiKey).not.toHaveBeenCalled();
  });

  it("delete_api_key requires confirm:true — schema validator catches the omission", async () => {
    const res = await client.callTool({
      name: "delete_api_key",
      arguments: { id: "key_1" },
    });
    expect(res.isError).toBe(true);
    expect(stub.deleteApiKey).not.toHaveBeenCalled();
  });

  it("delete_api_key forwards on explicit confirm:true", async () => {
    const res = await client.callTool({
      name: "delete_api_key",
      arguments: { id: "key_1", confirm: true },
    });
    expect(stub.deleteApiKey).toHaveBeenCalledWith("key_1");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(JSON.parse(content[0]!.text)).toEqual({ deleted: true, id: "key_1" });
  });

  it("list_reviews calls the SDK", async () => {
    await client.callTool({ name: "list_reviews", arguments: {} });
    expect(stub.listReviews).toHaveBeenCalledOnce();
  });

  it("get_review forwards the id", async () => {
    await client.callTool({
      name: "get_review",
      arguments: { message_id: "msg_p" },
    });
    expect(stub.getReview).toHaveBeenCalledWith("msg_p");
  });

  it("approve_review strips message_id and maps overrides to camelCase", async () => {
    await client.callTool({
      name: "approve_review",
      arguments: {
        message_id: "msg_p",
        subject: "edited subject",
        text: "edited body",
      },
    });
    // The wrapper resolves the owning agent internally, so the tool no
    // longer passes an address; the tool's text input maps to the
    // ApproveRequest `body` field (aligned with send/reply).
    expect(stub.approveReview).toHaveBeenCalledWith("msg_p", {
      subject: "edited subject",
      text: "edited body",
    });
  });

  it("approve_review approve-as-is sends empty overrides", async () => {
    await client.callTool({
      name: "approve_review",
      arguments: { message_id: "msg_p" },
    });
    expect(stub.approveReview).toHaveBeenCalledWith("msg_p", {});
  });

  // Regression: when idempotency_key is omitted, the MCP layer must
  // call approveReview with exactly TWO args (id, overrides) — not
  // three with `{ idempotencyKey: undefined }`.
  // Passing the undefined object sneaks past TypeScript but a callsite
  // that defaults the key (e.g. an auto-mint helper inside the SDK)
  // would receive `{ idempotencyKey: undefined }` as "user explicitly
  // set this to undefined" rather than "user didn't set this" —
  // different semantics. vitest's toHaveBeenCalledWith does deep-equal
  // on args and is strict on argument count, so this test fails if a
  // 4th arg leaks in. Mirrors the same guard on send / reply tests
  // above (lines 145 / 163).
  it("approve_review omits 3rd-arg opts when idempotency_key is unset", async () => {
    await client.callTool({
      name: "approve_review",
      arguments: { message_id: "msg_p", subject: "edited" },
    });
    expect(stub.approveReview).toHaveBeenCalledWith("msg_p", { subject: "edited" });
    // Two args only — no { idempotencyKey: undefined } leaking in as a
    // 3rd arg (different semantics for an auto-mint helper downstream).
    const lastCall = (stub.approveReview as unknown as { mock: { calls: unknown[][] } }).mock.calls.at(-1);
    expect(lastCall?.length).toBe(2);
  });

  // Approve fires SES so an idempotency_key argument has to reach the
  // SDK or retries can double-send. Strip the key out of overrides
  // (the API doesn't take it in the JSON body) and forward it as the
  // third-arg options object.
  it("approve_review forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "approve_review",
      arguments: {
        message_id: "msg_p",
        subject: "edited",
        idempotency_key: "approve-key-123",
      },
    });
    expect(stub.approveReview).toHaveBeenCalledWith(
      "msg_p",
      { subject: "edited" },
      { idempotencyKey: "approve-key-123" },
    );
  });

  // send_message and reply_to_message also expose idempotency_key. Verify
  // the MCP tool plumbs it through as `idempotencyKey` in SDK opts.
  it("send_message forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "x",
        text: "y",
        idempotency_key: "send-key-9",
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      expect.objectContaining({ to: ["alice@example.com"], subject: "x", text: "y" }),
      { idempotencyKey: "send-key-9" },
      undefined,
    );
  });

  it("reply_to_message forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "reply_to_message",
      arguments: {
        message_id: "msg_in_xyz",
        text: "reply",
        idempotency_key: "reply-key-9",
      },
    });
    expect(stub.reply).toHaveBeenCalledWith(
      "msg_in_xyz",
      expect.objectContaining({ text: "reply" }),
      { idempotencyKey: "reply-key-9" },
      undefined,
    );
  });

  // ── Attachment forwarding (slice A) ─────────────────────────────
  //
  // Wire-shape regression coverage. The Zod schema in
  // src/tools/attachments.ts is the single point of truth; these
  // tests assert it's plumbed into the three outbound tools without
  // dropping or mangling fields.

  // 9-byte payload — round-trip safe and well under any size cap.
  const helloBase64 = Buffer.from("hi-there!").toString("base64");
  const sampleAttachment = {
    filename: "note.txt",
    content_type: "text/plain",
    data: helloBase64,
  };
  // The wire shape after the tool's snake→camel mapping.
  const sdkAttachment = {
    filename: "note.txt",
    contentType: "text/plain",
    data: helloBase64,
  };

  it("send_message maps attachments to the SDK shape on client.send", async () => {
    await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "with file",
        text: "see attached",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      expect.objectContaining({ attachments: [sdkAttachment] }),
      {},
      undefined,
    );
  });

  it("reply_to_message maps attachments to the SDK shape on client.reply", async () => {
    await client.callTool({
      name: "reply_to_message",
      arguments: {
        message_id: "msg_in",
        text: "reply with file",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.reply).toHaveBeenCalledWith(
      "msg_in",
      expect.objectContaining({ text: "reply with file", attachments: [sdkAttachment] }),
      {},
      undefined,
    );
  });

  it("approve_review accepts an attachments override (HITL reviewer adds a file)", async () => {
    await client.callTool({
      name: "approve_review",
      arguments: {
        message_id: "msg_p",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.approveReview).toHaveBeenCalledWith("msg_p", {
      attachments: [sdkAttachment],
    });
  });

  it("approve_review empty attachments:[] is forwarded as a strip override", async () => {
    // Reviewer wants to remove all attachments the agent proposed.
    // Empty array must reach the SDK; if we accidentally filtered it
    // out, the backend would treat the override as absent (keep
    // existing attachments) — wrong behavior.
    await client.callTool({
      name: "approve_review",
      arguments: { message_id: "msg_p", attachments: [] },
    });
    expect(stub.approveReview).toHaveBeenCalledWith("msg_p", { attachments: [] });
  });

  it("send_message rejects base64 with whitespace (URL-safe or LLM-truncated patterns)", async () => {
    const res = await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "bad",
        text: "x",
        attachments: [
          {
            filename: "a.txt",
            content_type: "text/plain",
            // newline-padded base64 — the schema rejects whitespace.
            data: "aGVsbG8=\n",
          },
        ],
      },
    });
    expect(res.isError).toBe(true);
    expect(stub.send).not.toHaveBeenCalled();
  });

  it("send_message rejects base64 with length not divisible by 4 (truncation signal)", async () => {
    const res = await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "bad",
        text: "x",
        attachments: [
          {
            filename: "a.txt",
            content_type: "text/plain",
            data: "aGVsbG", // 6 chars — not %4
          },
        ],
      },
    });
    expect(res.isError).toBe(true);
    expect(stub.send).not.toHaveBeenCalled();
  });

  it("send_message rejects malformed content_type", async () => {
    const res = await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "bad",
        text: "x",
        attachments: [
          {
            filename: "a.txt",
            content_type: "pdf", // no slash
            data: helloBase64,
          },
        ],
      },
    });
    expect(res.isError).toBe(true);
    expect(stub.send).not.toHaveBeenCalled();
  });

  it("reject_review forwards the reason", async () => {
    await client.callTool({
      name: "reject_review",
      arguments: { message_id: "msg_p", reason: "wrong recipient" },
    });
    expect(stub.rejectReview).toHaveBeenCalledWith("msg_p", "wrong recipient");
  });

  it("surfaces SDK errors as isError results", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("HTTP 403: domain not verified"),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/domain not verified/);
  });

  // §6a #4 — surface the API envelope's machine-branchable `code`.
  it("surfaces the structured error code from an E2AError", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "domain_not_verified",
        message: "the sending domain is not verified",
        status: 403,
        retryable: false,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text).toContain("[domain_not_verified]"); // branchable code
    expect(text).toContain("the sending domain is not verified");
    expect(text).not.toContain("(retryable)"); // non-retryable
  });

  it("flags retryable E2AErrors so the agent knows a retry can help", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({ code: "rate_limited", message: "slow down", status: 429, retryable: true }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text).toContain("[rate_limited]");
    expect(text).toContain("(retryable)");
  });

  it("non-E2AError (wrapper) errors stay prose with no bogus code bracket", async () => {
    // e.g. the wrapper's "email is required" — a plain Error, not from the API.
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("email is required"));
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text).toBe("e2a error: email is required");
    expect(text).not.toMatch(/\[.*\]/); // no fabricated code bracket
  });

  it("an E2AError with no code falls through to prose (no empty bracket)", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({ code: "", message: "weird", status: 0, retryable: false }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text).toBe("e2a error: weird");
    expect(text).not.toContain("[]");
  });

  it("sanitizes the message: collapses newlines/control chars (keeps [code] parseable)", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "invalid_recipient",
        message: "bad addr]\n[ignore previous]\tx", // attacker-influenced: newline + forged bracket
        status: 422,
        retryable: false,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    // Exactly one real code bracket (the trusted code); message is single-line.
    expect(text.startsWith("e2a error [invalid_recipient]: ")).toBe(true);
    expect(text).not.toContain("\n");
    expect(text).not.toContain("\t");
  });

  it("truncates an over-long error message", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({ code: "x", message: "a".repeat(5000), status: 500, retryable: false }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text.length).toBeLessThan(600); // bounded, not 5000+
    expect(text).toContain("…");
  });

  // ── structuredContent on tool errors (GA review Tier-2 #12/#31) ─────────
  //
  // Every isError result now ALSO carries a machine-branchable
  // `structuredContent` payload — the sanctioned alternative to regex-parsing
  // the (frozen) `e2a error [code]: msg` text. The text stays byte-for-byte
  // stable; structuredContent is additive.

  it("a typed API error surfaces code/status/request_id/retryable/details in structuredContent", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "domain_not_verified",
        message: "the sending domain is not verified",
        status: 403,
        requestId: "req_abc123",
        details: { domain: "example.com", hint: "verify DNS" },
        retryable: false,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({
      code: "domain_not_verified",
      status: 403,
      request_id: "req_abc123",
      retryable: false,
      details: { domain: "example.com", hint: "verify DNS" },
    });
    // The legacy text form is untouched — agents parsing it keep working.
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text).toBe("e2a error [domain_not_verified]: the sending domain is not verified");
  });

  it("preserves an unknown future API code and detail fields", async () => {
    const details = { future_field: { nested: true } };
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "future_error_code",
        message: "future failure",
        status: 418,
        details,
        retryable: false,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.structuredContent).toEqual({
      code: "future_error_code",
      status: 418,
      retryable: false,
      details,
    });
  });

  it("a retryable API error carries retryable + retry_after_seconds in structuredContent", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "rate_limited",
        message: "slow down",
        status: 429,
        retryable: true,
        retryAfterSeconds: 30,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({
      code: "rate_limited",
      status: 429,
      retryable: true,
      retry_after_seconds: 30,
    });
  });

  it("a wrapper-thrown error carries the stable invalid_request code (no status/request_id)", async () => {
    // Plain Error from the MCP layer itself — no HTTP exchange happened, so
    // structuredContent has no status/request_id, but it still carries a
    // stable code (the server's canonical validation code) instead of none.
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error("email is required"));
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({ code: "invalid_request", retryable: false });
    // Text form unchanged: prose, no fabricated code bracket.
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(text).toBe("e2a error: email is required");
  });

  it("a connection-level failure carries connection_error/retryable/status 0 in structuredContent", async () => {
    // The SDK's connectionError(...) shape: code connection_error, status 0
    // (no HTTP response), retryable true — the documented structured form for
    // "the API was never reached".
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AConnectionError({
        code: "connection_error",
        message: "connection to https://api.e2a.dev failed: fetch failed",
        status: 0,
        retryable: true,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({ code: "connection_error", retryable: true, status: 0 });
  });

  it("a CodedError carries its server-vocabulary code (no status); text stays prose", async () => {
    // ownerOfPending's "pending draft already approved/rejected/expired" is a
    // not-found condition, not malformed input — it must NOT surface as
    // invalid_request (PR #453 review).
    const res = await runTool(async () => {
      throw new CodedError("not_found", "pending message msg_p not found on any owned agent");
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({ code: "not_found", retryable: false });
    expect(res.content[0]?.text).toBe(
      "e2a error: pending message msg_p not found on any owned agent",
    );
  });

  it("the confirm-guard throw carries invalid_request in structuredContent", async () => {
    // The tools' confirm guards (`throw new Error("delete_agent requires
    // confirm:true …")`) sit behind a z.literal(true) schema, so drive runTool
    // directly with the guard-style throw to pin its structured shape.
    const res = await runTool(async () => {
      throw new Error(
        "delete_agent requires confirm:true — refusing to proceed without explicit confirmation.",
      );
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({ code: "invalid_request", retryable: false });
    expect(res.content[0]?.text).toBe(
      "e2a error: delete_agent requires confirm:true — refusing to proceed without explicit confirmation.",
    );
  });

  it("oversized details are omitted from structuredContent (context-blowup guard)", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "invalid_request",
        message: "bad input",
        status: 422,
        details: { blob: "a".repeat(5000) },
        retryable: false,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBe(true);
    expect(res.structuredContent).toEqual({
      code: "invalid_request",
      status: 422,
      retryable: false,
      // no `details` key — its JSON exceeded the cap
    });
  });

  it("success results use REST-style snake_case without structuredContent", async () => {
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", text: "b" },
    });
    expect(res.isError).toBeFalsy();
    expect(res.structuredContent).toBeUndefined();
    const text = (res.content as Array<{ text: string }>)[0]?.text ?? "";
    expect(JSON.parse(text)).toEqual({ message_id: "msg_sent", status: "sent" });
  });

  it("normalizes stable success payload keys recursively while preserving from_", async () => {
    const res = await runTool(async () => ({
      messageId: "msg_1",
      messagesDeleted: 2,
      from_: "alice@example.com",
      deliveryMeta: { createdAt: "2026-07-16T00:00:00Z" },
      attachments: [{ contentType: "text/plain", sizeBytes: 4 }],
    }));

    expect(JSON.parse(res.content[0]!.text)).toEqual({
      message_id: "msg_1",
      messages_deleted: 2,
      from_: "alice@example.com",
      delivery_meta: { created_at: "2026-07-16T00:00:00Z" },
      attachments: [{ content_type: "text/plain", size_bytes: 4 }],
    });
  });

  // ── Templates (beta) ────────────────────────────────────────────
  //
  // The eight template tools are thin pass-throughs over the McpClient's
  // SDK-backed template methods: snake_case tool args (house arg style) map
  // to camelCase SDK request fields, then success results return through the
  // common MCP snake_case boundary. Templates remain explicitly beta; the
  // server enforces the create-mode and send-reference exclusivity rules.

  it("list_templates returns the summary rows", async () => {
    const res = await client.callTool({ name: "list_templates", arguments: {} });
    expect(stub.listTemplates).toHaveBeenCalledOnce();
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.templates[0].id).toBe("tmpl_1");
    expect(payload.templates[0].created_at).toBe("2026-06-01T00:00:00Z");
    expect(payload).not.toHaveProperty("next_cursor");
  });

  it("get_template forwards the id", async () => {
    await client.callTool({ name: "get_template", arguments: { id: "tmpl_1" } });
    expect(stub.getTemplate).toHaveBeenCalledWith("tmpl_1");
  });

  it("create_template maps snake_case args to the camelCase SDK request", async () => {
    await client.callTool({
      name: "create_template",
      arguments: {
        name: "Order shipped",
        alias: "order-shipped",
        subject: "Your order {{order_id}} shipped",
        text: "Hi {{name}}, it shipped.",
        html: "<p>Hi {{name}}</p>",
      },
    });
    expect(stub.createTemplate).toHaveBeenCalledWith({
      name: "Order shipped",
      alias: "order-shipped",
      subject: "Your order {{order_id}} shipped",
      text: "Hi {{name}}, it shipped.",
      html: "<p>Hi {{name}}</p>",
    });
  });

  it("create_template forwards from_starter without fabricating literal fields", async () => {
    await client.callTool({
      name: "create_template",
      arguments: { from_starter: "approval-request", alias: "my-approvals" },
    });
    // Only what the caller passed reaches the wire — no empty subject/body
    // keys that would trip the server's from_starter exclusivity check.
    expect(stub.createTemplate).toHaveBeenCalledWith({
      fromStarter: "approval-request",
      alias: "my-approvals",
    });
  });

  it("update_template splits id from the patch", async () => {
    await client.callTool({
      name: "update_template",
      arguments: { id: "tmpl_1", subject: "New subject {{x}}", html: "" },
    });
    // html: "" is a deliberate clear — it must survive to the wire.
    expect(stub.updateTemplate).toHaveBeenCalledWith("tmpl_1", {
      subject: "New subject {{x}}",
      html: "",
    });
  });

  it("delete_template requires confirm:true — schema validator catches the omission", async () => {
    const res = await client.callTool({
      name: "delete_template",
      arguments: { id: "tmpl_1" },
    });
    expect(res.isError).toBe(true);
    expect(stub.deleteTemplate).not.toHaveBeenCalled();
  });

  it("delete_template forwards on explicit confirm:true", async () => {
    const res = await client.callTool({
      name: "delete_template",
      arguments: { id: "tmpl_1", confirm: true },
    });
    expect(stub.deleteTemplate).toHaveBeenCalledWith("tmpl_1");
    expect(JSON.parse((res.content as Array<{ text: string }>)[0]!.text)).toEqual({
      deleted: true,
      id: "tmpl_1",
    });
  });

  it("delete tool descriptions match REST's reversible-agent and guarded-domain semantics", async () => {
    const { tools } = await client.listTools();
    const byName = new Map(tools.map((tool) => [tool.name, tool.description ?? ""]));

    expect(byName.get("delete_agent")).toContain("trash");
    expect(byName.get("delete_agent")).not.toContain("Permanently delete");
    expect(byName.get("delete_domain")).toContain(
      "permanently delete every live or trashed agent",
    );
    expect(byName.get("delete_domain")).toContain(
      "Moving an agent to trash is not sufficient",
    );
    expect(byName.get("delete_domain")).toContain("trashed agents still belong to the domain");
    expect(byName.get("delete_domain")).not.toContain("move those inboxes");
    expect(byName.get("delete_domain")).not.toContain("CASCADES to every agent");
  });

  it("validate_template forwards source parts + test_data", async () => {
    const res = await client.callTool({
      name: "validate_template",
      arguments: {
        subject: "Welcome, {{name}}!",
        text: "Hi {{name}}",
        test_data: { name: "Ada" },
      },
    });
    expect(stub.validateTemplate).toHaveBeenCalledWith({
      subject: "Welcome, {{name}}!",
      text: "Hi {{name}}",
      testData: { name: "Ada" },
    });
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.valid).toBe(true);
    expect(payload.rendered.subject).toBe("Welcome, Ada!");
    expect(payload.suggested_data).toEqual({ name: "Ada" });
  });

  it("list_starter_templates surfaces the catalog", async () => {
    const res = await client.callTool({ name: "list_starter_templates", arguments: {} });
    expect(stub.listStarterTemplates).toHaveBeenCalledOnce();
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.starter_templates[0].alias).toBe("approval-request");
    expect(payload.starter_templates[0].variables[0].name).toBe("approve_url");
  });

  it("get_starter_template forwards the alias and returns body sources", async () => {
    const res = await client.callTool({
      name: "get_starter_template",
      arguments: { alias: "approval-request" },
    });
    expect(stub.getStarterTemplate).toHaveBeenCalledWith("approval-request");
    const payload = JSON.parse((res.content as Array<{ text: string }>)[0].text);
    expect(payload.text).toContain("{{approve_url}}");
    expect(payload.html).toContain("{{approve_url}}");
  });

  // ── send_message template references (beta) ─────────────────────

  it("send_message forwards template_alias + template_data without literal subject/body", async () => {
    await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        template_alias: "welcome",
        template_data: { name: "Alice", plan: "pro" },
      },
    });
    // Exactly the template reference reaches the SDK — no subject/body keys
    // (even undefined ones) that would trip the server's exclusivity check.
    expect(stub.send).toHaveBeenCalledWith(
      {
        to: ["alice@example.com"],
        templateAlias: "welcome",
        templateData: { name: "Alice", plan: "pro" },
      },
      {},
      undefined,
    );
  });

  it("send_message forwards template_id", async () => {
    await client.callTool({
      name: "send_message",
      arguments: { to: ["alice@example.com"], template_id: "tmpl_1" },
    });
    expect(stub.send).toHaveBeenCalledWith(
      { to: ["alice@example.com"], templateId: "tmpl_1" },
      {},
      undefined,
    );
  });

  it("send_message surfaces the server's template exclusivity error as isError", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new E2AError({
        code: "invalid_request",
        message: "a template reference is mutually exclusive with subject, body and html",
        status: 400,
        retryable: false,
      }),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "literal",
        text: "literal",
        template_alias: "welcome",
      },
    });
    expect(res.isError).toBe(true);
    expect((res.content as Array<{ text: string }>)[0]?.text).toContain("[invalid_request]");
  });
});
