import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type { McpClient } from "../src/client.js";
import { startHttpServer } from "../src/http-server.js";

// Events tools exercised through the streamable-http MCP transport
// (the production-shipped transport for `mcp.e2a.dev/mcp`). Mirrors
// the InMemoryTransport tests in events.test.ts but goes through the
// real HTTP layer — catches auth header parsing, JSON-RPC framing,
// and session lifecycle bugs that InMemoryTransport masks.

function makeStubClient(): McpClient {
  const stub = {
    agentEmail: "bot@example.com",
    getMessage: vi.fn(async () => ({ messageId: "m" })),
    getAgent: vi.fn(async () => ({ id: "x@y", email: "x@y" })),
    listAgents: vi.fn(async () => [{ email: "bot@example.com" }]),
    listEvents: vi.fn(async () => [
      {
        id: "evt_http",
        type: "email.received",
        schemaVersion: 1,
        createdAt: "2026-06-01T12:00:00Z",
        status: "processed",
        data: { from: "alice@example.com" },
      },
    ]),
    getEvent: vi.fn(async (id: string) => ({
      id,
      type: "email.received",
      schemaVersion: 1,
      createdAt: "2026-06-01T12:00:00Z",
      status: "processed",
      data: {},
      deliveryStatus: { matchedWebhooks: 1, delivered: 1, pending: 0, failed: 0 },
    })),
    redeliverEvent: vi.fn(async (id: string, webhookId?: string) => ({
      eventId: id,
      webhookId: webhookId ?? "",
      deliveryId: "whd_replay_http",
      status: "pending",
    })),
  };
  return stub as unknown as McpClient;
}

describe("MCP events tools over streamable-http", () => {
  let stub: McpClient;
  let close: () => Promise<void>;
  let url: string;

  beforeEach(async () => {
    stub = makeStubClient();
    const { close: c, port } = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      allowedHosts: ["127.0.0.1", "localhost"],
      clientFactory: () => stub,
    });
    close = c;
    url = `http://127.0.0.1:${port}/mcp`;
  });

  afterEach(async () => {
    await close();
  });

  async function connect(): Promise<Client> {
    const transport = new StreamableHTTPClientTransport(new URL(url), {
      requestInit: { headers: { Authorization: "Bearer e2a_test" } },
    });
    const client = new Client({ name: "events-http-test", version: "1.0" });
    await client.connect(transport);
    return client;
  }

  function parseToolResult(result: unknown): Record<string, unknown> {
    const r = result as { content: { type: string; text: string }[] };
    expect(r.content[0].type).toBe("text");
    return JSON.parse(r.content[0].text);
  }

  it("lists events via real HTTP transport", async () => {
    const client = await connect();
    const result = await client.callTool({ name: "list_events", arguments: {} });
    const parsed = parseToolResult(result);
    expect((parsed.events as Array<{ id: string }>)[0].id).toBe("evt_http");
    expect(stub.listEvents).toHaveBeenCalledOnce();
    await client.close();
  });

  it("gets a single event via real HTTP transport", async () => {
    const client = await connect();
    const result = await client.callTool({
      name: "get_event",
      arguments: { event_id: "evt_via_http" },
    });
    const parsed = parseToolResult(result);
    expect(parsed.id).toBe("evt_via_http");
    expect(stub.getEvent).toHaveBeenCalledWith("evt_via_http");
    await client.close();
  });

  it("redelivers via real HTTP transport", async () => {
    const client = await connect();
    const result = await client.callTool({
      name: "redeliver_event",
      arguments: { event_id: "evt_x", webhook_id: "wh_y" },
    });
    const parsed = parseToolResult(result);
    expect(parsed.deliveryId).toBe("whd_replay_http");
    expect(stub.redeliverEvent).toHaveBeenCalledWith("evt_x", "wh_y");
    await client.close();
  });

  it("auth header reaches the MCP server (rejects missing bearer)", async () => {
    const transport = new StreamableHTTPClientTransport(new URL(url), {
      requestInit: { headers: {} }, // no Authorization
    });
    const client = new Client({ name: "no-auth-test", version: "1.0" });
    await expect(client.connect(transport)).rejects.toThrow();
  });

  // Bad-bearer rejection is exhaustively tested in
  // mcp/tests/http.test.ts (added by PR #104). Not re-covered here.
});
