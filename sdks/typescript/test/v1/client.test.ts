import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { E2AClient } from "../../src/v1/client.js";
import {
  E2AError,
  E2ANotFoundError,
  E2AConflictError,
  E2AValidationError,
  E2AConnectionError,
} from "../../src/v1/errors.js";

// These exercise the full hand-written stack — namespaced resources →
// generated `Promise*Api` → bearer auth → retry layer → fetch → envelope
// unwrap → typed-error mapping — by mocking the global `fetch` the generated
// `IsomorphicFetchHttpLibrary` calls. That's deliberately closer to the wire
// than mocking the generated API: header/URL/body encoding all get covered.

const BASE = "http://localhost:9998";

/** A minimal `fetch` Response the generated http library understands:
 *  it reads `.status`, iterates `.headers`, and calls `.text()` / `.blob()`. */
function mockFetch(status: number, jsonBody?: unknown, headers: Record<string, string> = {}) {
  const text = JSON.stringify(jsonBody ?? {});
  return vi.fn(async () => ({
    status,
    headers: new Headers({ "content-type": "application/json", ...headers }),
    text: async () => text,
    blob: async () => new Blob([text]),
  }) as unknown as Response);
}

function lastCall() {
  const mock = globalThis.fetch as ReturnType<typeof vi.fn>;
  const [url, init] = mock.mock.calls[mock.mock.calls.length - 1] as [string, RequestInit];
  return { url, init, headers: init.headers as Record<string, string> };
}

describe("E2AClient", () => {
  const originalFetch = globalThis.fetch;
  let client: E2AClient;
  let savedEnv: Record<string, string | undefined>;

  beforeEach(() => {
    savedEnv = {
      E2A_API_KEY: process.env.E2A_API_KEY,
      E2A_BASE_URL: process.env.E2A_BASE_URL,
      E2A_AGENT_EMAIL: process.env.E2A_AGENT_EMAIL,
    };
    delete process.env.E2A_API_KEY;
    delete process.env.E2A_BASE_URL;
    delete process.env.E2A_AGENT_EMAIL;
    client = new E2AClient({ apiKey: "e2a_test", baseUrl: BASE });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    for (const [k, v] of Object.entries(savedEnv)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  });

  // ── Construction ────────────────────────────────────────────────

  it("requires an apiKey (throws when none is given or in env)", () => {
    expect(() => new E2AClient({ baseUrl: BASE })).toThrow(/apiKey is required/);
  });

  it("falls back to E2A_API_KEY from the environment", () => {
    process.env.E2A_API_KEY = "e2a_env";
    expect(() => new E2AClient({ baseUrl: BASE })).not.toThrow();
  });

  it("exposes the namespaced resources", () => {
    expect(client.agents).toBeDefined();
    expect(client.messages).toBeDefined();
    expect(client.conversations).toBeDefined();
    expect(client.domains).toBeDefined();
    expect(client.events).toBeDefined();
    expect(client.webhooks).toBeDefined();
    expect(client.account).toBeDefined();
    expect(client.account.suppressions).toBeDefined();
  });

  // ── Auth + transport ────────────────────────────────────────────

  it("sends the bearer Authorization header", async () => {
    globalThis.fetch = mockFetch(200, { id: "ag_1", email: "bot@test.dev" });
    await client.agents.get("bot@test.dev");
    expect(lastCall().headers["Authorization"]).toBe("Bearer e2a_test");
  });

  // ── Agents ──────────────────────────────────────────────────────

  it("agents.get hits GET /v1/agents/{address} (URL-encoded)", async () => {
    globalThis.fetch = mockFetch(200, { id: "ag_1", email: "bot@test.dev" });
    const agent = await client.agents.get("bot@test.dev");
    const { url, init } = lastCall();
    expect(init.method).toBe("GET");
    expect(url).toContain("/v1/agents/");
    expect(url).toContain("bot%40test.dev");
    expect(agent.email).toBe("bot@test.dev");
  });

  it("agents.create POSTs the body to /v1/agents", async () => {
    globalThis.fetch = mockFetch(201, { id: "ag_new", email: "new@test.dev" });
    const res = await client.agents.create({ email: "new@test.dev", agent_mode: "local" } as never);
    const { url, init } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/agents");
    expect(JSON.parse(init.body as string)).toMatchObject({ email: "new@test.dev" });
    expect(res.id).toBe("ag_new");
  });

  it("agents.list returns an AutoPager over the agents array", async () => {
    globalThis.fetch = mockFetch(200, { agents: [{ id: "ag_1", email: "bot@test.dev" }] });
    const items = await client.agents.list().toArray({ limit: 10 });
    expect(items).toHaveLength(1);
    expect(items[0].email).toBe("bot@test.dev");
  });

  // ── Messages: idempotency + pagination ──────────────────────────

  it("messages.send mints an Idempotency-Key for the POST", async () => {
    globalThis.fetch = mockFetch(200, { message_id: "msg_s1", status: "sent" });
    await client.messages.send("bot@test.dev", { to: ["a@x.com"], subject: "Hi", body: "Hello" } as never);
    const { url, init, headers } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/agents/bot%40test.dev/messages");
    expect(headers["Idempotency-Key"]).toBeTruthy();
  });

  it("messages.send uses a caller-supplied idempotency key", async () => {
    globalThis.fetch = mockFetch(200, { message_id: "msg_s2", status: "sent" });
    await client.messages.send(
      "bot@test.dev",
      { to: ["a@x.com"], subject: "Hi", body: "Hello" } as never,
      { idempotencyKey: "caller-key-123" },
    );
    expect(lastCall().headers["Idempotency-Key"]).toBe("caller-key-123");
  });

  it("messages.list threads next_cursor across pages", async () => {
    const calls: string[] = [];
    globalThis.fetch = vi.fn(async (url: string) => {
      calls.push(url);
      const cursor = new URL(url).searchParams.get("cursor");
      const text = cursor
        ? JSON.stringify({ items: [{ message_id: "msg_2" }], next_cursor: null })
        : JSON.stringify({ items: [{ message_id: "msg_1" }], next_cursor: "cur_2" });
      return {
        status: 200,
        headers: new Headers({ "content-type": "application/json" }),
        text: async () => text,
        blob: async () => new Blob([text]),
      } as unknown as Response;
    }) as unknown as typeof fetch;

    const items = await client.messages.list("bot@test.dev").toArray({ limit: 50 });
    expect(items.map((m) => m.messageId)).toEqual(["msg_1", "msg_2"]);
    expect(calls).toHaveLength(2);
    expect(calls[1]).toContain("cursor=cur_2");
  });

  // ── Error mapping ───────────────────────────────────────────────

  it("maps a 404 envelope to E2ANotFoundError", async () => {
    globalThis.fetch = mockFetch(404, { error: { code: "agent_not_found", message: "no such agent" } });
    await expect(client.agents.get("ghost@test.dev")).rejects.toBeInstanceOf(E2ANotFoundError);
  });

  it("maps a 409 envelope to E2AConflictError", async () => {
    globalThis.fetch = mockFetch(409, { error: { code: "domain_exists", message: "already registered" } });
    await expect(
      client.domains.create({ domain: "dup.dev" } as never),
    ).rejects.toBeInstanceOf(E2AConflictError);
  });

  it("maps a 422 envelope to E2AValidationError", async () => {
    globalThis.fetch = mockFetch(422, { error: { code: "invalid_request", message: "bad input" } });
    await expect(
      client.agents.create({ email: "" } as never),
    ).rejects.toBeInstanceOf(E2AValidationError);
  });

  it("surfaces the envelope code/message/requestId on the typed error", async () => {
    globalThis.fetch = mockFetch(
      404,
      { error: { code: "agent_not_found", message: "no such agent" } },
      { "x-request-id": "req_abc" },
    );
    try {
      await client.agents.get("ghost@test.dev");
      throw new Error("expected to throw");
    } catch (e) {
      expect(e).toBeInstanceOf(E2AError);
      const err = e as E2AError;
      expect(err.code).toBe("agent_not_found");
      expect(err.message).toBe("no such agent");
      expect(err.requestId).toBe("req_abc");
      expect(err.status).toBe(404);
    }
  });

  // ── webhooks.deliveries pager termination (review finding) ──────

  it("webhooks.deliveries terminates even if the page carries a next_cursor", async () => {
    // The endpoint has no cursor param; surfacing next_cursor would make the
    // pager re-fetch the same page and trip the cycle guard. It must stop after
    // one page regardless.
    let calls = 0;
    globalThis.fetch = vi.fn(async () => {
      calls++;
      const text = JSON.stringify({ items: [{ id: "del_1" }], next_cursor: "should_be_ignored" });
      return {
        status: 200,
        headers: new Headers({ "content-type": "application/json" }),
        text: async () => text,
        blob: async () => new Blob([text]),
      } as unknown as Response;
    }) as unknown as typeof fetch;
    const items = await client.webhooks.deliveries("wh_1").toArray({ limit: 100 });
    expect(items).toHaveLength(1);
    expect(calls).toBe(1); // single page, no loop
  });

  // ── account + suppressions smoke (thin passthroughs) ────────────

  it("account.get / export / suppressions hit the right operations", async () => {
    globalThis.fetch = mockFetch(200, { plan: "free" });
    await client.account.get();
    expect(lastCall().url).toContain("/v1/account");

    globalThis.fetch = mockFetch(200, { suppressions: [{ address: "blocked@x.com" }] });
    const supp = await client.account.suppressions.list().toArray({ limit: 10 });
    expect(supp).toHaveLength(1);
    expect(lastCall().url).toContain("/v1/account/suppressions");
  });

  // ── connection-error path through call() ────────────────────────

  it("maps a transport-level failure to E2AConnectionError", async () => {
    // fetch rejects (DNS/refused/abort) with no HTTP response. With retries
    // off, the retry layer rethrows and call() wraps it as a connection error.
    const c = new E2AClient({ apiKey: "e2a_test", baseUrl: BASE, maxRetries: 0 });
    globalThis.fetch = vi.fn(async () => { throw new TypeError("fetch failed"); }) as unknown as typeof fetch;
    await expect(c.agents.get("bot@test.dev")).rejects.toBeInstanceOf(E2AConnectionError);
  });

  // ── listen() ────────────────────────────────────────────────────

  it("listen() requires an address (arg or E2A_AGENT_EMAIL)", () => {
    expect(() => client.listen()).toThrow(/agentEmail is required/);
  });
});
