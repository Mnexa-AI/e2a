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
        "list_messages",
        "get_message",
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

  it("get_message uses the env agent email when omitted", async () => {
    await client.callTool({
      name: "get_message",
      arguments: { message_id: "msg_abc" },
    });
    expect(stub.api.getMessage).toHaveBeenCalledWith("bot@example.com", "msg_abc");
  });

  it("whoami returns the env-scoped agent record", async () => {
    const res = await client.callTool({ name: "whoami", arguments: {} });
    // High-level client.getAgent — used to be client.api.getAgent before
    // we tidied the leftover raw-API call. Both paths hit the same row.
    expect(stub.getAgent).toHaveBeenCalledWith("bot@example.com");
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toContain("bot@example.com");
  });

  it("whoami errors clearly when no default agent is configured", async () => {
    const bareStub = makeStubClient({ agentEmail: "" });
    const bareClient = await connect(bareStub);
    const res = await bareClient.callTool({ name: "whoami", arguments: {} });
    expect(res.isError).toBe(true);
    const content = res.content as Array<{ type: string; text: string }>;
    expect(content[0]?.text).toMatch(/E2A_AGENT_EMAIL/);
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
