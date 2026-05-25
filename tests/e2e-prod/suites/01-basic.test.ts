import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug, uniqueSubject, SINK_EMAIL } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "01-basic";

after(async () => {
  const result = await cleanup(client);
  if (result.failed.length > 0) {
    warn(SUITE, "cleanup", `failed to delete ${result.failed.length} resources`, result.failed);
  } else {
    info(SUITE, "cleanup", `cleaned up ${result.succeeded} resources`);
  }
  writeReport(`./reports/01-basic.json`);
});

test("info: public deployment metadata is returned", async () => {
  const r = await client.get<{ shared_domain: string; slug_registration_enabled: boolean; public_url: string }>(
    "/api/v1/info",
  );
  assert.equal(r.status, 200);
  assert.ok(r.body?.shared_domain, "shared_domain present");
  assert.ok(r.body?.public_url?.startsWith("http"), "public_url is a URL");
  assert.equal(typeof r.body?.slug_registration_enabled, "boolean");
});

test("agents: list returns user's agents with required fields", async () => {
  const r = await client.get<{ agents: Array<{ email: string; domain: string; agent_mode: string }> }>(
    "/api/v1/agents",
  );
  assert.equal(r.status, 200);
  assert.ok(Array.isArray(r.body?.agents));
  for (const a of r.body!.agents) {
    assert.ok(a.email && a.email.includes("@"), `agent.email valid: ${a.email}`);
    assert.ok(a.domain, `agent.domain set: ${a.email}`);
    assert.ok(["local", "cloud"].includes(a.agent_mode), `agent_mode in enum: ${a.agent_mode}`);
  }
});

test("agents: get primary agent by email", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.get<{ email: string; domain: string; domain_verified: boolean }>(
    `/api/v1/agents/${encodeURIComponent(email)}`,
  );
  assert.equal(r.status, 200);
  assert.equal(r.body?.email, email);
  assert.equal(typeof r.body?.domain_verified, "boolean");
});

test("agents: get nonexistent agent returns 403 (anti-enumeration; matches spec)", async () => {
  const r = await client.get(`/api/v1/agents/nonexistent-${Date.now()}@agents.e2a.dev`);
  assert.equal(r.status, 403, `spec only documents 200/401/403 — expected 403 for unknown, got ${r.status}`);
});

test("agents: get malformed email returns 4xx", async () => {
  const r = await client.get(`/api/v1/agents/not-an-email`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("agents: POST without agent_mode defaults to cloud and 400s (DX finding)", async () => {
  const r = await client.post("/api/v1/agents", { body: { slug: uniqueSlug("nomode"), name: "no mode" } });
  if (r.status === 201) {
    info(SUITE, "no-mode-default", "POST /agents without agent_mode succeeded — default behavior should be documented");
  } else {
    info(
      SUITE,
      "no-mode-default",
      `POST /agents without agent_mode → ${r.status} "${r.raw.slice(0, 120)}". RegisterAgentRequest schema does not declare a default or required mode. Recommend either documenting agent_mode as required, or defaulting to "local" for slug onboarding.`,
    );
  }
  // No assertion — informational only.
});

test("agents: create + read + delete (slug on shared domain)", async () => {
  const slug = uniqueSlug();
  const create = await client.post<{ email: string; id: string; domain: string }>("/api/v1/agents", {
    body: { slug, name: `e2e ${slug}`, agent_mode: "local" },
  });
  assert.equal(create.status, 201, `create slug-agent expected 201, got ${create.status}: ${create.raw.slice(0, 200)}`);
  assert.ok(create.body?.email, "create returns email");
  assert.equal(create.body?.domain, client.env.sharedDomain);
  const email = create.body!.email;
  track("agent", email);

  const got = await client.get<{ email: string; domain_verified: boolean }>(
    `/api/v1/agents/${encodeURIComponent(email)}`,
  );
  assert.equal(got.status, 200);
  assert.equal(got.body?.email, email);
  assert.equal(got.body?.domain_verified, true, "slug-domain agent should be auto-verified");

  const del = await client.delete(`/api/v1/agents/${encodeURIComponent(email)}`);
  assert.ok(del.status === 204 || del.status === 200, `delete expected 200/204, got ${del.status}`);

  const after = await client.get(`/api/v1/agents/${encodeURIComponent(email)}`);
  assert.equal(after.status, 403, `deleted agent should 403 (anti-enumeration), got ${after.status}`);
});

test("agents: create with missing slug AND email returns 4xx", async () => {
  const r = await client.post("/api/v1/agents", { body: { name: "no identifier" } });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("agents: create with duplicate slug returns 409 (or 4xx)", async () => {
  const slug = uniqueSlug();
  const first = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "first", agent_mode: "local" },
  });
  assert.equal(first.status, 201);
  track("agent", first.body!.email);
  const second = await client.post("/api/v1/agents", { body: { slug, name: "second", agent_mode: "local" } });
  assert.ok(second.status >= 400 && second.status < 500, `dup slug expected 4xx, got ${second.status}`);
  if (second.status !== 409) {
    info(SUITE, "dup-slug-code", `spec documents 409 for "Agent already exists" but got ${second.status}: ${second.raw.slice(0, 120)}`);
  }
});

test("agents: update hitl_enabled via PUT persists", async () => {
  const slug = uniqueSlug();
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "update target", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  track("agent", email);

  const upd = await client.put(`/api/v1/agents/${encodeURIComponent(email)}`, { body: { hitl_enabled: true } });
  assert.equal(upd.status, 200, `update expected 200, got ${upd.status}: ${upd.raw.slice(0, 200)}`);

  const g = await client.get<{ hitl_enabled: boolean }>(`/api/v1/agents/${encodeURIComponent(email)}`);
  assert.equal(g.body?.hitl_enabled, true, "hitl_enabled update persisted");
});

test("agents: update with invalid agent_mode enum returns 4xx", async () => {
  const slug = uniqueSlug();
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "invalid mode", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  track("agent", c.body!.email);
  const r = await client.put(`/api/v1/agents/${encodeURIComponent(c.body!.email)}`, {
    body: { agent_mode: "not-a-real-mode" },
  });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx for bad enum, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("send: HITL-enabled agent returns 202 pending_approval", async () => {
  const slug = uniqueSlug("hitl");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "hitl", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  track("agent", email);
  const u = await client.put(`/api/v1/agents/${encodeURIComponent(email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject", hitl_ttl_seconds: 60 },
  });
  assert.equal(u.status, 200);

  const send = await client.post<{ message_id: string; status: string }>("/api/v1/send", {
    body: {
      from: email,
      to: [SINK_EMAIL],
      subject: uniqueSubject("hitl pending"),
      body: "test body — should never go out, immediately rejected",
    },
  });
  assert.ok(
    send.status === 202 || send.status === 200,
    `expected 202/200, got ${send.status}: ${send.raw.slice(0, 200)}`,
  );
  if (send.status !== 202) {
    fail(SUITE, "send-hitl-pending", `HITL agent should yield 202 pending_approval, got ${send.status}`, send.body);
    return;
  }
  assert.equal(send.body?.status, "pending_approval", "status field should be pending_approval");
  assert.ok(send.body?.message_id?.startsWith("msg_"), "message_id has msg_ prefix");

  const reject = await client.post(`/api/v1/messages/${send.body!.message_id}/reject`, {
    body: { reason: "e2e test rejection" },
  });
  assert.ok(reject.status === 200, `reject expected 200, got ${reject.status}: ${reject.raw.slice(0, 200)}`);
});

test("send: missing 'to' returns 4xx", async () => {
  const r = await client.post("/api/v1/send", {
    body: { from: client.env.primaryAgentEmail, subject: "no to", body: "hi" },
  });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("send: unowned 'from' returns 4xx (auth/scope guard)", async () => {
  const r = await client.post("/api/v1/send", {
    body: { from: "someone-else@example.com", to: [SINK_EMAIL], subject: "spoof", body: "hi" },
  });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx (cannot send as non-owned), got ${r.status}`);
});

test("messages: list with limit + pagination tokens", async () => {
  const r = await client.get<{ messages: unknown[]; next_token?: string }>("/api/v1/messages", {
    query: { limit: 5 },
  });
  assert.equal(r.status, 200);
  assert.ok(Array.isArray(r.body?.messages));
  if (r.body!.messages.length >= 5) {
    assert.ok(r.body!.next_token === undefined || typeof r.body!.next_token === "string", "next_token is string|absent");
  }
});

test("signing-secrets: list returns documented shape", async () => {
  const r = await client.get<{ secrets: Array<{ id: string; created_at: string }> }>(
    "/api/v1/users/me/signing-secrets",
  );
  assert.equal(r.status, 200);
  assert.ok(Array.isArray(r.body?.secrets));
  for (const s of r.body!.secrets) {
    assert.ok(s.id, "secret has id");
    assert.ok(s.created_at, "secret has created_at");
  }
});

test("domains: list returns documented shape", async () => {
  const r = await client.get<{ domains: Array<{ domain: string }> }>("/api/v1/domains");
  assert.equal(r.status, 200);
  assert.ok(Array.isArray(r.body?.domains));
});
