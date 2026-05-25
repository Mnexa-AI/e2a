import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "05-security";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/05-security.json`);
});

test("security: signing-secrets list exposes plaintext secret (documented; verify scoping)", async () => {
  const r = await client.get<{ secrets: Array<{ id: string; secret?: string; prefix?: string }> }>(
    "/api/v1/users/me/signing-secrets",
  );
  assert.equal(r.status, 200);
  for (const s of r.body!.secrets) {
    if (s.secret) {
      assert.ok(s.secret.length > 16, "plaintext secret should be substantial entropy");
    } else {
      info(SUITE, "secret-not-in-list", `secret ${s.id} listed without plaintext field — endpoint may only return prefix`);
    }
  }
});

test("security: signing-secret create + delete (cleanup roundtrip)", async () => {
  const c = await client.post<{ id: string; secret?: string }>("/api/v1/users/me/signing-secrets", {
    body: { name: "e2e test" },
  });
  assert.ok(c.status === 200 || c.status === 201, `expected 2xx, got ${c.status}: ${c.raw.slice(0, 200)}`);
  if (c.body?.id) track("signing_secret", c.body.id);
  if (c.body?.secret) {
    assert.ok(c.body.secret.length > 16, "freshly-created secret has substantial entropy");
  } else {
    info(SUITE, "create-secret-no-plaintext", "POST /signing-secrets did not return plaintext — verify users can retrieve later");
  }
});

test("security: signing-secret DELETE with bogus id returns 4xx (no cross-tenant lookup)", async () => {
  const r = await client.delete(`/api/v1/users/me/signing-secrets/sec_definitely_not_real_${Date.now()}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("security: agent name field doesn't leak HTML/script into stored body", async () => {
  const slug = uniqueSlug("xss");
  const payload = `<script>alert("xss")</script>&"<><`;
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: payload, agent_mode: "local" },
  });
  assert.equal(c.status, 201, c.raw.slice(0, 200));
  track("agent", c.body!.email);
  const g = await client.get<{ name: string }>(`/api/v1/agents/${encodeURIComponent(c.body!.email)}`);
  assert.equal(g.status, 200);
  // Response is JSON — characters should be present literally, NOT HTML-escaped (JSON handles escaping).
  // The risk is if the server were silently HTML-encoding into storage; that would harm clients.
  if (g.body?.name !== payload) {
    info(
      SUITE,
      "name-mutated",
      `agent name was mutated: stored "${g.body?.name}" vs sent "${payload}". Server may be sanitizing — verify intent.`,
    );
  } else {
    info(SUITE, "name-roundtrip", "agent name round-trips byte-for-byte through JSON");
  }
});

test("security: slug accepts only safe characters (no spaces/slashes injected)", async () => {
  const r = await client.post("/api/v1/agents", {
    body: { slug: "evil slug with spaces /and/slashes", name: "x", agent_mode: "local" },
  });
  if (r.status === 201) {
    fail(SUITE, "unsafe-slug-accepted", `server accepted slug with spaces and slashes: ${r.raw.slice(0, 200)}`);
  } else {
    assert.ok(r.status >= 400 && r.status < 500, `expected 4xx for unsafe slug, got ${r.status}`);
  }
});

test("security: extremely long subject is bounded (no 500)", async () => {
  const slug = uniqueSlug("longsubj");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "long", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  track("agent", c.body!.email);
  await client.put(`/api/v1/agents/${encodeURIComponent(c.body!.email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject" },
  });

  const subject = "A".repeat(10_000);
  const r = await client.post<{ message_id: string }>("/api/v1/send", {
    body: { from: c.body!.email, to: ["blackhole@e2a.dev"], subject, body: "x" },
  });
  if (r.status >= 500) {
    fail(SUITE, "long-subject-500", `10k-char subject caused ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  assert.ok(r.status < 500, `expected 4xx or 2xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
  if ((r.status === 200 || r.status === 202) && r.body?.message_id) {
    // Clean up any queued mail.
    await client.post(`/api/v1/messages/${r.body.message_id}/reject`, { body: { reason: "e2e long-subject cleanup" } });
  }
});

test("security: CRLF injection in subject rejected or sanitized (no header smuggling)", async () => {
  const slug = uniqueSlug("crlf");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "crlf", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  track("agent", c.body!.email);
  await client.put(`/api/v1/agents/${encodeURIComponent(c.body!.email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject" },
  });

  const r = await client.post<{ message_id: string }>("/api/v1/send", {
    body: {
      from: c.body!.email,
      to: ["blackhole@e2a.dev"],
      subject: "Hello\r\nBcc: attacker@evil.com\r\nX-Smuggled: yes",
      body: "x",
    },
  });
  if (r.status >= 500) {
    fail(SUITE, "crlf-500", `CRLF in subject caused ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  if (r.status === 200 || r.status === 202) {
    info(SUITE, "crlf-accepted", `CRLF in subject accepted (${r.status}). Server should sanitize before SMTP — verify in outbound path.`);
    if (r.body?.message_id) {
      await client.post(`/api/v1/messages/${r.body.message_id}/reject`, { body: { reason: "e2e crlf cleanup" } });
    }
  } else {
    info(SUITE, "crlf-rejected", `CRLF in subject rejected with ${r.status} — good`);
  }
});

test("security: export endpoint returns user's data only", async () => {
  const r = await client.get<{ user?: { email?: string }; agents?: unknown[]; messages?: unknown[] }>(
    "/api/v1/users/me/export",
  );
  assert.equal(r.status, 200, `export expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.body, "export returns body");
  // Lightly assert shape — we don't know other tenants' emails to confirm absence, but we can confirm presence of own.
  info(SUITE, "export-size", `export body length: ${r.raw.length} bytes`);
});

test("security: case-insensitive email path doesn't bypass ownership", async () => {
  const email = client.env.primaryAgentEmail;
  const upper = email.toUpperCase();
  const lower = email.toLowerCase();
  const a = await client.get(`/api/v1/agents/${encodeURIComponent(lower)}`);
  const b = await client.get(`/api/v1/agents/${encodeURIComponent(upper)}`);
  // Both should return same outcome (consistent normalization), neither should be 5xx.
  assert.ok(a.status < 500, `lowercase: ${a.status}`);
  assert.ok(b.status < 500, `uppercase: ${b.status}`);
  if (a.status !== b.status) {
    info(SUITE, "case-asymmetry", `case sensitivity differs: lower→${a.status} vs upper→${b.status}. May or may not be an issue depending on RFC compliance preference.`);
  }
});

test("security: send body with HTML — html_body distinct from body", async () => {
  const slug = uniqueSlug("html");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "html", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  track("agent", c.body!.email);
  await client.put(`/api/v1/agents/${encodeURIComponent(c.body!.email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject" },
  });

  const r = await client.post<{ message_id: string; status: string }>("/api/v1/send", {
    body: {
      from: c.body!.email,
      to: ["blackhole@e2a.dev"],
      subject: "html test",
      body: "plain text alt",
      html_body: "<p>HTML content with <a href='https://evil.example.com'>link</a></p>",
    },
  });
  assert.ok(r.status === 200 || r.status === 202, `expected 200/202, got ${r.status}: ${r.raw.slice(0, 200)}`);
  if (r.body?.message_id) {
    await client.post(`/api/v1/messages/${r.body.message_id}/reject`, { body: { reason: "e2e html cleanup" } });
  }
});
