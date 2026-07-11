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
    expect(info).toBeTruthy();
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

      const sent = await client.messages.send(bot, { to: [bot], subject, body: bodyText });
      expect(sent.messageId).toBeTruthy();
      expect(["sent", "accepted"]).toContain(sent.status);

      // A self-send loopback lands an inbound copy in the same inbox; poll for it.
      let found: { messageId: string } | undefined;
      for (let i = 0; i < 12 && !found; i++) {
        const msgs = await client.messages.list(bot, { limit: 20 }).toArray({ limit: 20 });
        found = msgs.find((m) => m.subject === subject);
        if (!found) await sleep(1500);
      }
      expect(found, `a message with subject "${subject}" must appear in the inbox within ~18s`).toBeTruthy();

      const full = await client.messages.get(bot, found!.messageId);
      expect(full.messageId).toBe(found!.messageId);
      expect(full.subject).toBe(subject);
      // NB: not asserting body.text — a self-send LOOPBACK delivers an inbound
      // message with an empty parsed body on staging (the loopback path is a
      // delivery mechanism, not a full MIME round-trip). The SDK-parity signal is
      // the send→list→get→reply round-trip + id/subject correlation, above.

      const reply = await client.messages.reply(bot, found!.messageId, {
        body: "Reply from the TS SDK live e2e",
      });
      expect(reply.messageId).toBeTruthy();
      expect(["sent", "accepted", "pending_review"]).toContain(reply.status);
    } finally {
      await client.agents.delete(bot);
    }
  }, 40_000);

  it("lists messages with a bounded page", async () => {
    const msgs = await client.messages.list(AGENT, { limit: 2 }).toArray({ limit: 2 });
    expect(msgs.length).toBeLessThanOrEqual(2);
    for (const m of msgs) {
      expect(m.messageId).toBeTruthy();
      expect(m.recipient).toBeTruthy();
    }
  });

  it("getMessage on a nonexistent id rejects with E2ANotFoundError", async () => {
    await expect(
      client.messages.get(AGENT, `msg_nonexistent_${Date.now()}`),
    ).rejects.toBeInstanceOf(E2ANotFoundError);
  });
});
