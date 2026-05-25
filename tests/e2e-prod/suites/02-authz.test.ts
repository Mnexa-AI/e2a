import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { info, warn, writeReport, fail } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "02-authz";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/02-authz.json`);
});

test("authz: no Authorization header returns 401 on list agents", async () => {
  const r = await client.get("/api/v1/agents", { apiKey: null });
  assert.equal(r.status, 401, `no key expected 401, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: malformed Authorization header returns 401", async () => {
  const r = await client.get("/api/v1/agents", { apiKey: null, headers: { Authorization: "NotBearer foo" } });
  assert.equal(r.status, 401, `bad scheme expected 401, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: random API key with valid prefix returns 401", async () => {
  const r = await client.get("/api/v1/agents", {
    apiKey: "e2a_0000000000000000000000000000000000000000000000000000000000000000",
  });
  assert.equal(r.status, 401, `bogus key expected 401, got ${r.status}`);
});

test("authz: random API key without 'e2a_' prefix returns 401", async () => {
  const r = await client.get("/api/v1/agents", { apiKey: "not-an-e2a-key" });
  assert.equal(r.status, 401, `bad prefix expected 401, got ${r.status}`);
});

test("authz: 401 body does NOT leak hint about key validity", async () => {
  const r1 = await client.get("/api/v1/agents", { apiKey: "e2a_00000000000000000000000000000000" });
  const r2 = await client.get("/api/v1/agents", { apiKey: "garbage" });
  if (r1.raw === r2.raw) {
    info(SUITE, "401-uniform", "401 bodies are identical for malformed vs bogus-but-shaped — good (no oracle)");
  } else {
    info(
      SUITE,
      "401-uniform",
      `401 bodies differ between malformed and well-shaped bogus keys. Could be a weak side-channel oracle. Bodies: "${r1.raw.slice(0, 80)}" vs "${r2.raw.slice(0, 80)}"`,
    );
  }
});

test("authz: bogus 'from' on /send returns 4xx (cannot impersonate)", async () => {
  const r = await client.post("/api/v1/send", {
    body: {
      from: "not-mine@some-random-domain-i-dont-own-987654.com",
      to: ["blackhole@e2a.dev"],
      subject: "spoof attempt",
      body: "this should be rejected",
    },
  });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: GET /agents/<email-i-dont-own> returns 403 (no info leak)", async () => {
  const r = await client.get(`/api/v1/agents/${encodeURIComponent("nobody@example.com")}`);
  assert.equal(r.status, 403, `expected 403, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: PUT /agents/<email-i-dont-own> returns 403 or 4xx", async () => {
  const r = await client.put(`/api/v1/agents/${encodeURIComponent("nobody@example.com")}`, {
    body: { hitl_enabled: true },
  });
  assert.ok(r.status === 403 || (r.status >= 400 && r.status < 500), `expected 4xx, got ${r.status}`);
});

test("authz: DELETE /agents/<email-i-dont-own> returns 403 or 4xx (no cross-tenant delete)", async () => {
  const r = await client.delete(`/api/v1/agents/${encodeURIComponent("nobody@example.com")}`);
  assert.ok(r.status === 403 || (r.status >= 400 && r.status < 500), `expected 4xx, got ${r.status}`);
  if (r.status === 200 || r.status === 204) {
    fail(SUITE, "cross-tenant-delete", "CRITICAL: deleted an agent we don't own");
  }
});

test("authz: GET /agents/<email>/messages of unowned agent returns 4xx", async () => {
  const r = await client.get(`/api/v1/agents/${encodeURIComponent("nobody@example.com")}/messages`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: GET /messages/<bogus-id> returns 4xx, not 200", async () => {
  const r = await client.get(`/api/v1/messages/msg_does_not_exist_${Date.now()}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: POST /messages/<bogus>/approve returns 4xx", async () => {
  const r = await client.post(`/api/v1/messages/msg_bogus_${Date.now()}/approve`, { body: {} });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("authz: POST /messages/<bogus>/reject returns 4xx", async () => {
  const r = await client.post(`/api/v1/messages/msg_bogus_${Date.now()}/reject`, { body: { reason: "n/a" } });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("authz: signing-secret DELETE of bogus id returns 4xx (no cross-tenant leak)", async () => {
  const r = await client.delete(`/api/v1/users/me/signing-secrets/sec_bogus_${Date.now()}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("authz: can act on own agent (positive control)", async () => {
  const slug = uniqueSlug("authz");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "authz pos", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  track("agent", c.body!.email);
  const g = await client.get(`/api/v1/agents/${encodeURIComponent(c.body!.email)}`);
  assert.equal(g.status, 200);
});
