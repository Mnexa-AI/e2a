import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient, type RawResponse } from "../harness/client.ts";
import { cleanup } from "../harness/cleanup.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "07-error-contract";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/07-error-contract.json`);
});

// Track which endpoints return JSON error bodies vs text. Spec varies per endpoint —
// the doc has many `schema: type: string` 4xx responses, suggesting bare-text errors
// are intentional. We surface the actual shape so the SDK team can normalize.
const shapeMap = new Map<string, { contentType: string; sample: string; status: number }>();

function noteShape(label: string, r: RawResponse) {
  shapeMap.set(label, {
    contentType: r.headers["content-type"] ?? "",
    sample: r.raw.slice(0, 120),
    status: r.status,
  });
}

test("error-contract: malformed JSON body returns 4xx, not 5xx", async () => {
  const r = await client.post("/api/v1/agents", { body: "{not json", headers: { "Content-Type": "application/json" } });
  noteShape("post-agents-bad-json", r);
  if (r.status >= 500) fail(SUITE, "bad-json-500", `malformed JSON caused ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("error-contract: empty body on POST /agents returns 4xx", async () => {
  const r = await client.post("/api/v1/agents", { body: "", headers: { "Content-Type": "application/json" } });
  noteShape("post-agents-empty", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: wrong content-type on POST /agents returns 4xx", async () => {
  const r = await client.post("/api/v1/agents", {
    body: "slug=foo&name=bar",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
  });
  noteShape("post-agents-wrong-ct", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: GET /info ignores body+method shenanigans", async () => {
  const r = await client.get("/api/v1/info");
  assert.equal(r.status, 200);
});

test("error-contract: POST /info returns 4xx or 405 (read-only endpoint)", async () => {
  const r = await client.post("/api/v1/info", { body: {} });
  noteShape("post-info", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: PUT /api/v1/messages (no such method) returns 405 or 4xx", async () => {
  const r = await client.put("/api/v1/messages", { body: {} });
  noteShape("put-messages", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("error-contract: DELETE /api/v1/messages (no such method) returns 4xx", async () => {
  const r = await client.delete("/api/v1/messages");
  noteShape("delete-messages", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("error-contract: GET nonexistent path returns 404", async () => {
  const r = await client.get("/api/v1/this/does/not/exist");
  noteShape("nonexistent-path", r);
  assert.equal(r.status, 404, `expected 404, got ${r.status}`);
});

test("error-contract: traversal attempt returns 4xx, not file leak", async () => {
  const r = await client.get("/api/v1/agents/..%2F..%2Fetc%2Fpasswd");
  if (r.status === 200) {
    fail(SUITE, "traversal-200", "path traversal returned 200 — possible bug");
  }
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("error-contract: very long email path is bounded (no 500)", async () => {
  const long = "a".repeat(2000) + "@example.com";
  const r = await client.get(`/api/v1/agents/${encodeURIComponent(long)}`);
  if (r.status >= 500) fail(SUITE, "long-email-500", `${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.status < 500, `expected <500, got ${r.status}`);
});

test("error-contract: invalid query types on /messages?limit=abc handled gracefully", async () => {
  const r = await client.get("/api/v1/messages?limit=abc");
  if (r.status >= 500) fail(SUITE, "bad-limit-500", `${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.status < 500, `expected <500, got ${r.status}`);
});

test("error-contract: PATCH on /domains/<bogus> returns 4xx", async () => {
  const r = await client.patch(`/api/v1/domains/bogus-${Date.now()}.example.com`, { body: {} });
  noteShape("patch-bogus-domain", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: POST /domains/<bogus>/verify returns 4xx", async () => {
  const r = await client.post(`/api/v1/domains/bogus-${Date.now()}.example.com/verify`, { body: {} });
  noteShape("verify-bogus-domain", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: invalid email path in /send.from", async () => {
  const r = await client.post("/api/v1/send", {
    body: { from: "not-an-email", to: ["someone@example.com"], subject: "x", body: "x" },
  });
  noteShape("send-bad-from", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: send with empty 'to' array returns 4xx", async () => {
  const r = await client.post("/api/v1/send", {
    body: { from: client.env.primaryAgentEmail, to: [], subject: "x", body: "x" },
  });
  noteShape("send-empty-to", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: send with invalid recipient address returns 4xx (no smtp call)", async () => {
  const r = await client.post("/api/v1/send", {
    body: { from: client.env.primaryAgentEmail, to: ["definitely not an email"], subject: "x", body: "x" },
  });
  noteShape("send-bad-recipient", r);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("error-contract: summary of error response shapes (informational)", async () => {
  const byContentType = new Map<string, string[]>();
  for (const [label, info] of shapeMap.entries()) {
    const ct = info.contentType.split(";")[0].trim();
    const arr = byContentType.get(ct) ?? [];
    arr.push(`${label}=${info.status}`);
    byContentType.set(ct, arr);
  }
  for (const [ct, labels] of byContentType.entries()) {
    info(SUITE, "shape-by-ct", `Content-Type "${ct}": ${labels.join(", ")}`);
  }
  // No assertion — surface info only.
});
