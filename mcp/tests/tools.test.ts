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
    expect(stub.api.getAgent).toHaveBeenCalledWith("bot@example.com");
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
