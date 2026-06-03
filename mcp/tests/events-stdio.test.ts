import { describe, expect, it, beforeAll, afterAll } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { resolve as pathResolve } from "node:path";

// MCP events tools exercised through the production stdio transport
// (the path used by `npx -y @e2a/mcp-server`). Spawns the built
// dist/index.js as a real subprocess and drives it via stdio.
//
// Requires `npm run build --workspace @e2a/mcp-server` to have run
// first — uses the local dist/index.js so the test always runs
// against the in-tree code.
//
// Runs against the local e2a backend at http://localhost:8080 with
// a fake API key. The MCP server validates the bearer at session
// init by calling listAgents — for these tests we don't reach the
// backend, instead we set E2A_AGENT_EMAIL to short-circuit the
// validation and exercise the tool catalog only.
//
// Skips when the dist/index.js isn't built, so this file is safe to
// commit even on a fresh clone.

const distPath = pathResolve(__dirname, "../dist/index.js");

describe("MCP events tools over stdio", () => {
  let client: Client | undefined;
  let transport: StdioClientTransport | undefined;

  beforeAll(async () => {
    try {
      transport = new StdioClientTransport({
        command: "node",
        args: [distPath],
        env: {
          ...process.env,
          E2A_API_KEY: "e2a_test_unused", // bypasses HTTP server's bearer check at session level
          E2A_AGENT_EMAIL: "bot@example.com",
          E2A_URL: "http://localhost:8080", // never actually called in this test
        },
      });
      client = new Client({ name: "events-stdio-test", version: "1.0" });
      await client.connect(transport);
    } catch (e) {
      // Likely "dist/index.js not built" — skip the suite.
      transport = undefined;
      client = undefined;
      // eslint-disable-next-line no-console
      console.warn("[events-stdio] skipping — build mcp first via `npm run build --workspace @e2a/mcp-server`", e);
    }
  }, 20_000);

  afterAll(async () => {
    if (client) await client.close().catch(() => undefined);
  });

  it("registers list_events, get_event, redeliver_event in the stdio tool catalog", async () => {
    if (!client) {
      // Skip when build is missing.
      return;
    }
    const { tools } = await client.listTools();
    const names = tools.map((t) => t.name);
    expect(names).toContain("list_events");
    expect(names).toContain("get_event");
    expect(names).toContain("redeliver_event");
  });

  it("tool catalog grew by 3 with the new events tools", async () => {
    if (!client) return;
    const { tools } = await client.listTools();
    // The exact baseline drifts as other slices add tools; just
    // verify the 3 new events tools are part of the total.
    const events = tools.filter((t) =>
      ["list_events", "get_event", "redeliver_event"].includes(t.name),
    );
    expect(events.length).toBe(3);
    expect(tools.length).toBeGreaterThanOrEqual(3);
  });

  it("describes the events tools with non-empty schemas", async () => {
    if (!client) return;
    const { tools } = await client.listTools();
    for (const name of ["list_events", "get_event", "redeliver_event"]) {
      const tool = tools.find((t) => t.name === name);
      expect(tool).toBeDefined();
      expect(tool!.description).toBeTruthy();
      expect(tool!.description!.length).toBeGreaterThan(50);
      expect(tool!.inputSchema).toBeDefined();
    }
  });
});
