import { describe, expect, it, beforeEach, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { buildServer } from "../src/server.js";

// Minimal stub of E2AClient — only the methods our tools actually call.
function makeStubClient(overrides: Partial<{ agentEmail: string }> = {}): E2AClient {
  const stub = {
    agentEmail: overrides.agentEmail ?? "bot@example.com",
    api: {
      getMessage: vi.fn(async (_email: string, id: string) => ({
        message_id: id,
        from: "alice@example.com",
        subject: "hi",
      })),
      getAgent: vi.fn(async (email: string) => ({
        id: email,
        email,
        agent_mode: "local",
      })),
    },
    send: vi.fn(async () => ({ message_id: "msg_sent", status: "sent" })),
    reply: vi.fn(async () => ({ message_id: "msg_reply", status: "sent" })),
    forward: vi.fn(async () => ({ message_id: "msg_fwd", status: "sent" })),
    listMessages: vi.fn(async () => ({ messages: [], next_token: undefined })),
    listAgents: vi.fn(async () => ({ agents: [{ email: "bot@example.com" }] })),
    registerAgent: vi.fn(async (body: Record<string, unknown>) => ({
      email: `${body.slug}@agents.example.com`,
      domain: "agents.example.com",
      id: `${body.slug}@agents.example.com`,
    })),
    getAgent: vi.fn(async (email: string) => ({
      id: email,
      email,
      agent_mode: "local",
      hitl_enabled: false,
    })),
    updateAgent: vi.fn(async (body: Record<string, unknown>) => ({
      id: "bot@example.com",
      email: "bot@example.com",
      ...body,
    })),
    deleteAgent: vi.fn(async () => undefined),
    listDomains: vi.fn(async () => ({
      domains: [
        { domain: "mail.acme.com", verified: true, verification_token: "tok1" },
      ],
    })),
    registerDomain: vi.fn(async (domain: string) => ({
      domain,
      verified: false,
      verification_token: "tok_new",
      dns_records: {
        mx: { host: domain, value: "mx.e2a.dev", priority: 10 },
        txt: { host: domain, value: "e2a-verify=tok_new" },
      },
    })),
    verifyDomain: vi.fn(async (domain: string) => ({
      domain,
      verified: true,
      verification_token: "tok_new",
    })),
    deleteDomain: vi.fn(async () => undefined),
    // Stand-in for the SDK's client.getMessage() which returns an
    // InboundEmail with verified=true (the REST channel is bearer-
    // authenticated so the SDK marks pre-verified). The production
    // tool reads the getters which on the real class throw if
    // unverified — but TypeScript's structural typing doesn't
    // distinguish data properties from getters, so this plain
    // object satisfies the call site. Attachments default to one
    // small PDF; tests override via mockResolvedValueOnce.
    getMessage: vi.fn(async (id: string, _email?: string) => ({
      messageId: id,
      conversationId: "conv_x",
      sender: "alice@example.com",
      recipient: "bot@example.com",
      to: ["bot@example.com"],
      cc: [],
      replyTo: [],
      subject: "hi",
      textBody: "hello world",
      htmlBody: null,
      receivedAt: "2026-05-20T10:00:00Z",
      attachments: [
        {
          filename: "report.pdf",
          contentType: "application/pdf",
          data: Buffer.from("%PDF-1.4 fake pdf bytes"),
          size: 23,
        },
      ],
    })),
    listPendingMessages: vi.fn(async () => ({ messages: [] })),
    getPendingMessage: vi.fn(async (id: string) => ({ id, status: "pending_approval" })),
    approveMessage: vi.fn(async () => ({ message_id: "msg_x", status: "sent" })),
    rejectMessage: vi.fn(async () => ({ message_id: "msg_x", status: "rejected" })),
  };
  return stub as unknown as E2AClient;
}

async function connect(stub: E2AClient): Promise<Client> {
  const server = buildServer({ client: stub, version: "0.0.0-test" });
  const client = new Client({ name: "test-client", version: "0.0.0" });
  const [clientT, serverT] = InMemoryTransport.createLinkedPair();
  await Promise.all([server.connect(serverT), client.connect(clientT)]);
  return client;
}

describe("e2a MCP server", () => {
  let stub: E2AClient;
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
        "send_email",
        "reply_to_message",
        "forward_message",
        "list_messages",
        "get_message",
        "get_attachment_data",
        "list_agents",
        "whoami",
        "create_agent",
        "update_agent",
        "delete_agent",
        "list_domains",
        "register_domain",
        "verify_domain",
        "delete_domain",
        "list_pending_messages",
        "get_pending_message",
        "approve_pending_message",
        "reject_pending_message",
      ].sort(),
    );
  });

  it("send_email forwards args to client.send", async () => {
    await client.callTool({
      name: "send_email",
      arguments: {
        to: ["alice@example.com"],
        subject: "hi",
        body: "hello",
        cc: ["bob@example.com"],
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      ["alice@example.com"],
      "hi",
      "hello",
      { cc: ["bob@example.com"] },
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
    expect(stub.reply).toHaveBeenCalledWith("msg_in", "thanks", { replyAll: true });
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
    );
  });

  it("list_messages forwards filters", async () => {
    await client.callTool({
      name: "list_messages",
      arguments: { status: "unread", page_size: 10 },
    });
    expect(stub.listMessages).toHaveBeenCalledWith({
      status: "unread",
      pageSize: 10,
    });
  });

  it("get_message uses the env agent email when omitted and returns parsed shape", async () => {
    const res = await client.callTool({
      name: "get_message",
      arguments: { message_id: "msg_abc" },
    });
    // High-level client.getMessage (the parsed InboundEmail path) —
    // not client.api.getMessage. The MCP server is responsible for
    // unwrapping to plain JSON so the LLM gets a digestible shape
    // without the raw_message MIME blob.
    expect(stub.getMessage).toHaveBeenCalledWith("msg_abc", "bot@example.com");
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

  it("get_attachment_data returns one attachment with base64 data", async () => {
    const res = await client.callTool({
      name: "get_attachment_data",
      arguments: { message_id: "msg_abc", attachment_index: 0 },
    });
    expect(stub.getMessage).toHaveBeenCalledWith("msg_abc", "bot@example.com");
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

  it("get_attachment_data rejects out-of-range index with a clear error", async () => {
    const res = await client.callTool({
      name: "get_attachment_data",
      arguments: { message_id: "msg_abc", attachment_index: 5 },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]!.text).toMatch(/out of range/);
  });

  it("get_attachment_data refuses attachments above the 2 MB inline cap", async () => {
    // One-shot override: pretend the message has a 5 MB attachment.
    (stub.getMessage as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      messageId: "msg_big",
      conversationId: null,
      sender: "alice@example.com",
      recipient: "bot@example.com",
      to: ["bot@example.com"],
      cc: [],
      replyTo: [],
      subject: "huge",
      textBody: "",
      htmlBody: null,
      receivedAt: null,
      attachments: [
        {
          filename: "big.zip",
          contentType: "application/zip",
          data: Buffer.alloc(0),
          size: 5 * 1024 * 1024,
        },
      ],
    });
    const res = await client.callTool({
      name: "get_attachment_data",
      arguments: { message_id: "msg_big", attachment_index: 0 },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]!.text).toMatch(/too large for inline retrieval/);
  });

  it("whoami returns the env-scoped agent record", async () => {
    const res = await client.callTool({ name: "whoami", arguments: {} });
    // High-level client.getAgent — used to be client.api.getAgent before
    // we tidied the leftover raw-API call. Both paths hit the same row.
    expect(stub.getAgent).toHaveBeenCalledWith("bot@example.com");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toContain("bot@example.com");
  });

  it("whoami auto-falls-back to the sole agent when env is unset and account owns exactly one", async () => {
    // Single-agent account, no env pin → should auto-resolve to the
    // one agent rather than error. Mirrors send_email's "from
    // required only when multiple agents" auto-from behavior.
    const bareStub = makeStubClient({ agentEmail: "" });
    (bareStub.listAgents as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      agents: [{ id: "solo@example.com", email: "solo@example.com" }],
    });
    const bareClient = await connect(bareStub);
    const res = await bareClient.callTool({ name: "whoami", arguments: {} });
    expect(res.isError).toBeFalsy();
    expect(bareStub.getAgent).toHaveBeenCalledWith("solo@example.com");
  });

  it("whoami errors with the inline agent list when account owns multiple agents", async () => {
    // Multi-agent + no env pin → refuse to guess, but include the
    // available agent emails in the error so the LLM can act
    // without a follow-up list_agents call.
    const bareStub = makeStubClient({ agentEmail: "" });
    (bareStub.listAgents as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      agents: [
        { id: "support@x.com", email: "support@x.com" },
        { id: "sales@x.com", email: "sales@x.com" },
      ],
    });
    const bareClient = await connect(bareStub);
    const res = await bareClient.callTool({ name: "whoami", arguments: {} });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    // Error should name BOTH agents so the LLM doesn't need a
    // follow-up list_agents call to pick.
    expect(content[0]?.text).toContain("support@x.com");
    expect(content[0]?.text).toContain("sales@x.com");
  });

  it("whoami errors when the account has zero agents and prompts to create one", async () => {
    const bareStub = makeStubClient({ agentEmail: "" });
    (bareStub.listAgents as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      agents: [],
    });
    const bareClient = await connect(bareStub);
    const res = await bareClient.callTool({ name: "whoami", arguments: {} });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/create_agent/);
  });

  it("create_agent defaults agent_mode to local", async () => {
    await client.callTool({
      name: "create_agent",
      arguments: { slug: "new-bot" },
    });
    expect(stub.registerAgent).toHaveBeenCalledWith({
      slug: "new-bot",
      agent_mode: "local",
    });
  });

  it("create_agent forwards cloud-mode webhook", async () => {
    await client.callTool({
      name: "create_agent",
      arguments: {
        slug: "cloud-bot",
        agent_mode: "cloud",
        webhook_url: "https://example.com/hook",
        name: "Cloud Bot",
      },
    });
    expect(stub.registerAgent).toHaveBeenCalledWith({
      slug: "cloud-bot",
      agent_mode: "cloud",
      name: "Cloud Bot",
      webhook_url: "https://example.com/hook",
    });
  });

  it("update_agent forwards body fields and uses env email by default", async () => {
    await client.callTool({
      name: "update_agent",
      arguments: { hitl_enabled: true, hitl_ttl_seconds: 3600 },
    });
    expect(stub.updateAgent).toHaveBeenCalledWith(
      { hitl_enabled: true, hitl_ttl_seconds: 3600 },
      {}, // no agent_email override → falls back to env-scoped agentEmail in the SDK
    );
  });

  it("update_agent threads explicit agent_email into opts.agentEmail", async () => {
    await client.callTool({
      name: "update_agent",
      arguments: {
        agent_email: "other@example.com",
        agent_mode: "cloud",
        webhook_url: "https://example.com/hook",
      },
    });
    expect(stub.updateAgent).toHaveBeenCalledWith(
      { agent_mode: "cloud", webhook_url: "https://example.com/hook" },
      { agentEmail: "other@example.com" },
    );
  });

  it("delete_agent requires confirm:true — server-side schema rejects when omitted", async () => {
    // The Zod schema marks `confirm` as required-literal(true); the MCP
    // server's validator surfaces that as an isError content before any
    // runTool body runs, so deleteAgent must NOT have been called.
    const res = await client.callTool({
      name: "delete_agent",
      arguments: { agent_email: "bot@example.com" },
    });
    expect(res.isError).toBe(true);
    expect(stub.deleteAgent).not.toHaveBeenCalled();
  });

  it("delete_agent forwards on explicit confirm:true", async () => {
    const res = await client.callTool({
      name: "delete_agent",
      arguments: { agent_email: "bot@example.com", confirm: true },
    });
    expect(stub.deleteAgent).toHaveBeenCalledWith("bot@example.com");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/bot@example\.com/);
  });

  it("delete_agent uses env-scoped email when agent_email omitted", async () => {
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

  it("approve_pending_message strips message_id and forwards overrides", async () => {
    await client.callTool({
      name: "approve_pending_message",
      arguments: {
        message_id: "msg_p",
        subject: "edited subject",
        body_text: "edited body",
      },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", {
      subject: "edited subject",
      body_text: "edited body",
    });
  });

  it("approve_pending_message approve-as-is sends empty overrides", async () => {
    await client.callTool({
      name: "approve_pending_message",
      arguments: { message_id: "msg_p" },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", {});
  });

  // Regression: when idempotency_key is omitted, the MCP layer must
  // call approveMessage with exactly TWO args — not three args with
  // `{ idempotencyKey: undefined }`. Passing the undefined object
  // sneaks past TypeScript but a callsite that defaults the key
  // (e.g. an auto-mint helper inside the SDK) would receive
  // `{ idempotencyKey: undefined }` as "user explicitly set this to
  // undefined" rather than "user didn't set this" — different
  // semantics. vitest's toHaveBeenCalledWith does deep-equal on args
  // and is strict on argument count, so this test fails if a 3rd
  // arg leaks in. Mirrors the same guard on send / reply tests
  // above (lines 145 / 163).
  it("approve_pending_message omits 3rd-arg opts when idempotency_key is unset", async () => {
    await client.callTool({
      name: "approve_pending_message",
      arguments: { message_id: "msg_p", subject: "edited" },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", { subject: "edited" });
    const lastCall = (stub.approveMessage as unknown as { mock: { calls: unknown[][] } }).mock.calls.at(-1);
    expect(lastCall?.length).toBe(2);
  });

  // Approve fires SES so an idempotency_key argument has to reach the
  // SDK or retries can double-send. Strip the key out of overrides
  // (the API doesn't take it in the JSON body) and forward it as the
  // third-arg options object.
  it("approve_pending_message forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "approve_pending_message",
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

  // send_email and reply_to_message also expose idempotency_key. Verify
  // the MCP tool plumbs it through as `idempotencyKey` in SDK opts.
  it("send_email forwards idempotency_key to the SDK", async () => {
    await client.callTool({
      name: "send_email",
      arguments: {
        to: ["alice@example.com"],
        subject: "x",
        body: "y",
        idempotency_key: "send-key-9",
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      ["alice@example.com"],
      "x",
      "y",
      expect.objectContaining({ idempotencyKey: "send-key-9" }),
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
      "reply",
      expect.objectContaining({ idempotencyKey: "reply-key-9" }),
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

  it("send_email forwards attachments verbatim to client.send", async () => {
    await client.callTool({
      name: "send_email",
      arguments: {
        to: ["alice@example.com"],
        subject: "with file",
        body: "see attached",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.send).toHaveBeenCalledWith(
      ["alice@example.com"],
      "with file",
      "see attached",
      { attachments: [sampleAttachment] },
    );
  });

  it("reply_to_message forwards attachments verbatim to client.reply", async () => {
    await client.callTool({
      name: "reply_to_message",
      arguments: {
        message_id: "msg_in",
        body: "reply with file",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.reply).toHaveBeenCalledWith("msg_in", "reply with file", {
      attachments: [sampleAttachment],
    });
  });

  it("approve_pending_message accepts an attachments override (HITL reviewer adds a file)", async () => {
    await client.callTool({
      name: "approve_pending_message",
      arguments: {
        message_id: "msg_p",
        attachments: [sampleAttachment],
      },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", {
      attachments: [sampleAttachment],
    });
  });

  it("approve_pending_message empty attachments:[] is forwarded as a strip override", async () => {
    // Reviewer wants to remove all attachments the agent proposed.
    // Empty array must reach the SDK; if we accidentally filtered it
    // out, the backend would treat the override as absent (keep
    // existing attachments) — wrong behavior.
    await client.callTool({
      name: "approve_pending_message",
      arguments: { message_id: "msg_p", attachments: [] },
    });
    expect(stub.approveMessage).toHaveBeenCalledWith("msg_p", { attachments: [] });
  });

  it("send_email rejects base64 with whitespace (URL-safe or LLM-truncated patterns)", async () => {
    const res = await client.callTool({
      name: "send_email",
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

  it("send_email rejects base64 with length not divisible by 4 (truncation signal)", async () => {
    const res = await client.callTool({
      name: "send_email",
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

  it("send_email rejects malformed content_type", async () => {
    const res = await client.callTool({
      name: "send_email",
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

  it("reject_pending_message forwards the reason", async () => {
    await client.callTool({
      name: "reject_pending_message",
      arguments: { message_id: "msg_p", reason: "wrong recipient" },
    });
    expect(stub.rejectMessage).toHaveBeenCalledWith("msg_p", "wrong recipient");
  });

  it("surfaces SDK errors as isError results", async () => {
    (stub.send as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("HTTP 403: domain not verified"),
    );
    const res = await client.callTool({
      name: "send_email",
      arguments: { to: ["x@example.com"], subject: "s", body: "b" },
    });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/domain not verified/);
  });
});
