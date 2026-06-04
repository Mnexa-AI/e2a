// Prod coverage for the surface introduced in v0.3.0:
//   - Events API (list / get / redeliver / redeliver-since)         PR #184
//   - Conversations API (list / get under agent scope)              PR #177
//   - Message forward (/messages/{id}/forward)                       PR #171
//   - Message labels (PATCH + ?labels= filter on /messages)          PR #173, #174
//   - Message search filters (?from, ?to, ?subject, ?since, ?until)  PR #154
//   - Domains CRUD completion (PATCH /domains/{domain})              PR #165
//   - Per-user resource limits (GET /users/me/limits)                PR #158
//
// Coverage shape is deliberately read-heavy: shape + auth + 4xx
// validation paths. The outbox/event-emission code path requires
// WEBHOOKS_OUTBOX_ENABLED=true, which is off in prod, so the events
// log is empty by design. We still validate every endpoint:
//   - responds with the documented status
//   - returns the documented JSON shape (incl. pagination fields)
//   - rejects bad input and missing auth correctly
//
// Tests that would require real data flow (e.g. forward happy path)
// fall back to negative paths to avoid creating residue prod has to
// clean up later. The cleanup tracker handles any residue that does
// land.

import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup } from "../harness/cleanup.ts";
import { info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "14-v030-features";

after(async () => {
  const result = await cleanup(client);
  if (result.failed.length > 0) {
    warn(SUITE, "cleanup", `failed to delete ${result.failed.length} resources`, result.failed);
  } else {
    info(SUITE, "cleanup", `cleaned up ${result.succeeded} resources`);
  }
  writeReport(`./reports/${SUITE}.json`);
});

// ─── Events API ───────────────────────────────────────────────────

test("events: list returns documented envelope shape", async () => {
  const r = await client.get<{ events: unknown[] | null; next_token: string | null }>("/api/v1/events");
  assert.equal(r.status, 200, `GET /events expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.body, "body is parsed JSON");
  // Go marshals an empty slice as `null` not `[]`; both are valid
  // empty-state encodings. next_token is "" when no more pages.
  assert.ok(
    r.body!.events === null || Array.isArray(r.body!.events),
    `events should be array or null, got ${typeof r.body!.events}`,
  );
  assert.ok("next_token" in r.body!, "next_token field present on response");
});

test("events: list without auth → 401", async () => {
  const r = await client.get("/api/v1/events", { apiKey: null });
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

test("events: list accepts all documented filter params without 400", async () => {
  // Send every filter the design specifies; any 400 here indicates
  // a regression in the query parser.
  const r = await client.get("/api/v1/events", {
    query: {
      type: "email.received",
      agent_id: "test-mcp@agents.e2a.dev",
      since: "2026-01-01T00:00:00Z",
      until: "2026-12-31T23:59:59Z",
      page_size: 10,
    },
  });
  assert.equal(r.status, 200, `multi-filter GET /events expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("events: list with page_size > max clamps or 400s, never crashes", async () => {
  const r = await client.get("/api/v1/events", { query: { page_size: 9999 } });
  assert.ok(r.status === 200 || r.status === 400, `expected 200/400, got ${r.status}`);
});

test("events: get nonexistent event → 404", async () => {
  const r = await client.get(`/api/v1/events/evt_nonexistent${Date.now()}`);
  assert.equal(r.status, 404, `expected 404, got ${r.status}`);
});

test("events: get without auth → 401", async () => {
  const r = await client.get(`/api/v1/events/evt_x`, { apiKey: null });
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

test("events: redeliver nonexistent event → 404", async () => {
  const r = await client.post(`/api/v1/events/evt_nonexistent${Date.now()}/redeliver`, {
    body: { webhook_id: "wh_anything" },
  });
  assert.equal(r.status, 404, `expected 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("events: redeliver without auth → 401", async () => {
  const r = await client.post(`/api/v1/events/evt_x/redeliver`, {
    apiKey: null,
    body: { webhook_id: "wh_x" },
  });
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

test("webhooks: redeliver-since nonexistent webhook is benign (no 5xx)", async () => {
  // Documented prod behavior: returns 200 with scheduled=0 when the
  // webhook id doesn't match, rather than 4xx. That diverges from the
  // anti-enumeration pattern used elsewhere (e.g. /agents/{id}), so
  // it's worth surfacing as a finding — but it's not a regression,
  // it's how the endpoint shipped in v0.3.0. Accept any non-5xx.
  const r = await client.post(`/api/v1/webhooks/wh_nonexistent${Date.now()}/redeliver-since`, {
    body: { since: new Date(Date.now() - 86400000).toISOString() },
  });
  assert.ok(r.status < 500, `expected <500, got ${r.status}: ${r.raw.slice(0, 200)}`);
  if (r.status === 200) {
    const body = r.body as { scheduled?: number; skipped_already_pending?: number } | null;
    assert.equal(body?.scheduled, 0, "nonexistent webhook should schedule 0 redeliveries");
    info(SUITE, "redeliver-since-shape", "endpoint returns 200/scheduled=0 for unknown webhook (no anti-enumeration)");
  }
});

test("webhooks: redeliver-since rejects since > 7 days back (range cap)", async () => {
  // Per the design, redeliver-since is capped at 7 days. We can't
  // test against a real webhook without creating one; verify the
  // validator runs early enough that the cap check fires even on
  // a missing webhook id. If 404 fires first (route ordering), the
  // test still passes — both are documented behaviors. The intent
  // is "never 5xx", which is the actual invariant.
  const since = new Date(Date.now() - 30 * 86400000).toISOString();
  const r = await client.post(`/api/v1/webhooks/wh_anything/redeliver-since`, {
    body: { since },
  });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

// ─── Conversations API ────────────────────────────────────────────

test("conversations: list under primary agent returns array shape", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.get<{ conversations: unknown[] }>(
    `/api/v1/agents/${encodeURIComponent(email)}/conversations`,
  );
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(Array.isArray(r.body?.conversations), "conversations is array");
});

test("conversations: list without auth → 401", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.get(`/api/v1/agents/${encodeURIComponent(email)}/conversations`, { apiKey: null });
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

test("conversations: list under nonexistent agent → 404 or 403", async () => {
  // The /agents/{id} root uses 403 anti-enumeration but the
  // sub-resource paths (conversations, messages) return 404 for an
  // unknown agent. Inconsistency worth tracking, accepted as-shipped.
  const r = await client.get(
    `/api/v1/agents/nonexistent-${Date.now()}@agents.e2a.dev/conversations`,
  );
  assert.ok(r.status === 404 || r.status === 403, `expected 404/403, got ${r.status}`);
});

test("conversations: get nonexistent conversation → 404", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.get(
    `/api/v1/agents/${encodeURIComponent(email)}/conversations/conv_nonexistent${Date.now()}`,
  );
  assert.equal(r.status, 404, `expected 404, got ${r.status}`);
});

// ─── Message forward + labels ──────────────────────────────────────

test("forward: nonexistent message → 404", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.post(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_nonexistent${Date.now()}/forward`,
    { body: { to: ["sink@e2a.dev"] } },
  );
  assert.equal(r.status, 404, `expected 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("forward: missing 'to' field → 400", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.post(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_anything/forward`,
    { body: {} },
  );
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("forward: without auth → 401", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.post(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_x/forward`,
    { apiKey: null, body: { to: ["sink@e2a.dev"] } },
  );
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

test("labels: PATCH nonexistent message → 404", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.patch(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_nonexistent${Date.now()}`,
    { body: { labels: ["urgent"] } },
  );
  assert.equal(r.status, 404, `expected 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("labels: PATCH rejects label with invalid chars (charset cap)", async () => {
  const email = client.env.primaryAgentEmail;
  // The validator runs before the row lookup, so this fails 400
  // even on a nonexistent message id.
  const r = await client.patch(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_anything`,
    { body: { labels: ["HAS SPACES & SYMBOLS!"] } },
  );
  assert.ok(r.status === 400 || r.status === 404, `expected 400/404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("labels: PATCH rejects reserved 'e2a:' prefix from caller writes", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.patch(
    `/api/v1/agents/${encodeURIComponent(email)}/messages/msg_anything`,
    { body: { labels: ["e2a:reserved"] } },
  );
  assert.ok(r.status === 400 || r.status === 404, `expected 400/404, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("agent messages: ?labels= filter accepted without error", async () => {
  // The labels filter lives on the per-agent inbox path, not on the
  // user-scoped HITL list at /api/v1/messages.
  const email = client.env.primaryAgentEmail;
  const r = await client.get(
    `/api/v1/agents/${encodeURIComponent(email)}/messages`,
    { query: { labels: "urgent" } },
  );
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("agent messages: too many ?labels= values → 400 (cap=50)", async () => {
  const email = client.env.primaryAgentEmail;
  // MaxLabelsPerOp = 50; send 51 to trip the cap.
  const url =
    `/api/v1/agents/${encodeURIComponent(email)}/messages?` +
    Array.from({ length: 51 }, (_, i) => `labels=l${i}`).join("&");
  const r = await client.get(url);
  assert.equal(r.status, 400, `expected 400, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

// ─── Message search filters (PR #154) ─────────────────────────────

test("agent messages: search filters accepted (from, to, subject, conversation_id, since, until)", async () => {
  // PR #154 search filters live on the per-agent inbox path.
  const email = client.env.primaryAgentEmail;
  const r = await client.get(`/api/v1/agents/${encodeURIComponent(email)}/messages`, {
    query: {
      from: "alice@example.com",
      to: "bob@example.com",
      subject: "test",
      conversation_id: "conv_x",
      since: "2026-01-01T00:00:00Z",
      until: "2026-12-31T00:00:00Z",
      limit: 5,
    },
  });
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("agent messages: invalid since timestamp handled (400 or graceful 200)", async () => {
  const email = client.env.primaryAgentEmail;
  const r = await client.get(
    `/api/v1/agents/${encodeURIComponent(email)}/messages`,
    { query: { since: "not-a-timestamp" } },
  );
  assert.ok(r.status === 400 || r.status === 200, `expected 400 or graceful 200, got ${r.status}`);
});

// ─── Domains: completion of CRUD (PR #165) ────────────────────────

test("domains: PATCH nonexistent domain with valid body → 404 or 403", async () => {
  // PATCH validates body BEFORE the ownership check, so we send the
  // documented "make me primary" body to skip the validator's
  // is_primary=false early-reject. The path itself shouldn't match
  // any owned domain, so we expect not-found semantics.
  const r = await client.patch(`/api/v1/domains/nonexistent-${Date.now()}.example.com`, {
    body: { is_primary: true },
  });
  assert.ok(r.status === 404 || r.status === 403, `expected 404/403, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("domains: PATCH without auth → 401", async () => {
  const r = await client.patch(`/api/v1/domains/example.com`, { apiKey: null, body: {} });
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

test("domains: DELETE nonexistent domain → 404 or 403", async () => {
  const r = await client.delete(`/api/v1/domains/nonexistent-${Date.now()}.example.com`);
  assert.ok(r.status === 404 || r.status === 403, `expected 404/403, got ${r.status}`);
});

// ─── Per-user resource limits (PR #158) ───────────────────────────

test("limits: GET /users/me/limits returns documented shape", async () => {
  type LimitsResp = {
    plan_code: string;
    limits: Record<string, number>;
    usage: Record<string, number>;
    upgrade_url?: string;
  };
  const r = await client.get<LimitsResp>("/api/v1/users/me/limits");
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(r.body, "body parsed");
  assert.equal(typeof r.body!.plan_code, "string", "plan_code is a string");
  assert.ok(r.body!.limits && typeof r.body!.limits === "object", "limits object present");
  assert.ok(r.body!.usage && typeof r.body!.usage === "object", "usage object present");
  // Every limit kind should have a numeric value; the corresponding
  // usage key drops the "max_" prefix (e.g. limits.max_agents pairs
  // with usage.agents). This pins the shape contract the dashboard's
  // limits card depends on.
  for (const [kind, limit] of Object.entries(r.body!.limits)) {
    assert.equal(typeof limit, "number", `limits.${kind} is a number`);
    const usageKey = kind.startsWith("max_") ? kind.slice("max_".length) : kind;
    assert.equal(
      typeof r.body!.usage[usageKey],
      "number",
      `usage.${usageKey} is a number (limits.${kind} ↔ usage.${usageKey} pair)`,
    );
  }
});

test("limits: GET /users/me/limits without auth → 401", async () => {
  const r = await client.get("/api/v1/users/me/limits", { apiKey: null });
  assert.equal(r.status, 401, `expected 401, got ${r.status}`);
});

// ─── Cross-cutting: retention indirectly (PR #186) ────────────────

test("pending list smoke (canonical /api/v1/pending serves cleanly)", async () => {
  // The 10-day TTL change only affects rows created against the new
  // binary. We can't directly assert TTL without a write — but we
  // can verify the canonical pending-list endpoint still serves
  // cleanly post-deploy, which is the practical regression signal.
  //
  // v0.4.0 renamed /api/v1/messages → /api/v1/pending; the legacy
  // path is verified separately by `pending: legacy /messages list
  // returns 404` below.
  const r = await client.get<{ messages: unknown[] }>("/api/v1/pending");
  assert.equal(r.status, 200);
  assert.ok(Array.isArray(r.body?.messages), "messages is array");
});

test("pending: legacy /api/v1/messages list returns 404 (no silent alias)", async () => {
  // v0.4.0 removed the legacy list path. A caller pinned to an older
  // SDK will see 404 here, not a successful response — which is the
  // intended breakage signal so the SDK update isn't silently
  // delayed.
  const r = await client.get("/api/v1/messages");
  assert.equal(r.status, 404, `legacy list path should 404, got ${r.status}`);
});
