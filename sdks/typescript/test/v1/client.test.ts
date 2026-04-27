import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { E2AClient } from "../../src/v1/client.js";
import { InboundEmail } from "../../src/v1/inbound-email.js";
import type { WebhookPayload } from "../../src/v1/inbound-email.js";

const BASE = "http://localhost:9998";

function mockFetch(status: number, body?: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body ?? "")),
  } as Partial<Response> as Response);
}

describe("E2AClient", () => {
  const originalFetch = globalThis.fetch;
  let client: E2AClient;

  beforeEach(() => {
    client = new E2AClient({
      apiKey: "e2a_test",
      baseUrl: BASE,
      agentEmail: "bot@test.dev",
    });
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  it("requires agentEmail at point of use", async () => {
    const prev = process.env.E2A_AGENT_EMAIL;
    delete process.env.E2A_AGENT_EMAIL;
    try {
      const c = new E2AClient({ apiKey: "e2a_test", baseUrl: BASE });
      globalThis.fetch = mockFetch(200, { messages: [] });
      await expect(c.listMessages()).rejects.toThrow("agentEmail is required");
    } finally {
      if (prev !== undefined) process.env.E2A_AGENT_EMAIL = prev;
    }
  });

  it("falls back to E2A_AGENT_EMAIL env var when agentEmail not passed", () => {
    const prev = process.env.E2A_AGENT_EMAIL;
    process.env.E2A_AGENT_EMAIL = "env-default@test.dev";
    try {
      const c = new E2AClient({ apiKey: "e2a_test", baseUrl: BASE });
      expect(c.agentEmail).toBe("env-default@test.dev");
      // Explicit arg still wins.
      const c2 = new E2AClient({
        apiKey: "e2a_test",
        baseUrl: BASE,
        agentEmail: "explicit@test.dev",
      });
      expect(c2.agentEmail).toBe("explicit@test.dev");
    } finally {
      if (prev === undefined) delete process.env.E2A_AGENT_EMAIL;
      else process.env.E2A_AGENT_EMAIL = prev;
    }
  });

  it("exposes the raw api", () => {
    expect(client.api).toBeDefined();
    expect(client.api.apiKey).toBe("e2a_test");
  });

  // ── Agents ──────────────────────────────────────────────────

  it("listAgents", async () => {
    globalThis.fetch = mockFetch(200, { agents: [{ id: "ag_1", email: "bot@test.dev" }] });
    const res = await client.listAgents();
    expect(res.agents![0].email).toBe("bot@test.dev");
  });

  it("registerAgent", async () => {
    globalThis.fetch = mockFetch(201, { id: "ag_new", email: "new@test.dev" });
    const res = await client.registerAgent({ email: "new@test.dev", agent_mode: "local" });
    expect(res.id).toBe("ag_new");
  });

  it("getAgent uses default email", async () => {
    globalThis.fetch = mockFetch(200, { id: "ag_1", email: "bot@test.dev" });
    await client.getAgent();
    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("bot%40test.dev");
  });

  it("getAgent accepts override", async () => {
    globalThis.fetch = mockFetch(200, { id: "ag_2", email: "other@test.dev" });
    await client.getAgent("other@test.dev");
    const url = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(url).toContain("other%40test.dev");
  });

  it("deleteAgent", async () => {
    globalThis.fetch = mockFetch(200, {});
    await expect(client.deleteAgent()).resolves.toBeUndefined();
  });

  // ── Messages ────────────────────────────────────────────────

  it("listMessages", async () => {
    globalThis.fetch = mockFetch(200, { messages: [{ message_id: "msg_1" }] });
    const res = await client.listMessages({ status: "unread" });
    expect(res.messages).toHaveLength(1);
  });

  it("getMessage returns InboundEmail", async () => {
    globalThis.fetch = mockFetch(200, {
      message_id: "msg_1",
      from: "alice@example.com",
      to: ["bot@test.dev"],
      recipient: "bot@test.dev",
      subject: "Hello",
      raw_message: Buffer.from(
        "From: alice@example.com\r\nTo: bot@test.dev\r\nSubject: Hello\r\n\r\nHi!",
      ).toString("base64"),
      auth_headers: { "X-E2A-Auth-Verified": "true" },
    });
    const email = await client.getMessage("msg_1");
    expect(email).toBeInstanceOf(InboundEmail);
    expect(email.messageId).toBe("msg_1");
    expect(email.sender).toBe("alice@example.com");
    expect(email.subject).toBe("Hello");
    expect(email.textBody).toBe("Hi!");
    expect(email.auth.verified).toBe(true);
    expect(email.isVerified).toBe(true);
  });

  it("parse returns InboundEmail from MessageDetail", async () => {
    const email = await client.parse({
      message_id: "msg_2",
      from: "bob@example.com",
      to: ["bot@test.dev"],
      recipient: "bot@test.dev",
      raw_message: Buffer.from(
        "From: bob@example.com\r\nTo: bot@test.dev\r\nSubject: Test\r\n\r\nBody",
      ).toString("base64"),
      auth_headers: {
        "X-E2A-Auth-Verified": "false",
        "X-E2A-Auth-Sender": "bob@example.com",
        "X-E2A-Auth-Entity-Type": "human",
      },
    });
    expect(email).toBeInstanceOf(InboundEmail);
    // Unverified by default — claim getters throw, but unverifiedPayload
    // and the always-available auth/isVerified/verified work.
    expect(email.unverifiedPayload.messageId).toBe("msg_2");
    expect(email.auth.verified).toBe(false);
    expect(email.auth.sender).toBe("bob@example.com");
    expect(email.auth.entityType).toBe("human");
    expect(email.isVerified).toBe(false);
    expect(email.verified).toBe(false);
  });

  it("parse accepts a webhook payload object", async () => {
    const payload: WebhookPayload = {
      message_id: "msg_webhook",
      from: "carol@example.com",
      to: ["bot@test.dev"],
      recipient: "bot@test.dev",
      raw_message: Buffer.from(
        "From: carol@example.com\r\nTo: bot@test.dev\r\nSubject: Webhook\r\n\r\nPayload body",
      ).toString("base64"),
      auth_headers: {
        "X-E2A-Auth-Verified": "true",
        "X-E2A-Auth-Sender": "carol@example.com",
      },
      received_at: "2026-03-29T00:00:00Z",
    };

    const email = await client.parse(payload);
    expect(email).toBeInstanceOf(InboundEmail);
    // Unverified by default — read via unverifiedPayload.
    expect(email.unverifiedPayload.messageId).toBe("msg_webhook");
    expect(email.unverifiedPayload.sender).toBe("carol@example.com");
    expect(email.unverifiedPayload.textBody).toBe("Payload body");
    // auth is always accessible.
    expect(email.auth.sender).toBe("carol@example.com");
  });

  it("parse exposes structured to/cc lists from the server payload", async () => {
    const payload: WebhookPayload = {
      message_id: "msg_lists",
      from: "alice@example.com",
      to: ["bot-a@test.dev", "bot-b@test.dev"],
      cc: ["watcher@example.com"],
      recipient: "bot-a@test.dev",
      raw_message: Buffer.from(
        "From: alice@example.com\r\nTo: bot-a@test.dev, bot-b@test.dev\r\n" +
        "Cc: watcher@example.com\r\nSubject: Group\r\n\r\nbody",
      ).toString("base64"),
    };

    const email = await client.parse(payload);
    // Unverified — read via unverifiedPayload.
    expect(email.unverifiedPayload.to).toEqual(["bot-a@test.dev", "bot-b@test.dev"]);
    expect(email.unverifiedPayload.cc).toEqual(["watcher@example.com"]);
    expect(email.unverifiedPayload.recipient).toBe("bot-a@test.dev");
  });

  it("parse accepts JSON string and Buffer webhook payloads", async () => {
    const payload = {
      message_id: "msg_string",
      from: "dana@example.com",
      to: ["bot@test.dev"],
      recipient: "bot@test.dev",
      raw_message: Buffer.from(
        "From: dana@example.com\r\nTo: bot@test.dev\r\nSubject: Buffer\r\n\r\nBuffer body",
      ).toString("base64"),
      auth_headers: { "X-E2A-Auth-Verified": "false" },
    };

    const fromString = await client.parse(JSON.stringify(payload));
    expect(fromString.unverifiedPayload.messageId).toBe("msg_string");
    expect(fromString.unverifiedPayload.textBody).toBe("Buffer body");

    const fromBuffer = await client.parse(
      Buffer.from(JSON.stringify({ ...payload, message_id: "msg_buffer" })),
    );
    expect(fromBuffer.unverifiedPayload.messageId).toBe("msg_buffer");
    expect(fromBuffer.unverifiedPayload.sender).toBe("dana@example.com");
  });

  it("reply", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent", message_id: "msg_r1" });
    const res = await client.reply("msg_1", "thanks");
    expect(res.status).toBe("sent");
    const body = JSON.parse((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body);
    expect(body.body).toBe("thanks");
  });

  it("reply with html and conversationId", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent" });
    await client.reply("msg_1", "thanks", {
      htmlBody: "<p>thanks</p>",
      conversationId: "conv_1",
    });
    const body = JSON.parse((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body);
    expect(body.html_body).toBe("<p>thanks</p>");
    expect(body.conversation_id).toBe("conv_1");
  });

  it("reply with replyAll, cc, bcc", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent" });
    await client.reply("msg_1", "thanks", {
      replyAll: true,
      cc: ["bob@example.com"],
      bcc: ["carol@example.com"],
    });
    const body = JSON.parse((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body);
    expect(body.reply_all).toBe(true);
    expect(body.cc).toEqual(["bob@example.com"]);
    expect(body.bcc).toEqual(["carol@example.com"]);
  });

  // ── Domains ─────────────────────────────────────────────────

  it("listDomains", async () => {
    globalThis.fetch = mockFetch(200, { domains: [] });
    const res = await client.listDomains();
    expect(res.domains).toHaveLength(0);
  });

  it("registerDomain", async () => {
    globalThis.fetch = mockFetch(201, { domain: "new.dev", verified: false });
    const res = await client.registerDomain("new.dev");
    expect(res.verified).toBe(false);
    const body = JSON.parse((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body);
    expect(body.domain).toBe("new.dev");
  });

  it("deleteDomain", async () => {
    globalThis.fetch = mockFetch(204);
    await expect(client.deleteDomain("test.dev")).resolves.toBeUndefined();
  });

  it("verifyDomain", async () => {
    globalThis.fetch = mockFetch(200, { domain: "test.dev", verified: true });
    const res = await client.verifyDomain("test.dev");
    expect(res.verified).toBe(true);
  });

  // ── Send ────────────────────────────────────────────────────

  it("send", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent", message_id: "msg_s1" });
    const res = await client.send(["alice@example.com"], "Hi", "Hello");
    expect(res.status).toBe("sent");
    const body = JSON.parse((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body);
    expect(body.from).toBe("bot@test.dev");
    expect(body.to).toEqual(["alice@example.com"]);
    expect(body.subject).toBe("Hi");
  });

  it("send with htmlBody, cc, bcc", async () => {
    globalThis.fetch = mockFetch(200, { status: "sent" });
    await client.send(["alice@example.com"], "Hi", "Hello", {
      htmlBody: "<p>Hello</p>",
      cc: ["bob@example.com"],
      bcc: ["carol@example.com"],
    });
    const body = JSON.parse((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0][1].body);
    expect(body.html_body).toBe("<p>Hello</p>");
    expect(body.cc).toEqual(["bob@example.com"]);
    expect(body.bcc).toEqual(["carol@example.com"]);
  });
});
