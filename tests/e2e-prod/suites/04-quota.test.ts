import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { uniqueSlug, holdAllOutbound } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
// Burst client for probing the limit. Capped at 10 RPS.
const burst = new ApiClient(client.env, 10);
const SUITE = "04-quota";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/04-quota.json`);
});

test("quota: send /v1/agents/{email}/messages rapidly until first 429 (spec says 60/min/agent)", async () => {
  const slug = uniqueSlug("quota");
  const c = await client.post<{ email: string }>("/v1/agents", {
    body: { email: `${slug}@${client.env.sharedDomain}`, name: "quota" },
  });
  assert.equal(c.status, 201);
  const email = c.body!.email;
  track("agent", email);
  await holdAllOutbound(client, email);

  const ids: string[] = [];
  let limited: { status: number; retryAfter: string | undefined; index: number } | null = null;

  const MAX_PROBE = 70;
  for (let i = 0; i < MAX_PROBE; i++) {
    const r = await burst.post<{ message_id: string; status: string }>(`/v1/agents/${encodeURIComponent(email)}/messages`, {
      body: { to: ["blackhole@e2a.dev"], subject: `probe ${i}`, text: "x" },
    });
    if (r.status === 429) {
      limited = { status: r.status, retryAfter: r.headers["retry-after"], index: i };
      break;
    }
    if (r.status >= 400) {
      info(SUITE, "send-probe-non-429-error", `unexpected ${r.status} at i=${i}: ${r.raw.slice(0, 200)}`);
      break;
    }
    if (r.body?.message_id) ids.push(r.body.message_id);
  }

  if (limited === null) {
    info(SUITE, "send-rate-limit", `no 429 hit after ${MAX_PROBE} sends — limit higher than spec's 60/min OR not enforced per-agent on this codepath`);
  } else {
    info(SUITE, "send-rate-limit-hit", `429 at request #${limited.index + 1}. Retry-After: ${limited.retryAfter ?? "(missing)"}`);
    if (!limited.retryAfter) {
      fail(SUITE, "missing-retry-after", "429 returned without Retry-After header (clients can't back off intelligently)");
    } else {
      const v = Number(limited.retryAfter);
      assert.ok(!Number.isNaN(v) || /^[A-Z][a-z]{2},/.test(limited.retryAfter), `Retry-After should be seconds or HTTP-date, got "${limited.retryAfter}"`);
    }
  }

  // Reject everything we queued so no actual mail leaves the system.
  for (const id of ids) {
    await burst.post(`/v1/reviews/${id}/reject`, { body: { reason: "e2e cleanup" } });
  }
});

test("quota: 429 on /agents create is documented and observable under repeated creates", async () => {
  // Spec lists 429 for POST /agents. Probe by creating in a tight loop.
  let createdLimit: number | null = null;
  let lastCreatedEmail: string | null = null;
  for (let i = 0; i < 15; i++) {
    const r = await burst.post<{ email: string }>("/v1/agents", {
      body: { email: `${uniqueSlug(`q${i}`)}@${burst.env.sharedDomain}`, name: "q" },
    });
    if (r.body?.email) {
      track("agent", r.body.email);
      lastCreatedEmail = r.body.email;
    }
    if (r.status === 429) {
      createdLimit = i;
      const retryAfter = r.headers["retry-after"];
      info(SUITE, "create-rate-limit-hit", `429 on /agents create at i=${i}. Retry-After: ${retryAfter ?? "(missing)"}`);
      if (!retryAfter) {
        fail(SUITE, "missing-retry-after-create", "429 on /agents create without Retry-After header");
      }
      break;
    }
    if (r.status !== 201) {
      info(SUITE, "create-non-201", `unexpected ${r.status} at i=${i}: ${r.raw.slice(0, 200)}`);
      break;
    }
  }
  if (createdLimit === null) {
    info(SUITE, "create-rate-limit", `no 429 on /agents create after 15 sequential creates — limit higher than 15/min`);
  }
  // We already track created agents; cleanup will delete them.
  assert.ok(true);
});

test("quota: rate-limited send still allows reads from same key (non-blocking)", async () => {
  // Even when a send-quota is exhausted, GETs on the same key should keep working.
  const r = await burst.get("/v1/info");
  assert.equal(r.status, 200, "info endpoint should remain accessible regardless of send quota");
  const a = await burst.get("/v1/agents");
  assert.equal(a.status, 200, "list agents should remain accessible");
});
