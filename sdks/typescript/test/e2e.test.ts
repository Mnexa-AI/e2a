/**
 * Live ergonomic e2e for the TypeScript SDK against a RUNNING server (staging).
 *
 * This exercises the real hand-written ergonomic surface (client.messages.* /
 * client.agents.* / client.info), so a green run attests the published SDK
 * actually works against a live deployment — the parity signal the contract
 * runner (raw HTTP) can't give.
 *
 * Gated on staging creds; skips cleanly when absent, so it stays inert in the
 * default `npm test`. Env is aligned with the contract runner + the Python live
 * test (E2A_TEST_* naming):
 *   E2A_TEST_BASE_URL     e.g. https://api-staging.e2a.dev (or a local tunnel)
 *   E2A_TEST_API_KEY      an API key for the target account
 *   E2A_TEST_AGENT_EMAIL  a shared-domain inbox on that account (self-send target)
 *
 * Run:
 *   E2A_TEST_BASE_URL=… E2A_TEST_API_KEY=… E2A_TEST_AGENT_EMAIL=… \
 *     npm run test:live --workspace @e2a/sdk
 */
import { describe, it, expect, beforeAll } from "vitest";
import { E2AClient, E2ANotFoundError } from "../src/v1/index.js";

const BASE_URL = process.env.E2A_TEST_BASE_URL || "";
const API_KEY = process.env.E2A_TEST_API_KEY || "";
const AGENT = process.env.E2A_TEST_AGENT_EMAIL || "";
const live = Boolean(BASE_URL && API_KEY && AGENT);

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe.skipIf(!live)("ts sdk live e2e", () => {
  let client: E2AClient;

  beforeAll(() => {
    client = new E2AClient({ apiKey: API_KEY, baseUrl: BASE_URL });
  });

  it("info() reports the deployment", async () => {
    const info = await client.info();
    expect(typeof info.version).toBe("string");
    expect(info.version.length).toBeGreaterThan(0);
  });

  it("agents.create → send → find in inbox → get → reply (self loopback) → delete", async () => {
    // Use a FRESH shared-domain agent (no protection) so the self-send delivers
    // immediately and loops back — the seeded conformance inbox may hold outbound
    // for review, which would never land in the inbox. Same domain as AGENT.
    const domain = AGENT.split("@")[1];
    const bot = `ts-sdk-live-${Date.now().toString(36)}@${domain}`;
    const created = await client.agents.create({ email: bot, name: "ts-sdk live e2e" });
    expect(created.email).toBe(bot);
    try {
      const subject = `ts-sdk-live ${Date.now()}`;
      const bodyText = "Hello from the TypeScript SDK live e2e";

      const sent = await client.messages.send(bot, { to: [bot], subject, text: bodyText });
      expect(sent.messageId).toBeTruthy();
      expect(["sent", "accepted"]).toContain(sent.status);

      // A self-send loopback lands an INBOUND copy in the same inbox; poll for it.
      // Filter to inbound so the just-sent outbound copy (same subject) can't match.
      let found: { id: string } | undefined;
      for (let i = 0; i < 12 && !found; i++) {
        const msgs = await client.messages.list(bot, { direction: "inbound", limit: 20 }).toArray({ limit: 20 });
        found = msgs.find((m) => m.subject === subject);
        if (!found) await sleep(1500);
      }
      expect(found, `an inbound message with subject "${subject}" must appear within ~18s`).toBeTruthy();

      const full = await client.messages.get(bot, found!.id);
      expect(full.id).toBe(found!.id);
      expect(full.subject).toBe(subject);
      // The delivered body is under `parsed` (inbound-extracted MIME), not `body`
      // (the held-outbound draft field, which is null for inbound by design).
      expect(full.parsed?.text ?? "").toContain(bodyText);

      const reply = await client.messages.reply(bot, found!.id, {
        text: "Reply from the TS SDK live e2e",
      });
      expect(reply.messageId).toBeTruthy();
      // Fresh unprotected inbox → the reply sends immediately (same as the send).
      expect(["sent", "accepted"]).toContain(reply.status);
    } finally {
      await client.agents.delete(bot);
    }
  }, 40_000);

  it("lists messages with a bounded page", async () => {
    const msgs = await client.messages.list(AGENT, { limit: 2 }).toArray({ limit: 2 });
    expect(msgs.length).toBeLessThanOrEqual(2);
    for (const m of msgs) {
      expect(m.id).toBeTruthy();
      expect(m.deliveredTo).toBeTruthy();
    }
  });

  it("getMessage on a nonexistent id rejects with E2ANotFoundError", async () => {
    await expect(
      client.messages.get(AGENT, `msg_nonexistent_${Date.now()}`),
    ).rejects.toBeInstanceOf(E2ANotFoundError);
  });
});
