import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { info, warn, writeReport, fail } from "../harness/report.ts";

const client = new ApiClient();
// Burst client for fan-out tests; capped at 5 RPS so we stay polite on prod.
const burst = new ApiClient(client.env, 5);
const SUITE = "03-concurrency";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/03-concurrency.json`);
});

test("concurrency: 5 parallel creates with distinct slugs all succeed", async () => {
  const slugs = Array.from({ length: 5 }, () => uniqueSlug("par"));
  const results = await Promise.all(
    slugs.map((slug) =>
      burst.post<{ email: string }>("/api/v1/agents", { body: { slug, name: "par", agent_mode: "local" } }),
    ),
  );
  for (const r of results) {
    assert.equal(r.status, 201, `parallel create failed: ${r.status} ${r.raw.slice(0, 200)}`);
    if (r.body?.email) track("agent", r.body.email);
  }
});

test("concurrency: 5 parallel creates with the SAME slug — exactly one wins, rest 409/4xx", async () => {
  const slug = uniqueSlug("race");
  const results = await Promise.all(
    Array.from({ length: 5 }, () =>
      burst.post<{ email: string }>("/api/v1/agents", { body: { slug, name: "race", agent_mode: "local" } }),
    ),
  );
  const successes = results.filter((r) => r.status === 201);
  const conflicts = results.filter((r) => r.status === 409);
  const otherFails = results.filter((r) => r.status !== 201 && r.status !== 409);

  assert.equal(successes.length, 1, `expected exactly 1 success, got ${successes.length}: ${results.map((r) => r.status).join(",")}`);
  for (const w of successes) if (w.body?.email) track("agent", w.body.email);

  if (otherFails.length > 0) {
    info(
      SUITE,
      "race-non-409",
      `${otherFails.length} losing creates returned ${otherFails.map((r) => r.status).join(",")} instead of 409 (spec). Body samples: ${otherFails.map((r) => r.raw.slice(0, 80)).join(" | ")}`,
    );
  } else {
    info(SUITE, "race-clean", `all ${conflicts.length} losers returned 409 cleanly`);
  }
});

test("concurrency: parallel reads of same agent return consistent body", async () => {
  const slug = uniqueSlug("cr");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "consistency", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  track("agent", email);

  const reads = await Promise.all(
    Array.from({ length: 8 }, () => burst.get<{ email: string; hitl_enabled: boolean }>(`/api/v1/agents/${encodeURIComponent(email)}`)),
  );
  for (const r of reads) {
    assert.equal(r.status, 200);
    assert.equal(r.body?.email, email);
  }
  const bodies = new Set(reads.map((r) => JSON.stringify(r.body)));
  assert.equal(bodies.size, 1, `expected identical bodies under parallel read, got ${bodies.size} distinct`);
});

test("concurrency: parallel PUT toggles converge to a final state (no 500)", async () => {
  const slug = uniqueSlug("toggle");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "toggle", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  track("agent", email);

  const ops = await Promise.all([
    burst.put(`/api/v1/agents/${encodeURIComponent(email)}`, { body: { hitl_enabled: true } }),
    burst.put(`/api/v1/agents/${encodeURIComponent(email)}`, { body: { hitl_enabled: false } }),
    burst.put(`/api/v1/agents/${encodeURIComponent(email)}`, { body: { hitl_enabled: true } }),
    burst.put(`/api/v1/agents/${encodeURIComponent(email)}`, { body: { hitl_enabled: false } }),
  ]);
  for (const r of ops) {
    if (r.status >= 500) {
      fail(SUITE, "parallel-put-500", `PUT returned ${r.status} under concurrent updates: ${r.raw.slice(0, 200)}`);
    }
    assert.ok(r.status < 500, `no 5xx under contention, got ${r.status}`);
  }
  // Final state should be one of the toggled values, not corrupted.
  const final = await client.get<{ hitl_enabled: boolean }>(`/api/v1/agents/${encodeURIComponent(email)}`);
  assert.equal(final.status, 200);
  assert.equal(typeof final.body?.hitl_enabled, "boolean");
});

test("concurrency: parallel DELETE of the same agent — one succeeds, rest 403/4xx, never 500", async () => {
  const slug = uniqueSlug("del");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "del", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  // Don't track — this test consumes it.

  const results = await Promise.all(
    Array.from({ length: 4 }, () => burst.delete(`/api/v1/agents/${encodeURIComponent(email)}`)),
  );
  const ok = results.filter((r) => r.status === 200 || r.status === 204);
  const fivexx = results.filter((r) => r.status >= 500);
  assert.equal(fivexx.length, 0, `no 5xx under parallel delete, got: ${results.map((r) => r.status).join(",")}`);
  assert.ok(ok.length >= 1, `at least one delete should succeed, got ${ok.length}: ${results.map((r) => r.status).join(",")}`);
  if (ok.length > 1) {
    info(SUITE, "delete-idempotent", `${ok.length} parallel deletes all returned 2xx — endpoint is idempotent (fine, but worth noting)`);
  }
});

test("concurrency: 8 parallel sends from HITL agent — all queue (no dropped/duplicated)", async () => {
  const slug = uniqueSlug("hitlconc");
  const c = await client.post<{ email: string }>("/api/v1/agents", {
    body: { slug, name: "hitl-conc", agent_mode: "local" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  track("agent", email);
  const u = await client.put(`/api/v1/agents/${encodeURIComponent(email)}`, {
    body: { hitl_enabled: true, hitl_expiration_action: "reject", hitl_ttl_seconds: 60 },
  });
  assert.equal(u.status, 200);

  const N = 8;
  const sends = await Promise.all(
    Array.from({ length: N }, (_, i) =>
      burst.post<{ message_id: string; status: string }>("/api/v1/send", {
        body: {
          from: email,
          to: ["blackhole@e2a.dev"],
          subject: `parallel ${i}`,
          body: `parallel send #${i}`,
        },
      }),
    ),
  );

  const ids = new Set<string>();
  for (const r of sends) {
    assert.ok(r.status === 202 || r.status === 200, `parallel send: status ${r.status}, body: ${r.raw.slice(0, 200)}`);
    assert.ok(r.body?.message_id?.startsWith("msg_"), `message_id present and prefixed`);
    ids.add(r.body!.message_id);
  }
  assert.equal(ids.size, N, `expected ${N} distinct message_ids, got ${ids.size}`);

  // Best-effort reject all so no actual mail leaves the system.
  for (const id of ids) {
    await client.post(`/api/v1/messages/${id}/reject`, { body: { reason: "e2e cleanup" } });
  }
});
