import { describe, expect, it, beforeEach, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { buildServer } from "../src/server.js";

// Slice 8 MCP tool tests for list_events / get_event / redeliver_event.
// Pattern mirrors tools.test.ts — in-memory transport, stub E2AClient,
// assert wire shape passed to the SDK + the tool result.

function makeStubClient(): E2AClient {
  const stub = {
    agentEmail: "bot@example.com",
    api: {
      // Existing methods the tools may touch transitively.
      getMessage: vi.fn(async () => ({ message_id: "m" })),
      getAgent: vi.fn(async () => ({ id: "x@y", email: "x@y" })),
      // Events methods.
      listEvents: vi.fn(async (params?: Record<string, unknown>) => ({
        events: [
          {
            id: "evt_abc",
            type: "email.received",
            schema_version: 1,
            created_at: "2026-06-01T12:00:00Z",
            status: "processed",
            data: { from: "alice@example.com" },
            _captured: params, // smuggle the params back for assertion
          },
        ],
        next_token: "next_cursor",
      })),
      getEvent: vi.fn(async (id: string) => ({
        id,
        type: "email.received",
        schema_version: 1,
        created_at: "2026-06-01T12:00:00Z",
        status: "processed",
        data: {},
        delivery_status: { matched_webhooks: 1, delivered: 1, pending: 0, failed: 0 },
      })),
      redeliverEvent: vi.fn(
        async (id: string, opts?: { webhookId?: string }) => ({
          event_id: id,
          webhook_id: opts?.webhookId ?? "",
          delivery_id: "whd_replay_xyz",
          status: "pending",
          _capturedOpts: opts,
        }),
      ),
    },
  };
  return stub as unknown as E2AClient;
}

async function buildClient(stub: E2AClient): Promise<Client> {
  const server = buildServer({ client: stub, version: "test" });
  const [serverTransport, clientTransport] = InMemoryTransport.createLinkedPair();
  await server.connect(serverTransport);
  const client = new Client({ name: "test", version: "1.0" });
  await client.connect(clientTransport);
  return client;
}

function parseToolResult(result: unknown): Record<string, unknown> {
  const r = result as { content: { type: string; text: string }[] };
  expect(r.content[0].type).toBe("text");
  return JSON.parse(r.content[0].text);
}

describe("MCP events tools", () => {
  let stub: E2AClient;

  beforeEach(() => {
    stub = makeStubClient();
  });

  describe("list_events", () => {
    it("is registered", async () => {
      const client = await buildClient(stub);
      const { tools } = await client.listTools();
      const names = tools.map((t) => t.name);
      expect(names).toContain("list_events");
    });

    it("invokes client.api.listEvents with no params", async () => {
      const client = await buildClient(stub);
      const result = await client.callTool({ name: "list_events", arguments: {} });
      const parsed = parseToolResult(result);
      expect(parsed.events).toBeDefined();
      const events = parsed.events as Array<{ id: string }>;
      expect(events[0].id).toBe("evt_abc");
      expect(stub.api.listEvents).toHaveBeenCalledOnce();
    });

    it("forwards all filter params to the SDK", async () => {
      const client = await buildClient(stub);
      await client.callTool({
        name: "list_events",
        arguments: {
          type: "email.received",
          agent_id: "ag_x",
          conversation_id: "conv_y",
          message_id: "msg_z",
          since: "2026-06-01T00:00:00Z",
          until: "2026-06-02T00:00:00Z",
          page_size: 25,
          token: "opaque",
        },
      });
      expect(stub.api.listEvents).toHaveBeenCalledWith({
        type: "email.received",
        agentId: "ag_x",
        conversationId: "conv_y",
        messageId: "msg_z",
        since: "2026-06-01T00:00:00Z",
        until: "2026-06-02T00:00:00Z",
        pageSize: 25,
        token: "opaque",
      });
    });

    it("includes next_token in result for pagination", async () => {
      const client = await buildClient(stub);
      const result = await client.callTool({ name: "list_events", arguments: {} });
      const parsed = parseToolResult(result);
      expect(parsed.next_token).toBe("next_cursor");
    });
  });

  describe("get_event", () => {
    it("is registered", async () => {
      const client = await buildClient(stub);
      const { tools } = await client.listTools();
      expect(tools.map((t) => t.name)).toContain("get_event");
    });

    it("calls client.api.getEvent with the event_id", async () => {
      const client = await buildClient(stub);
      const result = await client.callTool({
        name: "get_event",
        arguments: { event_id: "evt_xyz" },
      });
      const parsed = parseToolResult(result);
      expect(parsed.id).toBe("evt_xyz");
      expect(stub.api.getEvent).toHaveBeenCalledWith("evt_xyz");
    });

    it("surfaces delivery_status in the response", async () => {
      const client = await buildClient(stub);
      const result = await client.callTool({
        name: "get_event",
        arguments: { event_id: "evt_xyz" },
      });
      const parsed = parseToolResult(result);
      const ds = parsed.delivery_status as Record<string, number>;
      expect(ds.delivered).toBe(1);
    });
  });

  describe("redeliver_event", () => {
    it("is registered", async () => {
      const client = await buildClient(stub);
      const { tools } = await client.listTools();
      expect(tools.map((t) => t.name)).toContain("redeliver_event");
    });

    it("targeted: forwards webhook_id to the SDK", async () => {
      const client = await buildClient(stub);
      await client.callTool({
        name: "redeliver_event",
        arguments: { event_id: "evt_abc", webhook_id: "wh_target" },
      });
      expect(stub.api.redeliverEvent).toHaveBeenCalledWith("evt_abc", {
        webhookId: "wh_target",
      });
    });

    it("fan-out: omits webhook_id when not provided", async () => {
      const client = await buildClient(stub);
      await client.callTool({
        name: "redeliver_event",
        arguments: { event_id: "evt_abc" },
      });
      expect(stub.api.redeliverEvent).toHaveBeenCalledWith("evt_abc", {
        webhookId: undefined,
      });
    });

    it("returns the new delivery_id", async () => {
      const client = await buildClient(stub);
      const result = await client.callTool({
        name: "redeliver_event",
        arguments: { event_id: "evt_abc", webhook_id: "wh_x" },
      });
      const parsed = parseToolResult(result);
      expect(parsed.delivery_id).toBe("whd_replay_xyz");
    });
  });

  describe("tool catalog", () => {
    it("includes the 3 new events tools alongside existing 18 — total 21", async () => {
      const client = await buildClient(stub);
      const { tools } = await client.listTools();
      const names = new Set(tools.map((t) => t.name));
      expect(names.has("list_events")).toBe(true);
      expect(names.has("get_event")).toBe(true);
      expect(names.has("redeliver_event")).toBe(true);
      // Existing tools should still be registered.
      expect(names.has("send_email")).toBe(true);
      expect(names.has("list_messages")).toBe(true);
      expect(names.has("whoami")).toBe(true);
    });
  });

  describe("concurrent invocations", () => {
    it("handles 20 concurrent list_events calls", async () => {
      const client = await buildClient(stub);
      const results = await Promise.all(
        Array.from({ length: 20 }, () =>
          client.callTool({ name: "list_events", arguments: {} }),
        ),
      );
      expect(results).toHaveLength(20);
      for (const r of results) {
        const parsed = parseToolResult(r);
        expect((parsed.events as unknown[]).length).toBe(1);
      }
      expect(stub.api.listEvents).toHaveBeenCalledTimes(20);
    });

    it("handles concurrent mix of all three tools", async () => {
      const client = await buildClient(stub);
      await Promise.all([
        client.callTool({ name: "list_events", arguments: { type: "email.received" } }),
        client.callTool({ name: "get_event", arguments: { event_id: "evt_a" } }),
        client.callTool({
          name: "redeliver_event",
          arguments: { event_id: "evt_b", webhook_id: "wh_x" },
        }),
        client.callTool({ name: "list_events", arguments: {} }),
        client.callTool({ name: "get_event", arguments: { event_id: "evt_c" } }),
      ]);
      expect(stub.api.listEvents).toHaveBeenCalledTimes(2);
      expect(stub.api.getEvent).toHaveBeenCalledTimes(2);
      expect(stub.api.redeliverEvent).toHaveBeenCalledTimes(1);
    });
  });
});
