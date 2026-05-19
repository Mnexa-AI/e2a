import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { startHttpServer } from "../src/http-server.js";

// Reuse the same stub shape from tools.test.ts. Only the methods the
// tools actually call need to be present.
function makeStubClient(): E2AClient {
  const stub = {
    agentEmail: "bot@example.com",
    api: {
      getMessage: vi.fn(async (_e: string, id: string) => ({ message_id: id })),
      getAgent: vi.fn(async (e: string) => ({ id: e, email: e })),
    },
    send: vi.fn(async () => ({ message_id: "msg_sent", status: "sent" })),
    reply: vi.fn(async () => ({ message_id: "msg_reply", status: "sent" })),
    listMessages: vi.fn(async () => ({ messages: [] })),
    listAgents: vi.fn(async () => ({ agents: [] })),
    registerAgent: vi.fn(async () => ({ email: "x@y", id: "x", domain: "y" })),
    listPendingMessages: vi.fn(async () => ({ messages: [] })),
    getPendingMessage: vi.fn(async () => ({ id: "p", status: "pending_approval" })),
    approveMessage: vi.fn(async () => ({ message_id: "x", status: "sent" })),
    rejectMessage: vi.fn(async () => ({ message_id: "x", status: "rejected" })),
  };
  return stub as unknown as E2AClient;
}

describe("HTTP MCP server", () => {
  let stub: E2AClient;
  let close: () => Promise<void>;
  let url: string;

  beforeEach(async () => {
    stub = makeStubClient();
    const { close: c, port } = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      // Loopback hostnames vary with the random port; allow them all.
      allowedHosts: ["127.0.0.1", "localhost"],
      clientFactory: () => stub,
    });
    close = c;
    url = `http://127.0.0.1:${port}/mcp`;
  });

  afterEach(async () => {
    await close();
  });

  async function connect(headers: Record<string, string> = { Authorization: "Bearer e2a_test" }) {
    const transport = new StreamableHTTPClientTransport(new URL(url), {
      requestInit: { headers },
    });
    const client = new Client({ name: "http-test", version: "0.0.0" });
    await client.connect(transport);
    return { client, transport };
  }

  it("healthz responds OK without auth", async () => {
    const res = await fetch(`${url.replace("/mcp", "")}/healthz`);
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true });
  });

  it("missing bearer returns 401 with WWW-Authenticate", async () => {
    const res = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "x", version: "0" } },
      }),
    });
    expect(res.status).toBe(401);
    expect(res.headers.get("www-authenticate")).toMatch(/Bearer realm="e2a"/);
    const body = await res.json();
    expect(body.error.message).toMatch(/missing bearer/);
  });

  it("oauth-protected-resource discovery returns expected metadata", async () => {
    const res = await fetch(`${url.replace("/mcp", "")}/.well-known/oauth-protected-resource`);
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.authorization_servers).toEqual(["http://e2a.local"]);
    expect(body.bearer_methods_supported).toEqual(["header"]);
  });

  it("lists all 11 tools after initialize", async () => {
    const { client, transport } = await connect();
    const { tools } = await client.listTools();
    expect(tools.map((t) => t.name).sort()).toEqual(
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
    await transport.close();
  });

  it("forwards Bearer transparently to the per-session E2AClient", async () => {
    // The factory we passed receives the bearer string. We can assert it
    // gets exactly what the client sent without any rewriting.
    const factorySpy = vi.fn(() => stub);
    await close();
    const { close: c, port } = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      allowedHosts: ["127.0.0.1", "localhost"],
      clientFactory: factorySpy,
    });
    close = c;
    url = `http://127.0.0.1:${port}/mcp`;
    const { client, transport } = await connect({ Authorization: "Bearer ate2a_oauth_token_xyz" });
    await client.listTools();
    expect(factorySpy).toHaveBeenCalledWith("ate2a_oauth_token_xyz");
    await transport.close();
  });

  it("tool call dispatches to the per-session client", async () => {
    const { client, transport } = await connect();
    await client.callTool({ name: "list_agents", arguments: {} });
    expect(stub.listAgents).toHaveBeenCalledOnce();
    await transport.close();
  });

  it("rejects non-initialize requests without a session", async () => {
    const res = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: "Bearer e2a_test",
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 99, method: "tools/list" }),
    });
    expect(res.status).toBe(400);
    const body = await res.json();
    expect(body.error.message).toMatch(/no session/);
  });

  it("DELETE /mcp without session id returns 400", async () => {
    const res = await fetch(url, {
      method: "DELETE",
      headers: { Authorization: "Bearer e2a_test" },
    });
    expect(res.status).toBe(400);
  });

  it("DELETE /mcp with unknown session id returns 404", async () => {
    const res = await fetch(url, {
      method: "DELETE",
      headers: {
        Authorization: "Bearer e2a_test",
        "mcp-session-id": "ghost-session-id",
      },
    });
    expect(res.status).toBe(404);
  });

  it("GET /mcp without session id returns 400", async () => {
    const res = await fetch(url, {
      method: "GET",
      headers: { Authorization: "Bearer e2a_test" },
    });
    expect(res.status).toBe(400);
  });

  it("DELETE-then-POST: terminating a session makes follow-up POSTs return 400", async () => {
    // initialize via raw fetch so we can capture the SDK-issued Mcp-Session-Id
    const initRes = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: "Bearer e2a_test",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: {
          protocolVersion: "2024-11-05",
          capabilities: {},
          clientInfo: { name: "x", version: "0" },
        },
      }),
    });
    // SSE body — drain so the connection closes cleanly before we DELETE.
    await initRes.text();
    const sessionId = initRes.headers.get("mcp-session-id");
    expect(sessionId).toBeTruthy();

    const delRes = await fetch(url, {
      method: "DELETE",
      headers: {
        Authorization: "Bearer e2a_test",
        "mcp-session-id": sessionId!,
      },
    });
    expect(delRes.status).toBeLessThan(400);

    // Post-deletion: the same session id should no longer resolve, and
    // a non-initialize body must surface as "no session".
    const followup = await fetch(url, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: "Bearer e2a_test",
        "mcp-session-id": sessionId!,
      },
      body: JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list" }),
    });
    expect(followup.status).toBe(400);
    const body = await followup.json();
    expect(body.error.message).toMatch(/no session/);
  });

  it("rejects requests when Host is not in the SDK allowlist", async () => {
    // Tear down the loopback-friendly server and bring up one with a strict
    // allowlist so the SDK's DNS-rebinding guard fires on a normal request.
    await close();
    const { close: c, port } = await startHttpServer(0, {
      baseUrl: "http://e2a.local",
      allowedHosts: ["mcp.e2a.dev"], // 127.0.0.1 not allowed
      clientFactory: () => stub,
    });
    close = c;
    const res = await fetch(`http://127.0.0.1:${port}/mcp`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        Authorization: "Bearer e2a_test",
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "initialize",
        params: { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "x", version: "0" } },
      }),
    });
    expect(res.status).toBe(421);
  });
});
