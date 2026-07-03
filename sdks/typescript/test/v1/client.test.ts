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

/** A `fetch` mock that pages: looks up the response by the request's `cursor`
 *  query param (absent → the "" key). Records the requested URLs for assertions. */
function pagingFetch(pages: Record<string, { items: unknown[]; next_cursor: string | null }>) {
  const calls: string[] = [];
  const fn = vi.fn(async (url: string) => {
    calls.push(url);
    const cursor = new URL(url).searchParams.get("cursor") ?? "";
    const text = JSON.stringify(pages[cursor]);
    return {
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      text: async () => text,
      blob: async () => new Blob([text]),
    } as unknown as Response;
  });
  return { fn, calls };
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

  it("maps a per-request timeout to E2AConnectionError", async () => {
    // fetch hangs until its abort signal fires — i.e. only the per-attempt
    // timeout can end it. Exercises the full client → retry → fetch → typed-error
    // path (Python has the equivalent test_request_timeout_surfaces_as_connection_error).
    globalThis.fetch = vi.fn(
      (_url: string, init?: { signal?: AbortSignal }) =>
        new Promise((_resolve, reject) => {
          const s = init?.signal;
          if (s?.aborted) return reject(s.reason);
          s?.addEventListener("abort", () => reject(s.reason), { once: true });
        }),
    ) as unknown as typeof fetch;
    const c = new E2AClient({ apiKey: "e2a_test", baseUrl: BASE, timeoutMs: 5, maxRetries: 0 });
    await expect(c.agents.get("bot@test.dev")).rejects.toBeInstanceOf(E2AConnectionError);
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
    expect(client.templates).toBeDefined();
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
    const res = await client.agents.create({ email: "new@test.dev" });
    const { url, init } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/agents");
    expect(JSON.parse(init.body as string)).toMatchObject({ email: "new@test.dev" });
    expect(res.id).toBe("ag_new");
  });

  it("agents.list returns an AutoPager over the agents array", async () => {
    globalThis.fetch = mockFetch(200, { items: [{ id: "ag_1", email: "bot@test.dev" }], next_cursor: null });
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

  it("messages.getAttachment hits GET …/attachments/{index} and maps the view", async () => {
    globalThis.fetch = mockFetch(200, {
      index: 0,
      filename: "report.pdf",
      content_type: "application/pdf",
      size_bytes: 14,
      download_url: "https://api.test/d?token=tok",
      expires_at: "2026-06-20T10:15:00Z",
    });
    const att = await client.messages.getAttachment("bot@test.dev", "msg_1", 0, { inline: true });
    const { url, init } = lastCall();
    expect(init.method).toBe("GET");
    expect(url).toContain("/messages/msg_1/attachments/0");
    expect(url).toContain("inline=true");
    expect(att.downloadUrl).toBe("https://api.test/d?token=tok");
    expect(att.sizeBytes).toBe(14);
  });

  // ── webhooks.fetchMessage: email.received is metadata-only ──────
  it("webhooks.fetchMessage resolves (recipient, message_id) → GET the full message", async () => {
    globalThis.fetch = mockFetch(200, { message_id: "msg_9", subject: "Hi", raw_message: "..." });
    const event = {
      id: "evt_1",
      type: "email.received",
      data: { message_id: "msg_9", recipient: "bot@test.dev" },
    };
    const msg = await client.webhooks.fetchMessage(event);
    const { url, init } = lastCall();
    expect(init.method).toBe("GET");
    // the fetch keys carried by the metadata-only event drive the URL
    expect(url).toContain("/messages/msg_9");
    expect(url).toContain("bot%40test.dev");
    expect(msg.messageId).toBe("msg_9");
  });

  it("webhooks.fetchMessage rejects a non-received event or missing fetch keys", async () => {
    expect(() =>
      client.webhooks.fetchMessage({ type: "email.bounced", data: { message_id: "m", recipient: "r" } }),
    ).toThrow(/email\.received/);
    expect(() =>
      client.webhooks.fetchMessage({ type: "email.received", data: { message_id: "m" } }),
    ).toThrow(/recipient/);
  });

  // ── Reviews: account-scoped, id-addressed (no inbox email) ──────
  it("reviews.approve hits POST /v1/reviews/{id}/approve (no inbox email) + mints Idempotency-Key", async () => {
    globalThis.fetch = mockFetch(200, { message_id: "msg_r1", status: "sent" });
    await client.reviews.approve("msg_r1");
    const { url, init, headers } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/reviews/msg_r1/approve");
    expect(url).not.toContain("/agents/");
    expect(headers["Idempotency-Key"]).toBeTruthy();
  });

  it("reviews.reject hits POST /v1/reviews/{id}/reject", async () => {
    globalThis.fetch = mockFetch(200, { message_id: "msg_r2", status: "rejected" });
    await client.reviews.reject("msg_r2", { reason: "spam" } as never);
    const { url, init } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/reviews/msg_r2/reject");
  });

  it("reviews.list reads GET /v1/reviews (single page)", async () => {
    globalThis.fetch = mockFetch(200, {
      items: [{ id: "msg_r1", agent: "bot@test.dev", direction: "outbound" }],
      next_cursor: null,
    });
    const items = await client.reviews.list().toArray({ limit: 50 });
    expect(items.map((r) => r.id)).toEqual(["msg_r1"]);
    expect(lastCall().url).toContain("/v1/reviews");
  });

  // ── Pagination: cursor-walking endpoints ────────────────────────
  // conversations/events/suppressions take a `cursor` query param; the pager
  // must replay next_cursor until null, threading the cursor each follow-up.
  // (messages is covered above.)

  it("conversations.list threads next_cursor across pages", async () => {
    const { fn, calls } = pagingFetch({
      "": { items: [{ conversation_id: "conv_1" }], next_cursor: "cur_2" },
      cur_2: { items: [{ conversation_id: "conv_2" }], next_cursor: null },
    });
    globalThis.fetch = fn as unknown as typeof fetch;
    const items = await client.conversations.list("bot@test.dev").toArray({ limit: 50 });
    expect(items.map((c) => c.conversationId)).toEqual(["conv_1", "conv_2"]);
    expect(calls).toHaveLength(2);
    expect(calls[1]).toContain("cursor=cur_2");
  });

  it("events.list threads next_cursor across pages", async () => {
    const { fn, calls } = pagingFetch({
      "": { items: [{ id: "evt_1" }], next_cursor: "cur_2" },
      cur_2: { items: [{ id: "evt_2" }], next_cursor: null },
    });
    globalThis.fetch = fn as unknown as typeof fetch;
    const items = await client.events.list().toArray({ limit: 50 });
    expect(items.map((e) => e.id)).toEqual(["evt_1", "evt_2"]);
    expect(calls).toHaveLength(2);
    expect(calls[1]).toContain("cursor=cur_2");
  });

  it("account.suppressions.list threads next_cursor across pages", async () => {
    const { fn, calls } = pagingFetch({
      "": { items: [{ address: "a@x.com" }], next_cursor: "cur_2" },
      cur_2: { items: [{ address: "b@x.com" }], next_cursor: null },
    });
    globalThis.fetch = fn as unknown as typeof fetch;
    const items = await client.account.suppressions.list().toArray({ limit: 50 });
    expect(items.map((s) => s.address)).toEqual(["a@x.com", "b@x.com"]);
    expect(calls).toHaveLength(2);
    expect(calls[1]).toContain("cursor=cur_2");
  });

  // ── Pagination: cursorless (single-page) endpoints ──────────────
  // agents/domains/webhooks have no cursor param. Even if the server returns a
  // non-null next_cursor, the pager must stop after one page (it can't forward a
  // cursor; surfacing one would loop page 1 and trip the cycle guard). This locks
  // the intentional cursor-drop. (webhooks.deliveries is covered below.)

  it.each([
    ["agents", () => client.agents.list(), { id: "ag_1", email: "bot@test.dev" }],
    ["domains", () => client.domains.list(), { domain: "test.dev" }],
    ["webhooks", () => client.webhooks.list(), { id: "wh_1" }],
    ["templates", () => client.templates.list(), { id: "tmpl_1", name: "Welcome" }],
    ["templates.listStarters", () => client.templates.listStarters(), { alias: "welcome" }],
  ] as const)("%s.list stops after one page even if the server returns a next_cursor", async (_name, lister, item) => {
    let calls = 0;
    globalThis.fetch = vi.fn(async () => {
      calls++;
      const text = JSON.stringify({ items: [item], next_cursor: "should_be_ignored" });
      return {
        status: 200,
        headers: new Headers({ "content-type": "application/json" }),
        text: async () => text,
        blob: async () => new Blob([text]),
      } as unknown as Response;
    }) as unknown as typeof fetch;
    const items = await lister().toArray({ limit: 100 });
    expect(items).toHaveLength(1);
    expect(calls).toBe(1);
  });

  // ── Templates (beta) ────────────────────────────────────────────
  // camelCase model fields ↔ snake_case wire (the generated serializer maps
  // them), plus the two starter-catalog reads.

  it("templates.get hits GET /v1/templates/{id} and maps snake_case wire fields", async () => {
    globalThis.fetch = mockFetch(200, {
      id: "tmpl_1",
      name: "Welcome",
      subject: "Welcome, {{name}}!",
      body: "Hi {{name}}",
      html_body: "<p>Hi {{name}}</p>",
      from_starter_alias: "welcome",
      from_starter_version: "1",
      created_at: "2026-06-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
    });
    const tmpl = await client.templates.get("tmpl_1");
    const { url, init } = lastCall();
    expect(init.method).toBe("GET");
    expect(url).toContain("/v1/templates/tmpl_1");
    expect(tmpl.htmlBody).toBe("<p>Hi {{name}}</p>");
    expect(tmpl.fromStarterAlias).toBe("welcome");
    expect(tmpl.fromStarterVersion).toBe("1");
  });

  it("templates.create POSTs camelCase input as the snake_case wire body", async () => {
    globalThis.fetch = mockFetch(201, {
      id: "tmpl_new", name: "Approvals", subject: "s", body: "b",
      created_at: "2026-06-01T00:00:00Z", updated_at: "2026-06-01T00:00:00Z",
    });
    await client.templates.create({ fromStarter: "approval-request", alias: "my-approvals" });
    const { url, init } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/templates");
    // Exactly the caller's fields reach the wire, snake_cased — no fabricated
    // subject/body keys that would trip the server's from_starter exclusivity.
    expect(JSON.parse(init.body as string)).toEqual({
      from_starter: "approval-request",
      alias: "my-approvals",
    });
  });

  it("templates.update PATCHes the id and keeps an explicit html_body:'' clear", async () => {
    globalThis.fetch = mockFetch(200, {
      id: "tmpl_1", name: "Welcome", subject: "New {{x}}", body: "b",
      created_at: "2026-06-01T00:00:00Z", updated_at: "2026-06-02T00:00:00Z",
    });
    await client.templates.update("tmpl_1", { subject: "New {{x}}", htmlBody: "" });
    const { url, init } = lastCall();
    expect(init.method).toBe("PATCH");
    expect(url).toContain("/v1/templates/tmpl_1");
    expect(JSON.parse(init.body as string)).toEqual({ subject: "New {{x}}", html_body: "" });
  });

  it("templates.delete issues DELETE /v1/templates/{id}", async () => {
    globalThis.fetch = mockFetch(204);
    await client.templates.delete("tmpl_1");
    const { url, init } = lastCall();
    expect(init.method).toBe("DELETE");
    expect(url).toContain("/v1/templates/tmpl_1");
  });

  it("templates.validate POSTs to /v1/templates/validate and maps the response", async () => {
    globalThis.fetch = mockFetch(200, {
      valid: true,
      errors: [],
      rendered: { subject: "Welcome, Ada!", body: "Hi Ada", html_body: "<p>Hi Ada</p>" },
      // suggested_data is nested (dot-path variables emit nested objects).
      suggested_data: { user: { name: "example" } },
    });
    const res = await client.templates.validate({
      subject: "Welcome, {{user.name}}!",
      body: "Hi {{user.name}}",
      testData: { user: { name: "Ada" } },
    });
    const { url, init } = lastCall();
    expect(init.method).toBe("POST");
    expect(url).toContain("/v1/templates/validate");
    expect(JSON.parse(init.body as string)).toEqual({
      subject: "Welcome, {{user.name}}!",
      body: "Hi {{user.name}}",
      test_data: { user: { name: "Ada" } },
    });
    expect(res.valid).toBe(true);
    expect(res.rendered?.htmlBody).toBe("<p>Hi Ada</p>");
    expect(res.suggestedData).toEqual({ user: { name: "example" } });
  });

  it("templates.getStarter hits GET /v1/starter-templates/{alias} with body sources", async () => {
    globalThis.fetch = mockFetch(200, {
      alias: "approval-request",
      name: "Approval request",
      description: "Ask a human to approve an action.",
      version: "1",
      subject: "Approval needed: {{action}}",
      body: "Approve: {{approve_url}}",
      html_body: '<a href="{{approve_url}}">Approve</a>',
      variables: [
        { name: "approve_url", required: true, raw: false, description: "d", example: "https://x/approve" },
      ],
    });
    const starter = await client.templates.getStarter("approval-request");
    const { url, init } = lastCall();
    expect(init.method).toBe("GET");
    expect(url).toContain("/v1/starter-templates/approval-request");
    expect(starter.htmlBody).toContain("{{approve_url}}");
    expect(starter.variables[0].name).toBe("approve_url");
  });

  it("maps a template-part parse failure to E2AValidationError with the machine code", async () => {
    globalThis.fetch = mockFetch(400, {
      error: { code: "invalid_template", message: "template part body failed to parse" },
    });
    const err = await client.templates
      .create({ name: "x", subject: "s", body: "{{#bad}}" })
      .catch((e) => e);
    expect(err).toBeInstanceOf(E2AValidationError);
    expect(err.code).toBe("invalid_template");
    expect(err.retryable).toBe(false);
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

    globalThis.fetch = mockFetch(200, { items: [{ address: "blocked@x.com" }], next_cursor: null });
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

  it("listen() requires an email", () => {
    expect(() => client.listen("")).toThrow(/email is required/);
  });
});
