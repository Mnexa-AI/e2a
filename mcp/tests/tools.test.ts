import { describe, expect, it, beforeEach, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { simpleParser } from "mailparser";
import type { McpClient } from "../src/client.js";
import { buildServer } from "../src/server.js";

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
function makeStubClient(overrides: Partial<{ agentEmail: string }> = {}): McpClient {
  const stub = {
    agentEmail: overrides.agentEmail ?? "bot@example.com",
    send: vi.fn(async () => ({ messageId: "msg_sent", status: "sent" })),
    reply: vi.fn(async () => ({ messageId: "msg_reply", status: "sent" })),
    forward: vi.fn(async () => ({ messageId: "msg_fwd", status: "sent" })),
    updateMessageLabels: vi.fn(async () => ({ messageId: "msg_in", labels: ["urgent"] })),
    listConversations: vi.fn(async () => [{ conversationId: "conv_1" }]),
    getConversation: vi.fn(async () => ({ conversationId: "conv_1", messages: [] })),
    listMessages: vi.fn(async () => []),
    listAgents: vi.fn(async () => [{ email: "bot@example.com" }]),
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
    deleteAgent: vi.fn(async (addr?: string) => addr ?? "bot@example.com"),
    listDomains: vi.fn(async () => [
      { domain: "mail.acme.com", verified: true, verificationToken: "tok1" },
    ]),
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
    deleteDomain: vi.fn(async () => undefined),
    listWebhookDeliveries: vi.fn(
      async (id: string, _params: { status?: string; limit?: number }) => [
        { id: "whd_1", webhookId: id, status: "delivered", attempts: 1 },
      ],
    ),
    // Stand-in for McpClient.getMessage() which returns a v1
    // MessageView. Attachments are decoded by the tool from
    // `rawMessage`; the default raw carries one small PDF.
    getMessage: vi.fn(async (id: string, _addr?: string) => ({
      messageId: id,
      conversationId: "conv_x",
      _from: "alice@example.com",
      recipient: "bot@example.com",
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
    })),
    listPendingMessages: vi.fn(async () => []),
    getPendingMessage: vi.fn(async (id: string) => ({
      messageId: id,
      hitlStatus: "pending_approval",
    })),
    approveMessage: vi.fn(async () => ({ messageId: "msg_x", status: "sent" })),
    rejectMessage: vi.fn(async () => ({ messageId: "msg_x", status: "rejected" })),
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
        "list_agents",
        "get_agent",
        "whoami",
        "create_agent",
        "update_agent",
        "delete_agent",
        "list_domains",
        "register_domain",
        "verify_domain",
        "get_domain",
        "delete_domain",
        "list_pending_messages",
        "get_pending_message",
        "approve_message",
        "reject_message",
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
      ].sort(),
    );
  });

  it("send_message forwards args to client.send", async () => {
    await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "hi",
        body: "hello",
        cc: ["bob@example.com"],
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      { to: ["alice@example.com"], subject: "hi", body: "hello", cc: ["bob@example.com"] },
      {},
      undefined,
    );
  });

  it("reply_to_message forwards args to client.reply", async () => {
    await client.callTool({
      name: "reply_to_message",
      arguments: {
        message_id: "msg_in",
        body: "thanks",
        reply_all: true,
      },
    });
    expect(stub.reply).toHaveBeenCalledWith(
      "msg_in",
      { body: "thanks", replyAll: true },
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
        body: "FYI",
      },
    });
    expect(stub.forward).toHaveBeenCalledWith(
      "msg_in",
      ["destination@example.com"],
      { body: "FYI" },
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

  it("list_conversations forwards args to client.listConversations", async () => {
    await client.callTool({
      name: "list_conversations",
      arguments: { page_size: 20, since: "2026-05-01T00:00:00Z" },
    });
    expect(stub.listConversations).toHaveBeenCalledWith(
      { limit: 20, since: "2026-05-01T00:00:00Z" },
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

  it("list_messages forwards filters", async () => {
    await client.callTool({
      name: "list_messages",
      arguments: { read_status: "unread", page_size: 10 },
    });
    expect(stub.listMessages).toHaveBeenCalledWith({
      readStatus: "unread",
      limit: 10,
    });
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
    expect(parsed.message_id).toBe("msg_abc");
    expect(parsed.from).toBe("alice@example.com");
    expect(parsed.body_text).toBe("hello world");
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

  it("get_attachment returns one attachment with base64 data", async () => {
    const res = await client.callTool({
      name: "get_attachment",
      arguments: { message_id: "msg_abc", attachment_index: 0 },
    });
    expect(stub.getMessage).toHaveBeenCalledWith("msg_abc", undefined);
    const content = res.content as Array<{ type: string; text: string }>;
    const parsed = JSON.parse(content[0]!.text) as Record<string, unknown>;
    expect(parsed.filename).toBe("report.pdf");
    expect(parsed.content_type).toBe("application/pdf");
    expect(parsed.size_bytes).toBe(23);
    // Round-trip-decodable base64 of the original bytes.
    expect(Buffer.from(parsed.data as string, "base64").toString()).toBe(
      "%PDF-1.4 fake pdf bytes",
    );
  });

  it("get_attachment rejects out-of-range index with a clear error", async () => {
    const res = await client.callTool({
      name: "get_attachment",
      arguments: { message_id: "msg_abc", attachment_index: 5 },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]!.text).toMatch(/out of range/);
  });

  it("get_attachment refuses attachments above the 2 MB inline cap", async () => {
    // One-shot override: a message whose raw MIME carries a 3 MB
    // attachment (decoded), above the 2 MB inline cap.
    const big = Buffer.alloc(3 * 1024 * 1024, 0x41);
    (stub.getMessage as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      messageId: "msg_big",
      conversationId: null,
      _from: "alice@example.com",
      recipient: "bot@example.com",
      to: ["bot@example.com"],
      cc: [],
      replyTo: [],
      subject: "huge",
      status: "read",
      body: { text: "", html: undefined },
      createdAt: null,
      rawMessage: rawWith("", "big.zip", "application/zip", big),
    });
    const res = await client.callTool({
      name: "get_attachment",
      arguments: { message_id: "msg_big", attachment_index: 0 },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]!.text).toMatch(/too large for inline retrieval/);
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
    expect(payload.deliveries[0].webhookId).toBe("wh_abc");
  });

  it("update_agent maps HITL fields to camelCase and uses bound agent by default", async () => {
    await client.callTool({
      name: "update_agent",
      arguments: { hitl_enabled: true, hitl_ttl_seconds: 3600 },
    });
    expect(stub.updateAgent).toHaveBeenCalledWith(
      { hitlEnabled: true, hitlTtlSeconds: 3600 },
      undefined, // no explicit email → wrapper resolves the bound agent
    );
  });

  it("update_agent maps the new HITL/inbound-policy fields to camelCase", async () => {
    // hitl_mode + inbound_policy + inbound_allowlist were added; verify
    // the snake→camel mapping of each new field.
    await client.callTool({
      name: "update_agent",
      arguments: {
        hitl_mode: "high_impact",
        inbound_policy: "allowlist",
        inbound_allowlist: ["trusted@example.com"],
      },
    });
    expect(stub.updateAgent).toHaveBeenCalledWith(
      {
        hitlMode: "high_impact",
        inboundPolicy: "allowlist",
        inboundAllowlist: ["trusted@example.com"],
      },
      undefined,
    );
  });

  it("update_agent threads explicit email", async () => {
    await client.callTool({
      name: "update_agent",
      arguments: {
        email: "other@example.com",
        hitl_expiration_action: "reject",
      },
    });
    // Only the mapped HITL fields reach the SDK; the address is passed through.
    expect(stub.updateAgent).toHaveBeenCalledWith(
      { hitlExpirationAction: "reject" },
      "other@example.com",
    );
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
    expect(content[0]?.text).toMatch(/bot@example\.com/);
  });

  it("delete_agent passes undefined when email omitted (wrapper resolves bound agent)", async () => {
    await client.callTool({
      name: "delete_agent",
      arguments: { confirm: true },
    });
    expect(stub.deleteAgent).toHaveBeenCalledWith(undefined);
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
    expect(content[0]?.text).toContain("dnsRecords");
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
    expect(content[0]?.text).toContain("sendingStatus");
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
    expect(content[0]?.text).toMatch(/mail\.acme\.com/);
  });

  it("list_pending_messages calls the SDK", async () => {
    await client.callTool({ name: "list_pending_messages", arguments: {} });
    expect(stub.listPendingMessages).toHaveBeenCalledOnce();
  });

  it("get_pending_message forwards the id", async () => {
    await client.callTool({
      name: "get_pending_message",
      arguments: { message_id: "msg_p" },
    });
    expect(stub.getPendingMessage).toHaveBeenCalledWith("msg_p");
  });

  it("approve_message strips message_id and maps overrides to camelCase", async () => {
    await client.callTool({
      name: "approve_message",
      arguments: {
        message_id: "msg_p",
        subject: "edited subject",
        body_text: "edited body",
      },
    });
    // The wrapper resolves the owning agent internally, so the tool no
    // longer passes an address; the tool's body_text input maps to the
    // ApproveRequest `body` field (aligned with send/reply).
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", {
      subject: "edited subject",
      body: "edited body",
    });
  });

  it("approve_message approve-as-is sends empty overrides", async () => {
    await client.callTool({
      name: "approve_message",
      arguments: { message_id: "msg_p" },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", {});
  });

  // Regression: when idempotency_key is omitted, the MCP layer must
  // call approveMessage with exactly THREE args (agentEmail, id,
  // overrides) — not four with `{ idempotencyKey: undefined }`.
  // Passing the undefined object sneaks past TypeScript but a callsite
  // that defaults the key (e.g. an auto-mint helper inside the SDK)
  // would receive `{ idempotencyKey: undefined }` as "user explicitly
  // set this to undefined" rather than "user didn't set this" —
  // different semantics. vitest's toHaveBeenCalledWith does deep-equal
  // on args and is strict on argument count, so this test fails if a
  // 4th arg leaks in. Mirrors the same guard on send / reply tests
  // above (lines 145 / 163).
  it("approve_message omits 3rd-arg opts when idempotency_key is unset", async () => {
    await client.callTool({
      name: "approve_message",
      arguments: { message_id: "msg_p", subject: "edited" },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", { subject: "edited" });
    // Two args only — no { idempotencyKey: undefined } leaking in as a
    // 3rd arg (different semantics for an auto-mint helper downstream).
    const lastCall = (stub.approveMessage as unknown as { mock: { calls: unknown[][] } }).mock.calls.at(-1);
    expect(lastCall?.length).toBe(2);
  });

  // Approve fires SES so an idempotency_key argument has to reach the
  // SDK or retries can double-send. Strip the key out of overrides
  // (the API doesn't take it in the JSON body) and forward it as the
  // third-arg options object.
  it("approve_message forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "approve_message",
      arguments: {
        message_id: "msg_p",
        subject: "edited",
        idempotency_key: "approve-key-123",
      },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith(
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
        body: "y",
        idempotency_key: "send-key-9",
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      expect.objectContaining({ to: ["alice@example.com"], subject: "x", body: "y" }),
      { idempotencyKey: "send-key-9" },
      undefined,
    );
  });

  it("reply_to_message forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "reply_to_message",
      arguments: {
        message_id: "msg_in_xyz",
        body: "reply",
        idempotency_key: "reply-key-9",
      },
    });
    expect(stub.reply).toHaveBeenCalledWith(
      "msg_in_xyz",
      expect.objectContaining({ body: "reply" }),
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
        body: "see attached",
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
        body: "reply with file",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.reply).toHaveBeenCalledWith(
      "msg_in",
      expect.objectContaining({ body: "reply with file", attachments: [sdkAttachment] }),
      {},
      undefined,
    );
  });

  it("approve_message accepts an attachments override (HITL reviewer adds a file)", async () => {
    await client.callTool({
      name: "approve_message",
      arguments: {
        message_id: "msg_p",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", {
      attachments: [sdkAttachment],
    });
  });

  it("approve_message empty attachments:[] is forwarded as a strip override", async () => {
    // Reviewer wants to remove all attachments the agent proposed.
    // Empty array must reach the SDK; if we accidentally filtered it
    // out, the backend would treat the override as absent (keep
    // existing attachments) — wrong behavior.
    await client.callTool({
      name: "approve_message",
      arguments: { message_id: "msg_p", attachments: [] },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", { attachments: [] });
  });

  it("send_message rejects base64 with whitespace (URL-safe or LLM-truncated patterns)", async () => {
    const res = await client.callTool({
      name: "send_message",
      arguments: {
        to: ["alice@example.com"],
        subject: "bad",
        body: "x",
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
        body: "x",
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
        body: "x",
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

  it("reject_message forwards the reason", async () => {
    await client.callTool({
      name: "reject_message",
      arguments: { message_id: "msg_p", reason: "wrong recipient" },
    });
    expect(stub.rejectMessage).toHaveBeenCalledWith("msg_p", "wrong recipient");
  });

  it("surfaces SDK errors as isError results", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("HTTP 403: domain not verified"),
    );
    const res = await client.callTool({
      name: "send_message",
      arguments: { to: ["x@example.com"], subject: "s", body: "b" },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/domain not verified/);
  });
});
