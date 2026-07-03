// McpClient review-queue routing: the tools split across credential tiers, so
// they MUST hit tier-correct endpoints —
//   approve_message / reject_message  → ADMIN (account-only)  → sdk.reviews.*
//   list_pending / get_pending         → RUNTIME (agent-visible) → sdk.messages.*
// The account-only /v1/reviews path 403s an agent-scoped credential, so routing
// a runtime-tier tool through it is a regression. These tests pin the routing.

import { describe, it, expect, vi, afterEach } from "vitest";
import { createServer, type Server } from "node:http";
import { E2AError } from "@e2a/sdk/v1";
import { McpClient } from "../src/client.js";

function mockSdk() {
  return {
    reviews: {
      approve: vi.fn(async () => ({ messageId: "msg_p", status: "sent" })),
      reject: vi.fn(async () => ({ messageId: "msg_p", status: "rejected" })),
      get: vi.fn(async () => ({ messageId: "msg_p" })),
    },
    messages: {
      get: vi.fn(async () => ({ messageId: "msg_p" })),
      // ownerOfPending scans outbound to resolve the owning inbox.
      list: vi.fn(() => ({ toArray: async () => [{ messageId: "msg_p" }] })),
    },
    agents: {
      list: vi.fn(() => ({ toArray: async () => [{ email: "bot@test.dev" }] })),
    },
  };
}

describe("McpClient review routing (tier-correct endpoints)", () => {
  it("approveMessage → account-only reviews.approve (not messages.approve)", async () => {
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "account");
    await c.approveMessage("msg_p", {});
    expect(sdk.reviews.approve).toHaveBeenCalledWith("msg_p", {});
  });

  it("rejectMessage → account-only reviews.reject", async () => {
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "account");
    await c.rejectMessage("msg_p", "spam");
    expect(sdk.reviews.reject).toHaveBeenCalledWith("msg_p", { reason: "spam" });
  });

  it("getPendingMessage → agent-reachable messages.get, NOT account-only reviews.get", async () => {
    // Regression guard (PR #284 adversarial finding): get_pending_message is a
    // runtime-tier tool, so it must NOT route through sdk.reviews.get — that path
    // 403s an agent-scoped credential.
    const sdk = mockSdk();
    const c = new McpClient(sdk as never, "bot@test.dev", "agent");
    await c.getPendingMessage("msg_p");
    expect(sdk.messages.get).toHaveBeenCalledWith("bot@test.dev", "msg_p");
    expect(sdk.reviews.get).not.toHaveBeenCalled();
  });
});

// Templates (beta) go over a raw fetch path (no SDK resource yet), so the
// wire behavior — bearer header, method/path, envelope→E2AError mapping —
// is pinned against a real HTTP server rather than an SDK mock.
describe("McpClient templates raw path (beta)", () => {
  let server: Server | undefined;
  afterEach(() => server?.close());

  type Seen = { method?: string; url?: string; auth?: string; body?: string };

  async function clientAgainstMock(
    handler: (seen: Seen) => { status: number; body?: unknown },
  ): Promise<{ client: McpClient; seen: Seen }> {
    const seen: Seen = {};
    server = createServer((req, res) => {
      seen.method = req.method;
      seen.url = req.url;
      seen.auth = req.headers.authorization;
      let data = "";
      req.on("data", (c) => (data += c));
      req.on("end", () => {
        seen.body = data;
        const out = handler(seen);
        res.statusCode = out.status;
        res.setHeader("content-type", "application/json");
        res.end(out.body === undefined ? "" : JSON.stringify(out.body));
      });
    });
    await new Promise<void>((r) => server!.listen(0, r));
    const port = (server!.address() as { port: number }).port;
    const client = new McpClient({} as never, "", "account", {
      apiKey: "e2a_test_key",
      baseUrl: `http://127.0.0.1:${port}`,
    });
    return { client, seen };
  }

  it("listTemplates GETs /v1/templates with the bearer and returns the wire page", async () => {
    const { client, seen } = await clientAgainstMock(() => ({
      status: 200,
      body: { items: [{ id: "tmpl_1", name: "W", subject: "s", body: "b", created_at: "x", updated_at: "x" }], next_cursor: null },
    }));
    const page = await client.listTemplates();
    expect(seen.method).toBe("GET");
    expect(seen.url).toBe("/v1/templates");
    expect(seen.auth).toBe("Bearer e2a_test_key");
    expect(page.items[0]!.id).toBe("tmpl_1");
  });

  it("createTemplate POSTs the snake_case body verbatim", async () => {
    const { client, seen } = await clientAgainstMock(() => ({
      status: 201,
      body: { id: "tmpl_new", name: "W", subject: "s", body: "b", created_at: "x", updated_at: "x" },
    }));
    await client.createTemplate({ from_starter: "welcome", alias: "my-welcome" });
    expect(seen.method).toBe("POST");
    expect(JSON.parse(seen.body!)).toEqual({ from_starter: "welcome", alias: "my-welcome" });
  });

  it("deleteTemplate handles the empty 204 and percent-encodes the id", async () => {
    const { client, seen } = await clientAgainstMock(() => ({ status: 204 }));
    await client.deleteTemplate("tmpl_a/b");
    expect(seen.method).toBe("DELETE");
    expect(seen.url).toBe("/v1/templates/tmpl_a%2Fb");
  });

  it("maps the /v1 error envelope to an E2AError with the machine code", async () => {
    const { client } = await clientAgainstMock(() => ({
      status: 400,
      body: { error: { code: "invalid_template", message: "template part body failed to parse" } },
    }));
    const err = await client.createTemplate({ name: "x", subject: "s", body: "{{#bad}}" }).catch((e) => e);
    expect(err).toBeInstanceOf(E2AError);
    expect(err.code).toBe("invalid_template");
    expect(err.retryable).toBe(false);
    expect(err.message).toContain("failed to parse");
  });

  it("marks 5xx as retryable", async () => {
    const { client } = await clientAgainstMock(() => ({ status: 503, body: {} }));
    const err = await client.listStarterTemplates().catch((e) => e);
    expect(err).toBeInstanceOf(E2AError);
    expect(err.retryable).toBe(true);
  });

  it("throws a directive plain error when the session has no raw creds", async () => {
    const bare = new McpClient({} as never, "", "account");
    const err = await bare.listTemplates().catch((e) => e);
    expect(err).toBeInstanceOf(Error);
    expect(err).not.toBeInstanceOf(E2AError);
    expect(String(err.message)).toContain("template operations are unavailable");
  });
});
