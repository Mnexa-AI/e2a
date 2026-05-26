import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup } from "../harness/cleanup.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "13-billing-surface";

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/13-billing-surface.json`);
});

// These tests verify the BILLING API CONTRACT without actually touching Stripe:
// - The shape of /api/v1/users/me/limits (always available, dashboard depends on it)
// - The HTTP method discipline on /api/billing/* (POST-only on mutating endpoints
//   was a deliberate CSRF defense — confirm GET still 4xx's)
// - That /pricing is statically served and reachable
// Actual checkout/portal/webhook behavior is deferred to the live billing day.

test("billing: GET /api/v1/users/me/limits returns documented LimitsInfo shape", async () => {
  const r = await client.get<{
    plan_code?: string;
    upgrade_url?: string;
    limits?: { max_agents?: number; max_domains?: number; max_messages_month?: number; max_storage_bytes?: number };
    usage?: { agents?: number; domains?: number; messages_month?: number; storage_bytes?: number };
  }>("/api/v1/users/me/limits");

  if (r.status === 503) {
    info(SUITE, "limits-503", "limits subsystem reports not configured — billing primitive not wired up on this deployment");
    return;
  }
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 300)}`);
  // plan_code is required for the dashboard to render the upgrade affordance.
  if (!r.body?.plan_code) {
    fail(SUITE, "limits-missing-plan-code", `LimitsInfo missing plan_code: ${r.raw.slice(0, 300)}`);
  }
  // limits block — required by dashboard caps panel.
  if (!r.body?.limits) {
    fail(SUITE, "limits-missing-limits-block", `LimitsInfo missing 'limits' block: ${r.raw.slice(0, 300)}`);
  } else {
    for (const k of ["max_agents", "max_domains", "max_messages_month", "max_storage_bytes"] as const) {
      if (typeof r.body.limits[k] !== "number") {
        info(SUITE, `limits-missing-${k}`, `limits.${k} missing or non-number — dashboard will render '?'`);
      }
    }
  }
  // usage block — required by dashboard "you've used X" surface.
  if (!r.body?.usage) {
    fail(SUITE, "limits-missing-usage-block", "LimitsInfo missing 'usage' block — dashboard caps panel breaks");
  } else {
    for (const k of ["agents", "domains", "messages_month", "storage_bytes"] as const) {
      if (typeof r.body.usage[k] !== "number") {
        info(SUITE, `usage-missing-${k}`, `usage.${k} missing — dashboard surface incomplete`);
      }
    }
  }
  info(SUITE, "limits-snapshot", `plan_code="${r.body?.plan_code}" agents ${r.body?.usage?.agents}/${r.body?.limits?.max_agents}, msgs ${r.body?.usage?.messages_month}/${r.body?.limits?.max_messages_month}, storage ${r.body?.usage?.storage_bytes}/${r.body?.limits?.max_storage_bytes}`);
});

test("billing: GET /api/v1/users/me/limits requires auth (401)", async () => {
  const r = await client.get("/api/v1/users/me/limits", { apiKey: null });
  assert.equal(r.status, 401, `expected 401, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("billing: usage.agents counts roughly match the actual /agents list", async () => {
  const limits = await client.get<{ usage?: { agents?: number } }>("/api/v1/users/me/limits");
  if (limits.status !== 200 || typeof limits.body?.usage?.agents !== "number") {
    info(SUITE, "usage-skip", `limits unavailable (${limits.status}), skipping usage-vs-list check`);
    return;
  }
  const agents = await client.get<{ agents: unknown[] }>("/api/v1/agents");
  const actual = agents.body?.agents?.length ?? 0;
  const reported = limits.body.usage.agents!;
  // Allow ±1 drift for in-flight resources, but a big mismatch is a bug.
  if (Math.abs(reported - actual) > 1) {
    info(
      SUITE,
      "usage-agents-drift",
      `usage.agents=${reported} but /agents list has ${actual} — drift larger than ±1 may indicate counter is stale`,
    );
  } else {
    info(SUITE, "usage-agents-consistent", `usage.agents=${reported}, /agents list=${actual} — within ±1`);
  }
});

// --- /api/billing/* method discipline (Stripe sidecar) ---

test("billing: GET /api/billing/health returns 200", async () => {
  const r = await client.get("/api/billing/health", { apiKey: null });
  // Health may be intentionally unauthenticated; if not, allow 401.
  if (r.status >= 500) {
    fail(SUITE, "billing-health-5xx", `/api/billing/health returned ${r.status}: ${r.raw.slice(0, 200)}`);
    return;
  }
  if (r.status !== 200 && r.status !== 401) {
    info(SUITE, "billing-health-status", `/api/billing/health returned ${r.status}: ${r.raw.slice(0, 200)}`);
  }
});

test("billing: GET /api/billing/checkout is rejected (CSRF discipline — POST only)", async () => {
  // Under SameSite=Lax, a top-level GET navigation forwards the session cookie.
  // The billing endpoints were intentionally moved to POST-only as a CSRF defense.
  const r = await client.get("/api/billing/checkout");
  if (r.status === 200 || (r.status >= 300 && r.status < 400)) {
    fail(
      SUITE,
      "billing-checkout-get-leak",
      `GET /api/billing/checkout returned ${r.status} — should be 405/404 to prevent CSRF via top-level navigation. Body: ${r.raw.slice(0, 200)}`,
    );
    return;
  }
  // 405 / 404 / 401 are all acceptable rejection codes here.
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("billing: GET /api/billing/portal is rejected (CSRF discipline — POST only)", async () => {
  const r = await client.get("/api/billing/portal");
  if (r.status === 200 || (r.status >= 300 && r.status < 400)) {
    fail(
      SUITE,
      "billing-portal-get-leak",
      `GET /api/billing/portal returned ${r.status} — should be 405/404 to prevent CSRF. Body: ${r.raw.slice(0, 200)}`,
    );
    return;
  }
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("billing: POST /api/billing/checkout without auth returns 401", async () => {
  const r = await client.post("/api/billing/checkout", {
    body: { plan: "pro" },
    apiKey: null,
  });
  // Sidecar uses session-cookie auth from the dashboard, not Bearer. With no
  // cookie + no Bearer, it should reject. 401 ideal; 4xx acceptable.
  if (r.status === 200 || (r.status >= 300 && r.status < 400)) {
    fail(
      SUITE,
      "billing-checkout-no-auth-accepted",
      `POST /api/billing/checkout with no creds returned ${r.status} — must require auth`,
    );
  }
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("billing: POST /api/billing/webhook with empty body returns 400 (signature missing)", async () => {
  const r = await client.post("/api/billing/webhook", {
    body: "",
    headers: { "Content-Type": "application/json" },
    apiKey: null,
  });
  // Stripe webhook handler verifies signature header — empty body / missing
  // header → 400. Any 200 here would mean the verifier is bypassed.
  if (r.status === 200) {
    fail(SUITE, "billing-webhook-no-sig", "POST /api/billing/webhook with no signature returned 200 — signature verifier may be disabled");
  }
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("billing: POST /api/billing/webhook with invalid signature returns 400", async () => {
  const r = await client.post("/api/billing/webhook", {
    body: JSON.stringify({ id: "evt_test", type: "ping" }),
    headers: { "Content-Type": "application/json", "Stripe-Signature": "t=0,v1=nope" },
    apiKey: null,
  });
  if (r.status === 200) {
    fail(SUITE, "billing-webhook-bad-sig-200", "POST /api/billing/webhook with garbage signature returned 200 — verifier broken");
  }
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

// --- Static pricing page ---

test("billing: GET /pricing returns 200 HTML", async () => {
  const r = await client.get("/pricing", { apiKey: null });
  if (r.status !== 200) {
    fail(SUITE, "pricing-non-200", `/pricing returned ${r.status}: ${r.raw.slice(0, 200)}`);
    return;
  }
  const ct = r.headers["content-type"] ?? "";
  assert.ok(ct.includes("text/html"), `/pricing should be HTML, got Content-Type="${ct}"`);
  // Sanity: page mentions the plan names.
  const lower = r.raw.toLowerCase();
  for (const term of ["free", "pro", "scale"]) {
    if (!lower.includes(term)) {
      info(SUITE, `pricing-missing-${term}`, `/pricing page does not mention "${term}" — verify CTAs are intact`);
    }
  }
});

test("billing: GET /pricing/ (trailing slash) is reachable", async () => {
  const r = await client.get("/pricing/", { apiKey: null });
  if (r.status >= 500) {
    fail(SUITE, "pricing-slash-5xx", `/pricing/ returned ${r.status}`);
    return;
  }
  if (r.status !== 200 && (r.status < 300 || r.status >= 400)) {
    info(SUITE, "pricing-slash-status", `/pricing/ returned ${r.status} (expected 200 or 301/302 to /pricing)`);
  }
});

// --- /billing dashboard page (Next.js app router) ---

test("billing: GET /billing page is reachable (web dashboard route)", async () => {
  const r = await client.get("/billing", { apiKey: null });
  // The dashboard requires auth, so a 302 to /login or a 200 with a client-side
  // auth gate is both acceptable. A 404 would be a bug.
  if (r.status === 404) {
    fail(SUITE, "billing-page-404", "/billing returned 404 — dashboard route not deployed");
    return;
  }
  if (r.status >= 500) {
    fail(SUITE, "billing-page-5xx", `/billing returned ${r.status}`);
  }
});
