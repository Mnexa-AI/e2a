import { describe, it, expect } from "vitest";
import { createHmac } from "node:crypto";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  constructEvent,
  isEmailReceived,
  isEmailSent,
  isEmailFailed,
  isEmailDelivered,
  isEmailBounced,
  isEmailComplained,
  isDomainSendingVerified,
  isDomainSendingFailed,
  isDomainSuppressionAdded,
} from "../../src/v1/webhook-signature.js";

// Golden-payload lock (contract freeze PR-2): these are the SAME fixture
// files the server's builder tests assert their marshaled output against
// (internal/eventpayload/testdata). Parsing them through constructEvent into
// the typed payloads proves the TS types match the wire bytes — a server-side
// field change fails here until the SDK types are consciously updated.

const FIXTURE_DIR = join(__dirname, "../../../../internal/eventpayload/testdata");
const SECRET = "whsec_golden";

function loadFixture(name: string): { raw: string; header: string } {
  const raw = readFileSync(join(FIXTURE_DIR, name), "utf8");
  const t = Math.floor(Date.now() / 1000);
  const sig = createHmac("sha256", SECRET).update(`${t}.${raw}`).digest("hex");
  return { raw, header: `t=${t},v1=${sig}` };
}

function construct(name: string) {
  const { raw, header } = loadFixture(name);
  return constructEvent(raw, header, SECRET);
}

describe("golden payload fixtures parse into the typed payloads", () => {
  it("email.received", () => {
    const e = construct("email.received.json");
    expect(e.schema_version).toBe("1");
    if (!isEmailReceived(e)) throw new Error("guard failed");
    const d = e.data;
    expect(d.message_id).toMatch(/^msg_/);
    expect(d.agent_email).toBe("support@agents.example.com");
    expect(d.direction).toBe("inbound");
    expect(d.from).toBe("reply@customer.example.com");
    expect(d.authenticated_from).toBe("alice@customer.example.com");
    expect(d.to).toEqual(["support@agents.example.com"]);
    expect(d.delivered_to).toBe("support@agents.example.com");
    expect(d.subject).toBe("Order #1234 delayed");
    expect(d.auth_headers["X-E2A-Auth-Verified"]).toBe("true");
    expect(typeof d.received_at).toBe("string");
    expect(d.attachments).toEqual([
      { filename: "invoice.pdf", content_type: "application/pdf", size_bytes: 12345, index: 0 },
    ]);
  });

  it("email.sent", () => {
    const e = construct("email.sent.json");
    if (!isEmailSent(e)) throw new Error("guard failed");
    const d = e.data;
    expect(d.provider_message_id).toBeTruthy();
    expect(d.method).toBe("smtp");
    expect(d.direction).toBe("outbound");
    expect(d.to).toEqual(["alice@customer.example.com"]);
    expect(d.message_type).toBe("reply");
  });

  it("email.failed", () => {
    const e = construct("email.failed.json");
    if (!isEmailFailed(e)) throw new Error("guard failed");
    const d = e.data;
    expect(d.reason).toBe("550 5.1.1 user unknown");
    expect(d.message_type).toBe("send");
    // provider_message_id is not part of email.failed (never accepted).
    expect((d as unknown as Record<string, unknown>).provider_message_id).toBeUndefined();
  });

  it("email.delivered", () => {
    const e = construct("email.delivered.json");
    if (!isEmailDelivered(e)) throw new Error("guard failed");
    const d = e.data;
    expect(d.delivered_to).toBe("alice@customer.example.com");
    expect(d.subject).toBe("Re: Order #1234 delayed");
    // The redundant `status` field was DROPPED — the event type is the outcome.
    expect((d as unknown as Record<string, unknown>).status).toBeUndefined();
  });

  it("email.bounced", () => {
    const e = construct("email.bounced.json");
    if (!isEmailBounced(e)) throw new Error("guard failed");
    const d = e.data;
    expect(d.bounce_type).toBe("permanent");
    expect(d.bounce_sub_type).toBe("General");
    expect(d.smtp_detail).toBe("550 5.1.1 no such user");
    expect((d as unknown as Record<string, unknown>).status).toBeUndefined();
  });

  it("email.complained", () => {
    const e = construct("email.complained.json");
    if (!isEmailComplained(e)) throw new Error("guard failed");
    expect(e.data.delivered_to).toBe("carol@customer.example.com");
    expect((e.data as unknown as Record<string, unknown>).status).toBeUndefined();
  });

  it("domain.sending_verified", () => {
    const e = construct("domain.sending_verified.json");
    if (!isDomainSendingVerified(e)) throw new Error("guard failed");
    expect(e.data.domain).toBe("mail.customer.example.com");
    expect(e.data.sending_status).toBe("verified");
  });

  it("domain.sending_failed", () => {
    const e = construct("domain.sending_failed.json");
    if (!isDomainSendingFailed(e)) throw new Error("guard failed");
    expect(e.data.sending_status).toBe("failed");
    expect(e.data.reason).toBe("DKIM tokens not found in DNS");
  });

  it("domain.suppression_added", () => {
    const e = construct("domain.suppression_added.json");
    if (!isDomainSuppressionAdded(e)) throw new Error("guard failed");
    expect(e.data.address).toBe("bob@customer.example.com");
    expect(e.data.source).toBe("bounce");
    expect(e.data.message_id).toMatch(/^msg_/);
  });

  // Minimal (required-fields-only) fixtures: the same files the server's
  // presence-semantics lock generates. Parsing them proves the TS types keep
  // every optional field genuinely optional (absent, not null/empty) and
  // every required field present even on the sparsest real payload.
  describe("minimal-variant fixtures (required fields only)", () => {
    it("email.received.min", () => {
      const e = construct("email.received.min.json");
      if (!isEmailReceived(e)) throw new Error("guard failed");
      const d = e.data;
      expect(d.message_id).toMatch(/^msg_/);
      expect(d.delivered_to).toBe("support@agents.example.com");
      // Required present-but-empty, never absent.
      expect(d.authenticated_from).toBe("");
      expect(d.auth_headers).toEqual({});
      expect(d.to).toEqual(["support@agents.example.com"]);
      // Optional fields are ABSENT on the wire.
      expect(d.conversation_id).toBeUndefined();
      expect(d.cc).toBeUndefined();
      expect(d.reply_to).toBeUndefined();
      expect(d.attachments).toBeUndefined();
    });

    it("email.sent.min", () => {
      const e = construct("email.sent.min.json");
      if (!isEmailSent(e)) throw new Error("guard failed");
      const d = e.data;
      expect(d.provider_message_id).toBeTruthy();
      expect(d.conversation_id).toBeUndefined();
      expect(d.cc).toBeUndefined();
      expect(d.bcc).toBeUndefined();
    });

    it("email.failed.min", () => {
      const e = construct("email.failed.min.json");
      if (!isEmailFailed(e)) throw new Error("guard failed");
      const d = e.data;
      expect(d.reason).toBe("550 5.1.1 user unknown");
      expect(d.conversation_id).toBeUndefined();
      expect(d.cc).toBeUndefined();
      expect(d.bcc).toBeUndefined();
      expect(d.reason_code).toBeUndefined();
      expect(d.retryable).toBeUndefined();
    });

    it("email.delivered.min", () => {
      const e = construct("email.delivered.min.json");
      if (!isEmailDelivered(e)) throw new Error("guard failed");
      expect(e.data.delivered_to).toBe("alice@customer.example.com");
      expect(e.data.subject).toBeUndefined();
      expect(e.data.smtp_detail).toBeUndefined();
    });

    it("email.bounced.min", () => {
      const e = construct("email.bounced.min.json");
      if (!isEmailBounced(e)) throw new Error("guard failed");
      const d = e.data;
      // The required classification stays even on the sparsest bounce.
      expect(d.bounce_type).toBe("permanent");
      expect(d.subject).toBeUndefined();
      expect(d.smtp_detail).toBeUndefined();
      expect(d.bounce_sub_type).toBeUndefined();
    });

    it("email.complained.min", () => {
      const e = construct("email.complained.min.json");
      if (!isEmailComplained(e)) throw new Error("guard failed");
      expect(e.data.delivered_to).toBe("carol@customer.example.com");
      expect(e.data.subject).toBeUndefined();
      expect(e.data.smtp_detail).toBeUndefined();
    });

    it("domain.sending_failed.min", () => {
      const e = construct("domain.sending_failed.min.json");
      if (!isDomainSendingFailed(e)) throw new Error("guard failed");
      expect(e.data.sending_status).toBe("failed");
      expect(e.data.reason).toBeUndefined();
    });

    it("domain.suppression_added.min", () => {
      const e = construct("domain.suppression_added.min.json");
      if (!isDomainSuppressionAdded(e)) throw new Error("guard failed");
      expect(e.data.address).toBe("bob@customer.example.com");
      expect(e.data.source).toBe("bounce");
      expect(e.data.reason).toBeUndefined();
      expect(e.data.message_id).toBeUndefined();
    });
  });

  it("unknown event types still parse (envelope stays open)", () => {
    const raw = JSON.stringify({
      type: "email.future_kind",
      id: "evt_x",
      schema_version: "1",
      created_at: "2026-07-01T10:30:00Z",
      data: { anything: true },
    });
    const t = Math.floor(Date.now() / 1000);
    const sig = createHmac("sha256", SECRET).update(`${t}.${raw}`).digest("hex");
    const e = constructEvent(raw, `t=${t},v1=${sig}`, SECRET);
    expect(e.type).toBe("email.future_kind");
    expect(isEmailReceived(e)).toBe(false);
  });
});
