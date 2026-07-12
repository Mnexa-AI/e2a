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
  const r = await client.get("/v1/agents", { apiKey: null });
  assert.equal(r.status, 401, `no key expected 401, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: malformed Authorization header returns 401", async () => {
  const r = await client.get("/v1/agents", { apiKey: null, headers: { Authorization: "NotBearer foo" } });
  assert.equal(r.status, 401, `bad scheme expected 401, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: random API key with valid prefix returns 401", async () => {
  const r = await client.get("/v1/agents", {
    apiKey: "e2a_0000000000000000000000000000000000000000000000000000000000000000",
  });
  assert.equal(r.status, 401, `bogus key expected 401, got ${r.status}`);
});

test("authz: random API key without 'e2a_' prefix returns 401", async () => {
  const r = await client.get("/v1/agents", { apiKey: "not-an-e2a-key" });
  assert.equal(r.status, 401, `bad prefix expected 401, got ${r.status}`);
});

test("authz: 401 body does NOT leak hint about key validity", async () => {
  // Both keys are bogus. r1 has the e2a_ prefix and the right length; r2
  // is unrelated garbage. A correct auth gate must return an identical
  // 401 error shape for both so an attacker can't distinguish "key shape
  // is right but invalid" from "completely malformed input." The error
  // envelope carries a per-request `request_id` (ErrorBody schema) that is
  // unique by design, so we compare the structural error (code + message)
  // rather than raw bytes.
  const r1 = await client.get<{ error?: { code?: string; message?: string } }>("/v1/agents", {
    apiKey: "e2a_00000000000000000000000000000000",
  });
  const r2 = await client.get<{ error?: { code?: string; message?: string } }>("/v1/agents", {
    apiKey: "garbage",
  });
  assert.equal(r1.status, 401, `r1 expected 401, got ${r1.status}`);
  assert.equal(r2.status, 401, `r2 expected 401, got ${r2.status}`);
  const shape1 = JSON.stringify({ code: r1.body?.error?.code, message: r1.body?.error?.message });
  const shape2 = JSON.stringify({ code: r2.body?.error?.code, message: r2.body?.error?.message });
  if (shape1 !== shape2) {
    fail(
      SUITE,
      "401-oracle",
      `401 error shapes differ between malformed and well-shaped bogus keys — side-channel oracle. Shapes: ${shape1} vs ${shape2}`,
    );
    assert.fail(
      `401 error code+message must be identical to avoid leaking key-shape info; r1=${shape1} r2=${shape2}`,
    );
  }
  info(SUITE, "401-uniform", "401 error code+message identical for malformed vs bogus-but-shaped — good (no oracle)");
});

test("authz: send as an unowned agent returns 4xx (cannot impersonate)", async () => {
  // The sending agent is named in the path; sending as one the caller
  // doesn't own must be rejected.
  const r = await client.post(
    `/v1/agents/${encodeURIComponent("not-mine@some-random-domain-i-dont-own-987654.com")}/messages`,
    {
      body: {
        to: ["blackhole@e2a.dev"],
        subject: "spoof attempt",
        text: "this should be rejected",
      },
    },
  );
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: GET /agents/<email-i-dont-own> returns 404 not_found (no info leak — indistinguishable from nonexistent)", async () => {
  const r = await client.get(`/v1/agents/${encodeURIComponent("nobody@example.com")}`);
  assert.equal(r.status, 404, `expected 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.error?.code, "not_found", `expected code not_found, got ${r.body?.error?.code}`);
});

test("authz: PATCH /agents/<email-i-dont-own> returns 404 not_found", async () => {
  const r = await client.patch(`/v1/agents/${encodeURIComponent("nobody@example.com")}`, {
    body: { name: "rename attempt" },
  });
  assert.equal(r.status, 404, `expected 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: DELETE /agents/<email-i-dont-own> returns 404 not_found (no cross-tenant delete)", async () => {
  const r = await client.delete(`/v1/agents/${encodeURIComponent("nobody@example.com")}?confirm=DELETE`);
  // A cross-tenant delete must never succeed: a 2xx here is a critical breach.
  if (r.status === 200 || r.status === 204) {
    fail(SUITE, "cross-tenant-delete", `CRITICAL: deleted an agent we don't own; got ${r.status}`);
    assert.fail(`cross-tenant DELETE returned ${r.status} — agent we don't own was deleted`);
  }
  assert.equal(r.status, 404, `expected 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: GET /agents/<email>/messages of unowned agent returns 4xx", async () => {
  const r = await client.get(`/v1/agents/${encodeURIComponent("nobody@example.com")}/messages`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: GET /messages/<bogus-id> returns 4xx, not 200", async () => {
  const agent = encodeURIComponent(client.env.primaryAgentEmail);
  const r = await client.get(`/v1/agents/${agent}/messages/msg_does_not_exist_${Date.now()}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("authz: POST /v1/reviews/<bogus>/approve returns 4xx", async () => {
  const r = await client.post(`/v1/reviews/msg_bogus_${Date.now()}/approve`, { body: {} });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("authz: POST /v1/reviews/<bogus>/reject returns 4xx", async () => {
  const r = await client.post(`/v1/reviews/msg_bogus_${Date.now()}/reject`, { body: { reason: "n/a" } });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("authz: can act on own agent (positive control)", async () => {
  const slug = uniqueSlug("authz");
  const c = await client.post<{ email: string }>("/v1/agents", {
    body: { email: `${slug}@${client.env.sharedDomain}`, name: "authz pos" },
  });
  assert.equal(c.status, 201);
  track("agent", c.body!.email);
  const g = await client.get(`/v1/agents/${encodeURIComponent(c.body!.email)}`);
  assert.equal(g.status, 200);
});
