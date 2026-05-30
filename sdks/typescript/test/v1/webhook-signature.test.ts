import { createHmac } from "node:crypto";
import { describe, it, expect } from "vitest";
import { verifyWebhookSignature } from "../../src/v1/webhook-signature.js";

const SECRET = "whsec_test1234567890abcdef";

function sign(secret: string, t: string, body: string): string {
  return createHmac("sha256", secret).update(`${t}.${body}`).digest("hex");
}

describe("verifyWebhookSignature", () => {
  it("accepts a correctly signed envelope", () => {
    const body = '{"event":"email.received"}';
    const t = Math.floor(Date.now() / 1000).toString();
    const v1 = sign(SECRET, t, body);
    const ok = verifyWebhookSignature({
      rawBody: body,
      header: `t=${t},v1=${v1}`,
      secret: SECRET,
    });
    expect(ok).toBe(true);
  });

  it("rejects a tampered body", () => {
    const body = '{"event":"email.received"}';
    const t = Math.floor(Date.now() / 1000).toString();
    const v1 = sign(SECRET, t, body);
    const ok = verifyWebhookSignature({
      rawBody: '{"event":"email.received","tampered":true}',
      header: `t=${t},v1=${v1}`,
      secret: SECRET,
    });
    expect(ok).toBe(false);
  });

  it("rejects a wrong secret", () => {
    const body = "{}";
    const t = Math.floor(Date.now() / 1000).toString();
    const v1 = sign(SECRET, t, body);
    const ok = verifyWebhookSignature({
      rawBody: body,
      header: `t=${t},v1=${v1}`,
      secret: "whsec_wrongkey",
    });
    expect(ok).toBe(false);
  });

  it("rejects an old timestamp outside the tolerance", () => {
    const body = "{}";
    const now = 1_700_000_000_000;
    const t = Math.floor((now - 10 * 60 * 1000) / 1000).toString(); // 10 min ago
    const v1 = sign(SECRET, t, body);
    const ok = verifyWebhookSignature({
      rawBody: body,
      header: `t=${t},v1=${v1}`,
      secret: SECRET,
      now: () => now,
    });
    expect(ok).toBe(false);
  });

  it("accepts either v1 during rotation grace (dual-sig)", () => {
    const body = "{}";
    const t = Math.floor(Date.now() / 1000).toString();
    const oldSecret = "whsec_old";
    const newSecret = "whsec_new";
    const v1Old = sign(oldSecret, t, body);
    const v1New = sign(newSecret, t, body);
    // Receiver still using the OLD secret should accept (matches first v1).
    const okOld = verifyWebhookSignature({
      rawBody: body,
      header: `t=${t},v1=${v1Old},v1=${v1New}`,
      secret: oldSecret,
    });
    expect(okOld).toBe(true);
    // Receiver using the NEW secret should also accept (matches second v1).
    const okNew = verifyWebhookSignature({
      rawBody: body,
      header: `t=${t},v1=${v1Old},v1=${v1New}`,
      secret: newSecret,
    });
    expect(okNew).toBe(true);
  });

  it("rejects a header with missing parts", () => {
    expect(verifyWebhookSignature({ rawBody: "{}", header: "", secret: SECRET })).toBe(false);
    expect(verifyWebhookSignature({ rawBody: "{}", header: "t=123", secret: SECRET })).toBe(false);
    expect(verifyWebhookSignature({ rawBody: "{}", header: "v1=abc", secret: SECRET })).toBe(false);
  });
});
