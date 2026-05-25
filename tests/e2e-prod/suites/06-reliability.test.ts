import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "06-reliability";

let sharedAgentEmail = "";

before(async () => {
  // Single agent shared across reliability tests to avoid hitting the agent-creation rate limit.
  const slug = uniqueSlug("rel");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "rel-shared", agent_mode: "local" },
  });
  if (c.status !== 201) {
    throw new Error(`shared-agent setup failed: ${c.status} ${c.raw.slice(0, 200)}`);
  }
  sharedAgentEmail = c.body!.email;
  track("agent", sharedAgentEmail);
});

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/06-reliability.json`);
});

function wsUrl(email: string): string {
  const base = client.env.apiUrl.replace(/^http/, "ws");
  return `${base}/api/v1/agents/${encodeURIComponent(email)}/ws`;
}

function openWS(url: string, key?: string | null, timeoutMs = 5_000): Promise<WebSocket> {
  return new Promise((resolve, reject) => {
    const u = new URL(url);
    if (key) u.searchParams.set("token", key);
    const ws = new WebSocket(u.toString());
    const t = setTimeout(() => {
      try {
        ws.close();
      } catch {}
      reject(new Error(`WS open timed out after ${timeoutMs}ms`));
    }, timeoutMs);
    ws.addEventListener("open", () => {
      clearTimeout(t);
      resolve(ws);
    });
    ws.addEventListener("error", (e: Event) => {
      clearTimeout(t);
      reject(new Error(`WS error: ${(e as ErrorEvent).message ?? "unknown"}`));
    });
  });
}

function waitClose(ws: WebSocket, timeoutMs = 3_000): Promise<{ code: number; reason: string }> {
  return new Promise((resolve) => {
    const t = setTimeout(() => resolve({ code: -1, reason: "timeout" }), timeoutMs);
    ws.addEventListener("close", (e: CloseEvent) => {
      clearTimeout(t);
      resolve({ code: e.code, reason: e.reason });
    });
  });
}

test("reliability: WS to own agent opens", async () => {
  const ws = await openWS(wsUrl(sharedAgentEmail), client.env.apiKey, 5_000);
  assert.equal(ws.readyState, 1, "WS readyState should be OPEN");
  ws.close();
  await waitClose(ws);
});

test("reliability: WS without api_key fails to open", async () => {
  try {
    await openWS(wsUrl(sharedAgentEmail), null, 3_000);
    fail(SUITE, "ws-unauth-open", "WS opened without auth — should have rejected");
  } catch {
    info(SUITE, "ws-unauth-rejected", "WS without api_key correctly rejected");
  }
});

test("reliability: WS with wrong api_key fails to open", async () => {
  try {
    await openWS(wsUrl(sharedAgentEmail), "e2a_00000000000000000000000000000000", 3_000);
    fail(SUITE, "ws-badkey-open", "WS opened with bogus key — should have rejected");
  } catch {
    info(SUITE, "ws-badkey-rejected", "WS with bogus api_key correctly rejected");
  }
});

test("reliability: WS to non-owned agent fails to open", async () => {
  try {
    await openWS(wsUrl("nobody@example.com"), client.env.apiKey, 3_000);
    fail(SUITE, "ws-cross-tenant", "WS opened against non-owned agent — cross-tenant break");
  } catch {
    info(SUITE, "ws-cross-tenant-rejected", "WS to non-owned agent correctly rejected");
  }
});

test("reliability: WS reconnect cycle (open → close → open) works", async () => {
  const first = await openWS(wsUrl(sharedAgentEmail), client.env.apiKey, 5_000);
  first.close();
  await waitClose(first);
  await new Promise((r) => setTimeout(r, 250));
  const second = await openWS(wsUrl(sharedAgentEmail), client.env.apiKey, 5_000);
  assert.equal(second.readyState, 1, "reconnect should succeed");
  second.close();
  await waitClose(second);
});

test("reliability: two concurrent WS sessions to same agent", async () => {
  const [a, b] = await Promise.all([
    openWS(wsUrl(sharedAgentEmail), client.env.apiKey, 5_000),
    openWS(wsUrl(sharedAgentEmail), client.env.apiKey, 5_000),
  ]);
  // Either both open (server allows fan-out) or one is rejected — both are valid designs.
  if (a.readyState === 1 && b.readyState === 1) {
    info(SUITE, "ws-multi-session", "server allows multiple concurrent WS sessions to same agent (fan-out)");
  } else {
    info(SUITE, "ws-single-session", `only one WS held — states a=${a.readyState} b=${b.readyState}`);
  }
  try { a.close(); } catch {}
  try { b.close(); } catch {}
});

test("reliability: idempotent PUT — applying same payload twice yields same state", async () => {
  const payload = { hitl_enabled: true, hitl_expiration_action: "reject", hitl_ttl_seconds: 120 };
  const first = await client.put(`/api/v1/agents/${encodeURIComponent(sharedAgentEmail)}`, { body: payload });
  const second = await client.put(`/api/v1/agents/${encodeURIComponent(sharedAgentEmail)}`, { body: payload });
  assert.equal(first.status, 200);
  assert.equal(second.status, 200);
  const g = await client.get<{ hitl_enabled: boolean; hitl_ttl_seconds: number; hitl_expiration_action: string }>(
    `/api/v1/agents/${encodeURIComponent(sharedAgentEmail)}`,
  );
  assert.equal(g.body?.hitl_enabled, true);
  assert.equal(g.body?.hitl_ttl_seconds, 120);
  assert.equal(g.body?.hitl_expiration_action, "reject");
});

test("reliability: server timestamps are RFC3339-parseable", async () => {
  const r = await client.get<{ created_at: string }>(`/api/v1/agents/${encodeURIComponent(sharedAgentEmail)}`);
  assert.ok(r.body?.created_at);
  const t = new Date(r.body!.created_at).valueOf();
  assert.ok(!Number.isNaN(t), `created_at should parse as date: ${r.body?.created_at}`);
  assert.ok(t > 0 && t < Date.now() + 60_000, "created_at is plausibly recent");
});

test("reliability: GET /messages with bogus next_token returns 4xx, not 500", async () => {
  const r = await client.get("/api/v1/messages", {
    query: { limit: 5, next_token: "completely-invalid-token-xx" },
  });
  if (r.status >= 500) {
    fail(SUITE, "pagination-500", `bogus next_token caused ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  assert.ok(r.status < 500, `expected <500, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("reliability: GET /messages with negative limit returns 4xx, not 500", async () => {
  const r = await client.get("/api/v1/messages", { query: { limit: -5 } });
  if (r.status >= 500) {
    fail(SUITE, "negative-limit-500", `negative limit caused ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  assert.ok(r.status < 500, `expected <500, got ${r.status}`);
});

test("reliability: GET /messages with absurdly large limit returns 4xx or capped result, not 500", async () => {
  const r = await client.get<{ messages: unknown[] }>("/api/v1/messages", { query: { limit: 1_000_000 } });
  if (r.status >= 500) {
    fail(SUITE, "huge-limit-500", `huge limit caused ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  assert.ok(r.status < 500, `expected <500, got ${r.status}`);
  if (r.status === 200 && Array.isArray(r.body?.messages) && r.body!.messages.length > 1000) {
    info(SUITE, "limit-uncapped", `server returned ${r.body!.messages.length} items for limit=1M — no server-side cap`);
  } else if (r.status === 200) {
    info(SUITE, "limit-capped", `server capped result to ${r.body?.messages.length ?? "?"} items for limit=1M`);
  }
});
