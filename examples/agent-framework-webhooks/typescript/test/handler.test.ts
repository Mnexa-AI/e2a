import { createHmac } from "node:crypto";

import { describe, expect, it, vi } from "vitest";
import {
  E2AWebhookSignatureError,
  constructEvent,
  type InboundEmail,
  type WebhookEvent,
} from "@e2a/sdk/v1";

import { EventDeduper } from "../src/delivery-state.js";
import { DeliveryInProgress, handleDelivery } from "../src/handler.js";
import { emailPrompt } from "../src/prompt.js";

const SECRET = "whsec_test";

function signedDelivery(options: { eventId?: string; eventType?: string } = {}): {
  body: string;
  signature: string;
} {
  const payload = {
    id: options.eventId ?? "evt_1",
    type: options.eventType ?? "email.received",
    schema_version: "1",
    created_at: "2026-07-20T12:00:00.000Z",
    data: {
      message_id: "msg_1",
      agent_email: "agent@example.com",
      direction: "inbound",
      conversation_id: "conv_1",
      header_from: "sender@example.net",
      envelope_from: "sender@example.net",
      verified_domain: "example.net",
      to: ["agent@example.com"],
      cc: [],
      reply_to: [],
      delivered_to: "agent@example.com",
      subject: "Hello",
      received_at: "2026-07-20T12:00:00.000Z",
      attachments: [],
      authentication: {
        spf: { status: "pass", domain: "example.net", aligned: true },
        dkim: [{ status: "pass", domain: "example.net", selector: "selector1", aligned: true }],
        dmarc: { status: "pass", domain: "example.net", policy: "reject", aligned_by: ["spf", "dkim"] },
      },
    },
  };
  const body = JSON.stringify(payload);
  const timestamp = Math.floor(Date.now() / 1_000).toString();
  const digest = createHmac("sha256", SECRET).update(`${timestamp}.${body}`).digest("hex");
  return { body, signature: `t=${timestamp},v1=${digest}` };
}

type FakeEmail = {
  conversationId: string;
  from: string | null;
  subject: string;
  verified: boolean;
  flagged: boolean;
  text: string;
  rawMessage?: string;
  reply: ReturnType<typeof vi.fn>;
};

function collaborators(options: { agentOutput?: string; sendStatus?: string } = {}) {
  const email: FakeEmail = {
    conversationId: "conv_1",
    from: "sender@example.net",
    subject: "Hello",
    verified: true,
    flagged: false,
    text: "How are you?",
    reply: vi.fn().mockResolvedValue({ status: options.sendStatus ?? "accepted" }),
  };
  // The example contracts intentionally accept the SDK facade. This fixture
  // supplies only the fields exercised by the framework-neutral handler.
  const inboundEmail = email as unknown as InboundEmail;
  const inbound = { fromEvent: vi.fn().mockResolvedValue(inboundEmail) };
  const agent = { reply: vi.fn().mockResolvedValue(options.agentOutput ?? "Thanks") };
  return { email, inboundEmail, inbound, agent };
}

describe("emailPrompt", () => {
  it("projects only normalized, sender-controlled fields", () => {
    const { email, inboundEmail } = collaborators();
    email.rawMessage = "SECRET RAW MIME";

    expect(emailPrompt(inboundEmail)).toBe(
      "From: sender@example.net\n" +
        "Subject: Hello\n" +
        "Sender DMARC verified: yes\n" +
        "Policy flagged: no\n\n" +
        "How are you?",
    );
    expect(emailPrompt(inboundEmail)).not.toContain("SECRET RAW MIME");
  });

  it("uses the missing sender fallback and no boolean projections", () => {
    const { email, inboundEmail } = collaborators();
    email.from = null;
    email.verified = false;
    email.flagged = true;

    expect(emailPrompt(inboundEmail)).toContain("From: (missing)");
    expect(emailPrompt(inboundEmail)).toContain("Sender DMARC verified: no");
    expect(emailPrompt(inboundEmail)).toContain("Policy flagged: yes");
  });
});

describe("EventDeduper", () => {
  it("allows exactly one new claim under 100-way contention", async () => {
    const deduper = new EventDeduper();

    const results = await Promise.all(Array.from({ length: 100 }, () => deduper.claim("evt_1")));

    expect(results.filter((result) => result === "new")).toHaveLength(1);
    expect(results.filter((result) => result === "processing")).toHaveLength(99);
  });

  it("transitions claims through release and completion", async () => {
    const deduper = new EventDeduper();

    expect(await deduper.claim("evt_1")).toBe("new");
    expect(await deduper.claim("evt_1")).toBe("processing");
    await deduper.release("evt_1");
    expect(await deduper.claim("evt_1")).toBe("new");
    await deduper.complete("evt_1");
    expect(await deduper.claim("evt_1")).toBe("processed");
  });

  it("evicts processed event IDs in FIFO order", async () => {
    const deduper = new EventDeduper({ maxProcessed: 2 });
    for (const id of ["evt_1", "evt_2", "evt_3"]) {
      expect(await deduper.claim(id)).toBe("new");
      await deduper.complete(id);
    }

    expect(await deduper.claim("evt_1")).toBe("new");
    expect(await deduper.claim("evt_2")).toBe("processed");
    expect(await deduper.claim("evt_3")).toBe("processed");
  });

  it.each([0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY])(
    "rejects invalid maxProcessed capacity %s",
    (maxProcessed) => {
      expect(() => new EventDeduper({ maxProcessed })).toThrow("maxProcessed must be a positive integer");
    },
  );
});

describe("handleDelivery", () => {
  it("verifies, fetches, trims, and replies with event idempotency", async () => {
    const { body, signature } = signedDelivery();
    const { email, inboundEmail, inbound, agent } = collaborators({ agentOutput: "  Thanks  " });

    const result = await handleDelivery({
      body,
      signature,
      secret: SECRET,
      inbound,
      agent,
      deduper: new EventDeduper(),
    });

    expect(result).toEqual({ status: "replied", conversationId: "conv_1" });
    expect(inbound.fromEvent).toHaveBeenCalledOnce();
    expect(inbound.fromEvent).toHaveBeenCalledWith(constructEvent(body, signature, SECRET));
    expect(agent.reply).toHaveBeenCalledWith(inboundEmail);
    expect(email.reply).toHaveBeenCalledWith(
      { text: "Thanks", conversationId: "conv_1" },
      { idempotencyKey: "evt_1" },
    );
  });

  it("rejects an invalid signature before claiming or downstream work", async () => {
    const { body, signature } = signedDelivery();
    const badSignature = `${signature.slice(0, -1)}${signature.endsWith("0") ? "1" : "0"}`;
    const { email, inbound, agent } = collaborators();
    const deduper = new EventDeduper();

    await expect(
      handleDelivery({ body, signature: badSignature, secret: SECRET, inbound, agent, deduper }),
    ).rejects.toBeInstanceOf(E2AWebhookSignatureError);
    expect(inbound.fromEvent).not.toHaveBeenCalled();
    expect(agent.reply).not.toHaveBeenCalled();
    expect(email.reply).not.toHaveBeenCalled();
    expect(await deduper.claim("evt_1")).toBe("new");
  });

  it("accepts a Uint8Array raw body and ignores non-email events before claiming", async () => {
    const { body, signature } = signedDelivery({ eventType: "email.sent" });
    const { email, inbound, agent } = collaborators();
    const deduper = new EventDeduper();

    const result = await handleDelivery({
      body: new TextEncoder().encode(body),
      signature,
      secret: SECRET,
      inbound,
      agent,
      deduper,
    });

    expect(result).toEqual({ status: "ignored" });
    expect(inbound.fromEvent).not.toHaveBeenCalled();
    expect(agent.reply).not.toHaveBeenCalled();
    expect(email.reply).not.toHaveBeenCalled();
    expect(await deduper.claim("evt_1")).toBe("new");
  });

  it("runs only one turn and reply for a completed duplicate", async () => {
    const delivery = signedDelivery();
    const { email, inbound, agent } = collaborators();
    const deduper = new EventDeduper();
    const input = { ...delivery, secret: SECRET, inbound, agent, deduper };

    expect(await handleDelivery(input)).toEqual({ status: "replied", conversationId: "conv_1" });
    expect(await handleDelivery(input)).toEqual({ status: "duplicate" });
    expect(inbound.fromEvent).toHaveBeenCalledOnce();
    expect(agent.reply).toHaveBeenCalledOnce();
    expect(email.reply).toHaveBeenCalledOnce();
  });

  it("throws DeliveryInProgress with the colliding event ID", async () => {
    const delivery = signedDelivery();
    const { email, inbound, agent } = collaborators();
    const deduper = new EventDeduper();
    expect(await deduper.claim("evt_1")).toBe("new");

    const pending = handleDelivery({ ...delivery, secret: SECRET, inbound, agent, deduper });

    await expect(pending).rejects.toMatchObject({ eventId: "evt_1" });
    await expect(pending).rejects.toBeInstanceOf(DeliveryInProgress);
    expect(inbound.fromEvent).not.toHaveBeenCalled();
    expect(agent.reply).not.toHaveBeenCalled();
    expect(email.reply).not.toHaveBeenCalled();
    expect(await deduper.claim("evt_1")).toBe("processing");
  });

  it("completes whitespace-only output without replying", async () => {
    const delivery = signedDelivery();
    const { email, inbound, agent } = collaborators({ agentOutput: " \n\t " });
    const deduper = new EventDeduper();

    expect(await handleDelivery({ ...delivery, secret: SECRET, inbound, agent, deduper })).toEqual({
      status: "no_reply",
      conversationId: "conv_1",
    });
    expect(await handleDelivery({ ...delivery, secret: SECRET, inbound, agent, deduper })).toEqual({
      status: "duplicate",
    });
    expect(email.reply).not.toHaveBeenCalled();
  });

  it("releases an agent failure so the delivery can retry", async () => {
    const delivery = signedDelivery();
    const { email, inbound, agent } = collaborators();
    agent.reply.mockRejectedValueOnce(new Error("agent failed"));
    const deduper = new EventDeduper();
    const input = { ...delivery, secret: SECRET, inbound, agent, deduper };

    await expect(handleDelivery(input)).rejects.toThrow("agent failed");
    await expect(handleDelivery(input)).resolves.toEqual({ status: "replied", conversationId: "conv_1" });
    expect(agent.reply).toHaveBeenCalledTimes(2);
    expect(email.reply).toHaveBeenCalledOnce();
  });

  it("releases a send failure so the delivery can retry", async () => {
    const delivery = signedDelivery();
    const { email, inbound, agent } = collaborators();
    email.reply.mockRejectedValueOnce(new Error("send failed"));
    const deduper = new EventDeduper();
    const input = { ...delivery, secret: SECRET, inbound, agent, deduper };

    await expect(handleDelivery(input)).rejects.toThrow("send failed");
    await expect(handleDelivery(input)).resolves.toEqual({ status: "replied", conversationId: "conv_1" });
    expect(inbound.fromEvent).toHaveBeenCalledTimes(2);
    expect(agent.reply).toHaveBeenCalledTimes(2);
    expect(email.reply).toHaveBeenCalledTimes(2);
  });

  it.each(["accepted", "sent"])("maps %s sends to replied", async (sendStatus) => {
    const delivery = signedDelivery();
    const { inbound, agent } = collaborators({ sendStatus });

    await expect(
      handleDelivery({ ...delivery, secret: SECRET, inbound, agent, deduper: new EventDeduper() }),
    ).resolves.toEqual({ status: "replied", conversationId: "conv_1" });
  });

  it("preserves a pending_review send status", async () => {
    const delivery = signedDelivery();
    const { inbound, agent } = collaborators({ sendStatus: "pending_review" });

    await expect(
      handleDelivery({ ...delivery, secret: SECRET, inbound, agent, deduper: new EventDeduper() }),
    ).resolves.toEqual({ status: "pending_review", conversationId: "conv_1" });
  });
});
