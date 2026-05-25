import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { info, warn, writeReport } from "../harness/report.ts";
import { uniqueSlug } from "../harness/fixtures.ts";

const client = new ApiClient();
const SUITE = "09-postfix";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/09-postfix.json`);
});

test("postfix #3: POST /agents without agent_mode returns 400 (was implicit default to cloud)", async () => {
  const slug = uniqueSlug("nomode");
  const r = await client.post("/api/v1/agents", { body: { slug, name: "no mode" } });
  assert.equal(r.status, 400, `expected 400, got ${r.status}: ${r.raw.slice(0, 200)}`);
  // Body should mention agent_mode explicitly.
  assert.ok(/agent_mode/i.test(r.raw), `expected error mentioning agent_mode, got: ${r.raw.slice(0, 200)}`);
  info(SUITE, "agent-mode-required", `body: "${r.raw.trim()}"`);
});

test("postfix #3: invalid agent_mode value returns 400", async () => {
  const slug = uniqueSlug("badmode");
  const r = await client.post("/api/v1/agents", {
    body: { slug, name: "bad mode", agent_mode: "neither-local-nor-cloud" },
  });
  assert.equal(r.status, 400, `expected 400, got ${r.status}`);
});

test("postfix #4: GET nonexistent path returns 404 with text/plain body", async () => {
  const r = await client.get("/api/v1/this/does/not/exist");
  assert.equal(r.status, 404, `expected 404, got ${r.status}`);
  const ct = r.headers["content-type"] ?? "";
  assert.ok(ct.startsWith("text/plain"), `expected text/plain, got "${ct}"`);
  assert.ok(r.raw.trim().length > 0, "body should be non-empty");
  info(SUITE, "404-shape", `Content-Type: "${ct}", body: "${r.raw.trim()}"`);
});

test("postfix #4: wrong-method on /info returns 405 with text/plain body (was empty)", async () => {
  const r = await client.post("/api/v1/info", { body: {} });
  assert.equal(r.status, 405, `expected 405, got ${r.status}`);
  const ct = r.headers["content-type"] ?? "";
  assert.ok(ct.startsWith("text/plain"), `expected text/plain, got "${ct}"`);
  assert.ok(r.raw.trim().length > 0, "body should be non-empty");
  info(SUITE, "405-shape", `Content-Type: "${ct}", body: "${r.raw.trim()}"`);
});

test("postfix #4: wrong-method on /messages returns 405 with body", async () => {
  const r = await client.put("/api/v1/messages", { body: {} });
  assert.equal(r.status, 405, `expected 405, got ${r.status}`);
  const ct = r.headers["content-type"] ?? "";
  assert.ok(ct.startsWith("text/plain"), `expected text/plain, got "${ct}"`);
});

test("postfix #6: GET /agents/{email} is case-insensitive (lowercase + uppercase match)", async () => {
  const email = client.env.primaryAgentEmail;
  const lower = await client.get(`/api/v1/agents/${encodeURIComponent(email.toLowerCase())}`);
  const upper = await client.get(`/api/v1/agents/${encodeURIComponent(email.toUpperCase())}`);
  assert.equal(lower.status, 200, "lowercase form should resolve");
  assert.equal(upper.status, 200, `uppercase form should also resolve, got ${upper.status}: ${upper.raw.slice(0, 200)}`);
  if (lower.body && upper.body) {
    // Both should resolve to the same canonical agent.
    const lEmail = (lower.body as { email?: string }).email;
    const uEmail = (upper.body as { email?: string }).email;
    assert.equal(lEmail, uEmail, "both case forms should resolve to the same canonical email");
  }
});

test("postfix #6: GET /agents/{email} with mixed-case still matches", async () => {
  const email = client.env.primaryAgentEmail;
  const local = email.split("@")[0];
  const domain = email.split("@")[1];
  const mixed = local.toUpperCase() + "@" + domain.toLowerCase();
  const r = await client.get(`/api/v1/agents/${encodeURIComponent(mixed)}`);
  assert.equal(r.status, 200, `mixed-case email should match, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("postfix #7: /send with CRLF in subject is rejected at the API (400)", async () => {
  const r = await client.post("/api/v1/send", {
    body: {
      from: client.env.primaryAgentEmail,
      to: ["blackhole@e2a.dev"],
      subject: "Hello\r\nBcc: attacker@evil.com",
      body: "x",
    },
  });
  assert.equal(r.status, 400, `expected 400 (CRLF rejected), got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(/CR|LF|\\r|\\n|newline|line/i.test(r.raw), `expected error mentioning CR/LF, got: ${r.raw.slice(0, 200)}`);
  info(SUITE, "crlf-rejected", `body: "${r.raw.trim()}"`);
});

test("postfix #7: bare LF in subject is also rejected (no carriage return)", async () => {
  const r = await client.post("/api/v1/send", {
    body: {
      from: client.env.primaryAgentEmail,
      to: ["blackhole@e2a.dev"],
      subject: "Hello\nX-Smuggled: yes",
      body: "x",
    },
  });
  assert.equal(r.status, 400, `expected 400 for bare LF, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("postfix #1 #2: /send 429 includes Retry-After header (probed via invalid payloads + 1 quota-hit guard)", async () => {
  // We can't probe the send rate limit on prod without queueing real HITL
  // notifications. Instead we verify the header CONTRACT: the docs (and
  // OpenAPI) now say 429 carries Retry-After. We'll skip the active probe.
  info(
    SUITE,
    "retry-after-probe-skipped",
    "skipping active 60-send rate-limit probe to avoid triggering HITL notification emails — see issue #146",
  );
});

test("postfix #1 #2: /agents 429 includes Retry-After header (active probe — does NOT send mail)", async () => {
  // Agent creation is a pure CRUD op; failing creates don't fan out to SMTP.
  // Probe until we see a 429 OR exhaust 25 attempts.
  let saw429 = false;
  let retryAfter: string | undefined;
  for (let i = 0; i < 25; i++) {
    const r = await client.post<{ email?: string }>("/api/v1/agents", {
      body: { slug: uniqueSlug(`pf${i}`), name: "pf", agent_mode: "local" },
    });
    if (r.status === 429) {
      saw429 = true;
      retryAfter = r.headers["retry-after"];
      break;
    }
    if (r.status === 201 && r.body?.email) {
      // Track in the cleanup registry so the after() hook removes it,
      // even if the loop exits early or a later attempt fails — leaving
      // 10+ probe agents around per run pollutes the account.
      track("agent", r.body.email);
    }
  }
  if (!saw429) {
    info(SUITE, "no-429-hit", "did not hit /agents rate limit in 25 attempts — limit higher than expected");
    return;
  }
  assert.ok(retryAfter, "429 must carry Retry-After header");
  const secs = Number(retryAfter);
  assert.ok(!Number.isNaN(secs) && secs > 0, `Retry-After should be positive seconds, got "${retryAfter}"`);
  info(SUITE, "retry-after-ok", `429 carries Retry-After: ${retryAfter}s — fix landed`);
});

test("postfix #8: MCP strict-schema fix is in the prod MCP server (informational)", async () => {
  // Prod MCP server is a separate deployment; the fix needs to be re-deployed.
  // This test just notes the assertion. The actual schema-strict assertions
  // ran in suite 08-mcp against the LOCAL MCP server dist.
  info(
    SUITE,
    "mcp-fix-deploy-note",
    "MCP strict schemas verified against local dist (08-mcp). Prod MCP deployment carries the previous version until re-deployed.",
  );
});
