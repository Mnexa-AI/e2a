import { test } from "node:test";
import assert from "node:assert/strict";
import {
  resolveSinkEmail,
  resolveSiteUrl,
  type ProdEnv,
} from "./env.ts";

test("resolveSiteUrl prefers an explicit site URL and removes its trailing slash", () => {
  assert.equal(
    resolveSiteUrl("https://api.e2a.dev/", "https://console.example.test/"),
    "https://console.example.test",
  );
});

test("resolveSiteUrl maps the canonical production API origin to the production site", () => {
  assert.equal(resolveSiteUrl("https://api.e2a.dev/"), "https://e2a.dev");
});

test("resolveSiteUrl maps the staging API origin to the staging site", () => {
  assert.equal(
    resolveSiteUrl("https://api-staging.e2a.dev/"),
    "https://staging.e2a.dev",
  );
});

test("resolveSiteUrl keeps other API targets as the site target", () => {
  assert.equal(
    resolveSiteUrl("https://self-hosted.example.test/base/"),
    "https://self-hosted.example.test/base",
  );
});

test("resolveSinkEmail returns the explicit non-empty sink", () => {
  assert.equal(resolveSinkEmail(" sink@example.test "), "sink@example.test");
});

test("resolveSinkEmail rejects a missing or empty sink", () => {
  assert.throws(
    () => resolveSinkEmail(),
    /E2E_SINK_EMAIL.*required/i,
  );
  assert.throws(
    () => resolveSinkEmail("   "),
    /E2E_SINK_EMAIL.*required/i,
  );
});

test("ApiClient can override its base URL without changing existing constructor arguments", async () => {
  const originalApiKey = process.env.E2A_API_KEY;
  const originalAgentEmail = process.env.E2A_AGENT_EMAIL;
  const originalSinkEmail = process.env.E2E_SINK_EMAIL;
  process.env.E2A_API_KEY = "test-key";
  process.env.E2A_AGENT_EMAIL = "primary@agents.example.test";
  process.env.E2E_SINK_EMAIL = "sink@example.test";
  const { ApiClient } = await import("./client.ts");
  const env: ProdEnv = {
    apiUrl: "https://api.example.test",
    siteUrl: "https://site.example.test",
    apiKey: "test-key",
    primaryAgentEmail: "primary@agents.example.test",
    sinkEmail: "primary@agents.example.test",
    sharedDomain: "agents.example.test",
    mcpUrl: "https://api.example.test/mcp",
    allowStress: false,
    cleanupMode: "always",
    rateLimitRps: 1,
  };
  const originalFetch = globalThis.fetch;
  let requestedUrl = "";
  globalThis.fetch = async (input) => {
    requestedUrl = String(input);
    return new Response("not found", { status: 404 });
  };

  try {
    const client = new ApiClient(env, 1_000_000, env.siteUrl);
    await client.get("/pricing", { apiKey: null });
  } finally {
    globalThis.fetch = originalFetch;
    if (originalApiKey === undefined) delete process.env.E2A_API_KEY;
    else process.env.E2A_API_KEY = originalApiKey;
    if (originalAgentEmail === undefined) delete process.env.E2A_AGENT_EMAIL;
    else process.env.E2A_AGENT_EMAIL = originalAgentEmail;
    if (originalSinkEmail === undefined) delete process.env.E2E_SINK_EMAIL;
    else process.env.E2E_SINK_EMAIL = originalSinkEmail;
  }

  assert.equal(requestedUrl, "https://site.example.test/pricing");
});
