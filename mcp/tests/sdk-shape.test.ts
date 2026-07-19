import { describe, it, expect, afterEach } from "vitest";
import { createServer, type Server } from "node:http";
import {
  E2AClient,
  type AgentSuppressionView,
  type SendEmailInput,
} from "@e2a/sdk/v1";
import { McpClient } from "../src/client.js";

// Guardrail (companion to scripts/check-sdk-version-sync.mjs): exercise the REAL
// @e2a/sdk deserialization through the MCP client against a mock server that
// returns the CURRENT wire shape. The MCP tests elsewhere mock the SDK itself,
// so they can't catch an SDK-model skew. This one can: an old SDK that models
// `dns_records` as an {mx,txt,dkim} OBJECT deserializes the wire ARRAY into {}
// (the bug that shipped dnsRecords: {} over MCP after the SDK majored to 4.0.0).
// With a matching SDK it survives as a populated array.

// The v1.0.5+ DomainView: dns_records is ONE purpose-tagged array.
const WIRE_DOMAIN = {
  domain: "guardrail.example.com",
  verified: false,
  verification_token: "e2a-verify=tok",
  dns_records: [
    { type: "TXT", name: "guardrail.example.com", value: "e2a-verify=tok", priority: null, purpose: "ownership", status: "pending" },
    { type: "MX", name: "guardrail.example.com", value: "mx.e2a.dev", priority: 10, purpose: "inbound_mx", status: "pending" },
    { type: "MX", name: "bounce.guardrail.example.com", value: "feedback-smtp.us-east-1.amazonses.com", priority: 10, purpose: "mail_from_mx", status: "pending" },
    { type: "TXT", name: "bounce.guardrail.example.com", value: "v=spf1 include:amazonses.com ~all", priority: null, purpose: "mail_from_spf", status: "pending" },
  ],
  created_at: "2026-01-01T00:00:00Z",
  verified_at: null,
  sending_status: "none",
};

describe("MCP <-> SDK shape guardrail", () => {
  let server: Server | undefined;
  afterEach(() => server?.close());

  async function clientAgainstMock(): Promise<McpClient> {
    server = createServer((req, res) => {
      res.statusCode = req.method === "POST" ? 201 : 200;
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify(WIRE_DOMAIN));
    });
    await new Promise<void>((r) => server!.listen(0, r));
    const port = (server!.address() as { port: number }).port;
    const sdk = new E2AClient({ apiKey: "test", baseUrl: `http://127.0.0.1:${port}` });
    return new McpClient(sdk);
  }

  it("register_domain deserializes dns_records as a non-empty purpose-tagged array", async () => {
    const client = await clientAgainstMock();
    const domain = (await client.registerDomain("guardrail.example.com")) as {
      dnsRecords?: Array<{ purpose?: string; status?: string }>;
    };

    // The actual regression: a stale SDK would give dnsRecords: {} here.
    expect(Array.isArray(domain.dnsRecords)).toBe(true);
    expect(domain.dnsRecords!.length).toBeGreaterThan(0);
    const purposes = domain.dnsRecords!.map((r) => r.purpose);
    expect(purposes).toContain("ownership");
    expect(purposes).toContain("mail_from_mx");
    for (const rec of domain.dnsRecords!) {
      expect(rec).toHaveProperty("purpose");
      expect(rec).toHaveProperty("status");
    }
  });

  it("get_domain returns the same array shape", async () => {
    const client = await clientAgainstMock();
    const domain = (await client.getDomain("guardrail.example.com")) as {
      dnsRecords?: unknown[];
    };
    expect(Array.isArray(domain.dnsRecords)).toBe(true);
    expect(domain.dnsRecords!.length).toBeGreaterThan(0);
  });

  it("publishes the managed unsubscribe and agent suppression shapes", () => {
    const send: SendEmailInput = {
      to: ["recipient@example.net"],
      subject: "Update",
      text: "Hello",
      unsubscribe: { mode: "managed" },
    };
    expect(send.unsubscribe?.mode).toBe("managed");

    if (false) {
      const sdk = new E2AClient({ apiKey: "test", baseUrl: "http://127.0.0.1" });
      const list: Promise<AgentSuppressionView[]> = sdk.agents
        .listSuppressions("sender@example.com")
        .toArray({ limit: 10 });
      const create: Promise<AgentSuppressionView> = sdk.agents.createSuppression(
        "sender@example.com",
        { address: "recipient@example.net" },
      );
      const remove: Promise<{ deleted: boolean }> = sdk.agents.deleteSuppression(
        "sender@example.com",
        "recipient@example.net",
      );
      void list;
      void create;
      void remove;
    }
  });
});
