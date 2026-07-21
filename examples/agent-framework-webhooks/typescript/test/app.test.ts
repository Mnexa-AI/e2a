import { createHmac } from "node:crypto";
import { request as httpRequest } from "node:http";

import request from "supertest";
import { describe, expect, it, vi } from "vitest";
import type { InboundEmail } from "@e2a/sdk/v1";

import {
  MAX_WEBHOOK_BODY_BYTES,
  closeApp,
  createApp,
  selectAgent,
  validateProviderConfig,
} from "../src/app.js";
import { EventDeduper } from "../src/delivery-state.js";

const SECRET = "whsec_app_test";

function delivery(eventId = "evt_app"): { body: Buffer; signature: string } {
  const body = Buffer.from(JSON.stringify({
    id: eventId,
    type: "email.received",
    schema_version: "1",
    created_at: "2026-07-20T12:00:00.000Z",
    data: {
      message_id: "msg_app",
      agent_email: "agent@example.com",
      direction: "inbound",
      conversation_id: "conv_app",
      header_from: "sender@example.net",
      envelope_from: "sender@example.net",
      verified_domain: "example.net",
      to: ["agent@example.com"], cc: [], reply_to: [],
      delivered_to: "agent@example.com",
      subject: "Hello", received_at: "2026-07-20T12:00:00.000Z",
      attachments: [],
      authentication: {
        spf: { status: "pass", domain: "example.net", aligned: true },
        dkim: [],
        dmarc: { status: "pass", domain: "example.net", policy: "reject", aligned_by: ["spf"] },
      },
    },
  }));
  const timestamp = Math.floor(Date.now() / 1_000).toString();
  const digest = createHmac("sha256", SECRET).update(timestamp).update(".").update(body).digest("hex");
  return { body, signature: `t=${timestamp},v1=${digest}` };
}

function appCollaborators() {
  const reply = vi.fn().mockResolvedValue({ status: "accepted" });
  const email = {
    conversationId: "conv_app", from: "sender@example.net", inbox: "agent@example.com",
    subject: "Hello", text: "Question", verified: true, flagged: false, reply,
  } as unknown as InboundEmail;
  const inbound = { fromEvent: vi.fn().mockResolvedValue(email) };
  const agent = { reply: vi.fn().mockResolvedValue("Answer"), close: vi.fn() };
  return { inbound, agent, reply };
}

async function chunkedStatus(app: ReturnType<typeof createApp>, chunks: Buffer[]): Promise<number> {
  const server = app.listen(0, "127.0.0.1");
  await new Promise<void>((resolve) => server.once("listening", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("expected a TCP listener");
  try {
    return await new Promise<number>((resolve, reject) => {
      const outgoing = httpRequest({
        host: "127.0.0.1", port: address.port, path: "/webhook", method: "POST",
        headers: { "content-type": "application/json", "transfer-encoding": "chunked" },
      }, (incoming) => {
        incoming.resume();
        incoming.once("end", () => resolve(incoming.statusCode ?? 0));
      });
      outgoing.once("error", reject);
      for (const chunk of chunks) outgoing.write(chunk);
      outgoing.end();
    });
  } finally {
    await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
}

describe("createApp", () => {
  it("reports healthy only after runtime configuration is ready", async () => {
    const c = appCollaborators();
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent });
    await request(app).get("/health").expect(200, { status: "ok" });
  });

  it("returns 503 readiness when startup configuration is invalid", async () => {
    const app = createApp({ env: {}, framework: "openai" });
    await request(app).get("/health").expect(503, { status: "unavailable" });
    await request(app).post("/webhook").set("content-type", "application/json").send("{}").expect(503);
  });

  it("maps signature failures to 401 before downstream work", async () => {
    const c = appCollaborators();
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent });
    await request(app)
      .post("/webhook").set("content-type", "application/json")
      .set("X-E2A-Signature", "t=1,v1=bad").send(delivery().body.toString("utf8"))
      .expect(401, { error: "invalid signature" });
    expect(c.inbound.fromEvent).not.toHaveBeenCalled();
  });

  it("processes the exact signed bytes and sends one bound reply", async () => {
    const c = appCollaborators();
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent });
    const signed = delivery();
    await request(app).post("/webhook").set("content-type", "application/json")
      .set("X-E2A-Signature", signed.signature).send(signed.body.toString("utf8"))
      .expect(200, { status: "replied", conversationId: "conv_app" });
    expect(c.reply).toHaveBeenCalledOnce();
  });

  it("maps a processing collision to 503", async () => {
    const c = appCollaborators();
    const deduper = new EventDeduper();
    await deduper.claim("evt_app");
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent, deduper });
    const signed = delivery();
    await request(app).post("/webhook").set("content-type", "application/json")
      .set("X-E2A-Signature", signed.signature).send(signed.body.toString("utf8"))
      .expect(503, { error: "delivery in progress" });
  });

  it("rejects an oversized declared body without processing", async () => {
    const c = appCollaborators();
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent });
    await request(app).post("/webhook").set("content-type", "application/json")
      .send("x".repeat(MAX_WEBHOOK_BODY_BYTES + 1)).expect(413);
    expect(c.inbound.fromEvent).not.toHaveBeenCalled();
  });

  it("rejects an oversized chunked body without processing", async () => {
    const c = appCollaborators();
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent });
    const chunks = [Buffer.alloc(MAX_WEBHOOK_BODY_BYTES, 0x78), Buffer.from("x")];
    await expect(chunkedStatus(app, chunks)).resolves.toBe(413);
    expect(c.inbound.fromEvent).not.toHaveBeenCalled();
  });

  it("closes adapter resources exactly once", async () => {
    const c = appCollaborators();
    const app = createApp({ webhookSecret: SECRET, inbound: c.inbound, agent: c.agent });
    await closeApp(app);
    await closeApp(app);
    expect(c.agent.close).toHaveBeenCalledOnce();
  });
});

describe("framework configuration", () => {
  it("selects exact supported names", () => {
    const fake = { reply: async () => "fake" };
    expect(selectAgent("fake", { fake: () => fake })).toBe(fake);
    expect(() => selectAgent("OpenAI", { fake: () => fake })).toThrow("AGENT_FRAMEWORK");
  });

  it("requires provider credentials while allowing fake and complete ADK Vertex config", () => {
    expect(() => validateProviderConfig("fake", {})).not.toThrow();
    expect(() => validateProviderConfig("openai", {})).toThrow("OPENAI_API_KEY");
    expect(() => validateProviderConfig("anthropic", {})).toThrow("ANTHROPIC_API_KEY");
    expect(() => validateProviderConfig("langchain", { OPENAI_API_KEY: "key" })).not.toThrow();
    expect(() => validateProviderConfig("langchain", { LANGCHAIN_MODEL: "anthropic:model" })).toThrow("openai:");
    expect(() => validateProviderConfig("adk", {
      GOOGLE_GENAI_USE_VERTEXAI: "true", GOOGLE_CLOUD_PROJECT: "p", GOOGLE_CLOUD_LOCATION: "us",
    })).not.toThrow();
  });
});
