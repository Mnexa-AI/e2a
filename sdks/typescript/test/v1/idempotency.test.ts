import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { E2AApi, E2AClient } from "../../src/v1/index.js";

const BASE = "http://localhost:9998";

/**
 * Capture the headers passed to fetch() on the most recent call.
 * Returns a mock + a getter for the last set of headers seen.
 */
function spyFetch(status = 200, body: unknown = { status: "sent", message_id: "msg_xyz" }) {
  let lastHeaders: Record<string, string> = {};
  const mock = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    lastHeaders = (init?.headers as Record<string, string>) ?? {};
    return {
      ok: status >= 200 && status < 300,
      status,
      json: () => Promise.resolve(body),
      text: () => Promise.resolve(JSON.stringify(body ?? "")),
    } as Partial<Response> as Response;
  });
  return { mock, lastHeaders: () => lastHeaders };
}

describe("Idempotency-Key transport behavior", () => {
  const originalFetch = globalThis.fetch;
  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("sendEmail auto-generates a UUIDv4 Idempotency-Key when none supplied", async () => {
    const spy = spyFetch();
    globalThis.fetch = spy.mock;

    const api = new E2AApi({ apiKey: "e2a_test", baseUrl: BASE });
    await api.sendEmail({
      to: ["alice@example.com"],
      subject: "x",
      body: "y",
    });

    const headers = spy.lastHeaders();
    expect(headers["Idempotency-Key"]).toBeDefined();
    // UUIDv4 has the canonical 8-4-4-4-12 hex shape.
    expect(headers["Idempotency-Key"]).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i,
    );
  });

  it("sendEmail honors a caller-supplied Idempotency-Key verbatim", async () => {
    const spy = spyFetch();
    globalThis.fetch = spy.mock;

    const api = new E2AApi({ apiKey: "e2a_test", baseUrl: BASE });
    await api.sendEmail(
      { to: ["alice@example.com"], subject: "x", body: "y" },
      { idempotencyKey: "user-supplied-key-42" },
    );

    expect(spy.lastHeaders()["Idempotency-Key"]).toBe("user-supplied-key-42");
  });

  it("replyToMessage carries the Idempotency-Key header", async () => {
    const spy = spyFetch();
    globalThis.fetch = spy.mock;

    const api = new E2AApi({ apiKey: "e2a_test", baseUrl: BASE });
    await api.replyToMessage(
      "bot@test.dev",
      "msg_in_abc",
      { body: "hi" },
      { idempotencyKey: "reply-key-1" },
    );

    expect(spy.lastHeaders()["Idempotency-Key"]).toBe("reply-key-1");
  });

  it("sendEmail generates a different key on each call by default", async () => {
    const spy = spyFetch();
    globalThis.fetch = spy.mock;

    const api = new E2AApi({ apiKey: "e2a_test", baseUrl: BASE });
    await api.sendEmail({ to: ["a@b.com"], subject: "x", body: "y" });
    const k1 = spy.lastHeaders()["Idempotency-Key"];
    await api.sendEmail({ to: ["a@b.com"], subject: "x", body: "y" });
    const k2 = spy.lastHeaders()["Idempotency-Key"];

    expect(k1).toBeDefined();
    expect(k2).toBeDefined();
    expect(k1).not.toBe(k2);
  });

  it("high-level E2AClient.send threads idempotencyKey through", async () => {
    const spy = spyFetch();
    globalThis.fetch = spy.mock;

    const client = new E2AClient({
      apiKey: "e2a_test",
      baseUrl: BASE,
      agentEmail: "bot@test.dev",
    });
    await client.send(["alice@example.com"], "x", "y", {
      idempotencyKey: "client-key-99",
    });
    expect(spy.lastHeaders()["Idempotency-Key"]).toBe("client-key-99");
  });

  it("high-level E2AClient.reply threads idempotencyKey through", async () => {
    const spy = spyFetch();
    globalThis.fetch = spy.mock;

    const client = new E2AClient({
      apiKey: "e2a_test",
      baseUrl: BASE,
      agentEmail: "bot@test.dev",
    });
    await client.reply("msg_in_abc", "hi", { idempotencyKey: "client-reply-key" });
    expect(spy.lastHeaders()["Idempotency-Key"]).toBe("client-reply-key");
  });
});
