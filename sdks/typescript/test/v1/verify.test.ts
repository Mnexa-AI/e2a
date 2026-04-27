import { createHash, createHmac } from "node:crypto";
import { describe, it, expect } from "vitest";
import { InboundEmail } from "../../src/v1/inbound-email.js";

// Build an InboundEmail whose auth headers were signed with `secret`,
// bound to `body` and `messageID`. Returns the email and the headers
// object (so tests can mutate fields and confirm verification fails).
async function signedEmail(opts: {
  secret: string;
  body?: Buffer;
  messageId?: string;
  sender?: string;
  domainCheck?: string;
  ts?: string;
}) {
  const body = opts.body ?? Buffer.from("hello world");
  const messageId = opts.messageId ?? "msg_abc";
  const sender = opts.sender ?? "alice@example.com";
  const domainCheck = opts.domainCheck ?? "spf=pass; dkim=pass";
  const ts = opts.ts ?? new Date().toISOString().replace(/\.\d{3}Z$/, "Z");
  const bodyHash = createHash("sha256").update(body).digest("hex");
  const canonical = [
    "true", sender, "human", domainCheck, "",
    ts, messageId, bodyHash,
  ].join("\n");
  const sig = createHmac("sha256", opts.secret).update(canonical).digest("hex");
  const headers: Record<string, string> = {
    "X-E2A-Auth-Verified": "true",
    "X-E2A-Auth-Sender": sender,
    "X-E2A-Auth-Entity-Type": "human",
    "X-E2A-Auth-Domain-Check": domainCheck,
    "X-E2A-Auth-Timestamp": ts,
    "X-E2A-Auth-Message-Id": messageId,
    "X-E2A-Auth-Body-Hash": bodyHash,
    "X-E2A-Auth-Signature": sig,
  };
  const email = await InboundEmail.fromPayload({
    message_id: messageId,
    from: sender,
    to: "bot@example.com",
    raw_message: body.toString("base64"),
    auth_headers: headers,
  }, {} as never);
  return { email, headers };
}

async function emailWith(headers: Record<string, string>, body = Buffer.from("hello world")) {
  return InboundEmail.fromPayload({
    message_id: "msg_abc",
    from: "alice@example.com",
    to: "bot@example.com",
    raw_message: body.toString("base64"),
    auth_headers: headers,
  }, {} as never);
}

const SECRET = "x".repeat(32);

describe("InboundEmail.verifySignature", () => {
  it("accepts a legit signature", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    expect(email.verifySignature(SECRET)).toBe(true);
  });

  it("rejects wrong secret", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    expect(email.verifySignature("y".repeat(32))).toBe(false);
  });

  it("rejects tampered body_hash header", async () => {
    const { headers } = await signedEmail({ secret: SECRET });
    headers["X-E2A-Auth-Body-Hash"] = "0".repeat(64);
    const e2 = await emailWith(headers);
    expect(e2.verifySignature(SECRET)).toBe(false);
  });

  it("rejects tampered sender", async () => {
    const { headers } = await signedEmail({ secret: SECRET });
    headers["X-E2A-Auth-Sender"] = "eve@evil.com";
    const e2 = await emailWith(headers);
    expect(e2.verifySignature(SECRET)).toBe(false);
  });

  it("rejects tampered message_id", async () => {
    const { headers } = await signedEmail({ secret: SECRET });
    headers["X-E2A-Auth-Message-Id"] = "msg_attacker";
    const e2 = await emailWith(headers);
    expect(e2.verifySignature(SECRET)).toBe(false);
  });

  it("rejects modified body bytes", async () => {
    const { headers } = await signedEmail({ secret: SECRET, body: Buffer.from("original") });
    const e2 = await emailWith(headers, Buffer.from("forged"));
    expect(e2.verifySignature(SECRET)).toBe(false);
  });

  it("rejects expired timestamp", async () => {
    const { email } = await signedEmail({ secret: SECRET, ts: "2020-01-01T00:00:00Z" });
    expect(email.verifySignature(SECRET)).toBe(false);
  });

  it("rejects missing signature", async () => {
    const { headers } = await signedEmail({ secret: SECRET });
    headers["X-E2A-Auth-Signature"] = "";
    const e2 = await emailWith(headers);
    expect(e2.verifySignature(SECRET)).toBe(false);
  });
});

// --- Strict-verify gate (SDK 2.0) ---

import { UnverifiedEmailError } from "../../src/v1/inbound-email.js";

describe("InboundEmail field-access gate", () => {
  const SECRET = "test-secret-gate";

  it("throws UnverifiedEmailError on claim-field access before verify", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    // Default state: not yet verified.
    expect(email.verified).toBe(false);
    // Each gated getter throws.
    expect(() => email.sender).toThrow(UnverifiedEmailError);
    expect(() => email.subject).toThrow(UnverifiedEmailError);
    expect(() => email.recipient).toThrow(UnverifiedEmailError);
    expect(() => email.to).toThrow(UnverifiedEmailError);
    expect(() => email.cc).toThrow(UnverifiedEmailError);
    expect(() => email.textBody).toThrow(UnverifiedEmailError);
    expect(() => email.attachments).toThrow(UnverifiedEmailError);
    expect(() => email.messageId).toThrow(UnverifiedEmailError);
  });

  it("auth, rawMessage, isVerified, unverifiedPayload, verifySignature work without verify", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    // None of these should throw.
    expect(email.auth).toBeDefined();
    expect(email.rawMessage).toBeInstanceOf(Buffer);
    expect(email.isVerified).toBe(true); // server's claim
    expect(email.verified).toBe(false); // not yet checked
    expect(email.unverifiedPayload.sender).toBe("alice@example.com");
  });

  it("successful verifySignature unlocks claim-field access", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    expect(email.verifySignature(SECRET)).toBe(true);
    expect(email.verified).toBe(true);
    // No longer throws:
    expect(email.sender).toBe("alice@example.com");
    expect(email.subject).toBeDefined();
  });

  it("failed verifySignature keeps the email locked", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    expect(email.verifySignature("wrong-secret")).toBe(false);
    expect(email.verified).toBe(false);
    expect(() => email.sender).toThrow(UnverifiedEmailError);
  });

  it("verifySignature with no secret + no env throws", async () => {
    const prev = process.env.E2A_HMAC_SECRET;
    delete process.env.E2A_HMAC_SECRET;
    try {
      const { email } = await signedEmail({ secret: SECRET });
      expect(() => email.verifySignature()).toThrow(/E2A_HMAC_SECRET/);
    } finally {
      if (prev !== undefined) process.env.E2A_HMAC_SECRET = prev;
    }
  });

  it("verifySignature falls back to E2A_HMAC_SECRET env var", async () => {
    const prev = process.env.E2A_HMAC_SECRET;
    process.env.E2A_HMAC_SECRET = SECRET;
    try {
      const { email } = await signedEmail({ secret: SECRET });
      expect(email.verifySignature()).toBe(true);
      expect(email.sender).toBe("alice@example.com");
    } finally {
      if (prev === undefined) delete process.env.E2A_HMAC_SECRET;
      else process.env.E2A_HMAC_SECRET = prev;
    }
  });

  it("reply() throws if called before verify", async () => {
    const { email } = await signedEmail({ secret: SECRET });
    await expect(email.reply("hi")).rejects.toThrow(UnverifiedEmailError);
  });
});
