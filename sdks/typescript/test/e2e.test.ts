/**
 * E2E tests against the live e2a API.
 *
 * Requires:
 *   E2A_RUN_LIVE_TESTS=1
 *   and E2A_API_KEY / E2A_AGENT_EMAIL env vars (or ~/.e2a/config.json)
 *
 * Run:
 *   E2A_RUN_LIVE_TESTS=1 E2A_API_KEY=e2a_... E2A_AGENT_EMAIL=test-dummy@agents.e2a.dev npx vitest run test/e2e.test.ts
 */
import { describe, it, expect, beforeAll } from "vitest";
import { readFileSync } from "fs";
import { homedir } from "os";
import { join } from "path";
import { E2AClient } from "../src/v1/index.js";

function loadCredentials(): { apiKey: string; agentEmail: string } | null {
  let apiKey = process.env.E2A_API_KEY || "";
  let agentEmail = process.env.E2A_AGENT_EMAIL || "";

  if (!apiKey) {
    try {
      const config = JSON.parse(
        readFileSync(join(homedir(), ".e2a", "config"), "utf-8"),
      );
      apiKey = config.api_key || "";
      agentEmail = agentEmail || config.agent_email || "";
    } catch {
      // no config file
    }
  }

  if (!apiKey || !agentEmail) return null;
  return { apiKey, agentEmail };
}

const creds =
  process.env.E2A_RUN_LIVE_TESTS === "1" ? loadCredentials() : null;

describe.skipIf(!creds)("e2e", () => {
  let client: E2AClient;
  let agentEmail: string;

  beforeAll(() => {
    client = new E2AClient(creds!);
    agentEmail = creds!.agentEmail;
  });
  it("sends an email and finds it in inbox", async () => {
    const subject = `TS SDK e2e test ${Date.now()}`;

    // Send an email to ourselves
    const sendResult = await client.send(agentEmail, subject, "Hello from TypeScript SDK e2e test");
    expect(sendResult.status).toBe("sent");
    expect(sendResult.messageId).toBeTruthy();
    expect(sendResult.method).toBe("smtp");

    // Wait for delivery
    await new Promise((r) => setTimeout(r, 3000));

    // Check inbox for the message
    const { messages } = await client.getMessages({ status: "all", pageSize: 10 });
    expect(messages.length).toBeGreaterThan(0);

    const found = messages.find((m) => m.subject === subject);
    expect(found).toBeDefined();

    // Read the full message
    const email = await client.getMessage(found!.messageId);
    expect(email.subject).toBe(subject);
    expect(email.textBody).toContain("Hello from TypeScript SDK e2e test");
    expect(email.auth.entityType).toBeTruthy();

    // Reply to it
    const replyResult = await email.reply("Reply from TS SDK e2e test");
    expect(replyResult.status).toBe("sent");
    expect(replyResult.messageId).toBeTruthy();
  }, 30_000);

  it("lists messages with pagination", async () => {
    const result = await client.getMessages({ status: "all", pageSize: 2 });
    expect(result.messages.length).toBeLessThanOrEqual(2);
    // Each message has expected fields
    for (const m of result.messages) {
      expect(m.messageId).toBeTruthy();
      expect(m.sender).toBeTruthy();
      expect(m.recipient).toBeTruthy();
    }
  });

  it("handles non-existent message gracefully", async () => {
    await expect(
      client.getMessage("msg_nonexistent_" + Date.now()),
    ).rejects.toThrow();
  });
});
