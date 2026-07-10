import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { cleanup, track } from "../harness/cleanup.ts";
import { runId } from "../harness/fixtures.ts";
import { fail, info, warn, writeReport } from "../harness/report.ts";

const client = new ApiClient();
const SUITE = "10-domains";

// .example.com is reserved by RFC 2606 specifically for documentation/testing —
// safe to register-then-fail-to-verify without colliding with real DNS.
function fakeDomain(label: string): string {
  return `e2e-${runId()}-${label}-${Math.random().toString(36).slice(2, 8)}.example.com`;
}

after(async () => {
  const r = await cleanup(client);
  if (r.failed.length) warn(SUITE, "cleanup", `failed ${r.failed.length}`, r.failed);
  writeReport(`./reports/10-domains.json`);
});

test("domains: register returns 201 with DNS records + zero-counted Domain", async () => {
  const domain = fakeDomain("reg");
  const r = await client.post<{ domain: string; dns_records?: Array<{ type: string; name: string; value: string; priority?: number | null; purpose: string; status: string }>; agent_count?: number }>(
    "/v1/domains",
    { body: { domain } },
  );
  if (r.status !== 201) {
    fail(SUITE, "register-non-201", `expected 201, got ${r.status}: ${r.raw.slice(0, 240)}`);
    // Hard assert — the primary register test of the domains suite must
    // fail loudly if POST /domains regresses, not just record a JSON
    // finding behind a green node:test dot.
    assert.fail(`POST /v1/domains expected 201, got ${r.status}: ${r.raw.slice(0, 200)}`);
  }
  track("domain", domain);
  assert.equal(r.body?.domain, domain, "echoed domain matches");
  assert.ok(r.body?.dns_records, "dns_records present in 201 body");
  // agent_count is 0 right after registration on the single-domain endpoint.
  if (r.body?.agent_count !== undefined && r.body.agent_count !== 0) {
    info(SUITE, "register-agent-count", `agent_count = ${r.body.agent_count} immediately after register — spec says 0`);
  }
});

test("domains: GET /domains/{domain} returns same record after register", async () => {
  const domain = fakeDomain("get");
  const c = await client.post("/v1/domains", { body: { domain } });
  if (c.status !== 201) {
    info(SUITE, "register-skipped", `register returned ${c.status} — skipping get probe: ${c.raw.slice(0, 200)}`);
    return;
  }
  track("domain", domain);
  const g = await client.get<{ domain: string; dns_records?: unknown }>(`/v1/domains/${encodeURIComponent(domain)}`);
  assert.equal(g.status, 200, `GET expected 200, got ${g.status}: ${g.raw.slice(0, 200)}`);
  assert.equal(g.body?.domain, domain);
});

test("domains: list includes newly-registered domain", async () => {
  const domain = fakeDomain("list");
  const c = await client.post("/v1/domains", { body: { domain } });
  if (c.status !== 201) {
    info(SUITE, "list-skipped", `register returned ${c.status} — skipping list probe`);
    return;
  }
  track("domain", domain);
  const list = await client.get<{ items: Array<{ domain: string }>; next_cursor?: string | null }>("/v1/domains");
  assert.equal(list.status, 200);
  const found = list.body?.items?.some((d) => d.domain === domain);
  assert.ok(found, `freshly-registered ${domain} not in list response`);
});

test("domains: verify unowned-DNS domain returns 412 (TXT missing) with per-record diagnostic", async () => {
  const domain = fakeDomain("verify");
  const c = await client.post("/v1/domains", { body: { domain } });
  if (c.status !== 201) {
    info(SUITE, "verify-skipped", `register returned ${c.status} — skipping verify`);
    return;
  }
  track("domain", domain);
  const v = await client.post<{ domain: string; mx?: unknown; spf?: unknown; dkim?: unknown }>(
    `/v1/domains/${encodeURIComponent(domain)}/verify`,
    { body: {} },
  );
  // Spec: 200 if verified, 412 if TXT missing. Real DNS for our fake domain has no
  // matching TXT, so 412 is the expected path. Anything else (esp. 5xx) is a bug.
  if (v.status >= 500) {
    fail(SUITE, "verify-500", `verify on unowned-DNS domain returned ${v.status}: ${v.raw.slice(0, 200)}`);
    return;
  }
  assert.ok(v.status === 412 || v.status === 200, `verify expected 412 (or 200), got ${v.status}: ${v.raw.slice(0, 200)}`);
  if (v.status === 200) {
    info(SUITE, "verify-unexpected-200", `verify succeeded against ${domain} — unexpected (we don't control DNS)`);
  } else {
    // 412 should still carry the diagnostic body per spec.
    assert.equal(v.body?.domain, domain, "412 body still includes domain field");
  }
});

test("domains: DELETE returns 204 and removes from list", async () => {
  const domain = fakeDomain("del");
  const c = await client.post("/v1/domains", { body: { domain } });
  if (c.status !== 201) {
    info(SUITE, "delete-skipped", `register returned ${c.status} — skipping delete probe`);
    return;
  }
  // Don't track — this test consumes it.
  // DELETE is irreversible (deprovisions the sending identity) and requires
  // the ?confirm=DELETE guard — without it the server returns 400 confirmation_required.
  const del = await client.delete(`/v1/domains/${encodeURIComponent(domain)}?confirm=DELETE`);
  assert.equal(del.status, 204, `DELETE expected 204, got ${del.status}: ${del.raw.slice(0, 200)}`);
  const after = await client.get(`/v1/domains/${encodeURIComponent(domain)}`);
  assert.ok(after.status === 404 || after.status === 403, `deleted domain should 404/403, got ${after.status}`);
});

test("domains: DELETE of a domain with agents on it fails (400)", async () => {
  const domain = fakeDomain("inuse");
  const c = await client.post("/v1/domains", { body: { domain } });
  if (c.status !== 201) {
    info(SUITE, "in-use-skipped", `register returned ${c.status} — skipping in-use probe`);
    return;
  }
  track("domain", domain);
  // Spec says "Agents still exist on this domain" → 400. But we can only attach an
  // agent to a verified domain. Our fake-domain register won't verify, so we can't
  // create an agent on it. Surface this as a coverage limit rather than a test.
  info(
    SUITE,
    "in-use-coverage-limit",
    "cannot exercise 'delete with agents attached' on .example.com (no DNS verify possible) — needs a verified domain fixture",
  );
});

test("domains: register malformed domain returns 4xx", async () => {
  const r = await client.post("/v1/domains", { body: { domain: "not a domain, just garbage" } });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx for bad domain, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("domains: register empty body returns 4xx", async () => {
  const r = await client.post("/v1/domains", { body: {} });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx for empty body, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("domains: re-register same domain (same account) is idempotent — 201", async () => {
  // Per openapi, 409 (code domain_taken) fires only when the domain is claimed by
  // ANOTHER account. Re-registering a domain you already own is idempotent and
  // returns 201 with the same DomainView — a single-tenant conformance run can't
  // exercise the cross-account 409 path, so we assert the idempotent same-account
  // behavior instead and flag the 409 path as a coverage limit.
  const domain = fakeDomain("dup");
  const first = await client.post<{ domain: string }>("/v1/domains", { body: { domain } });
  if (first.status !== 201) {
    info(SUITE, "dup-skipped", `first register returned ${first.status} — skipping dup probe`);
    return;
  }
  track("domain", domain);
  const second = await client.post<{ domain: string }>("/v1/domains", { body: { domain } });
  assert.equal(second.status, 201, `same-account re-register expected idempotent 201, got ${second.status}: ${second.raw.slice(0, 200)}`);
  assert.equal(second.body?.domain, domain, "idempotent re-register echoes the same domain");
  info(SUITE, "dup-cross-account-coverage-limit", "409 domain_taken requires a domain claimed by another account — not exercisable single-tenant");
});

test("domains: GET unowned domain returns 4xx (no info leak)", async () => {
  const r = await client.get(`/v1/domains/${encodeURIComponent("not-mine-domain-987654321.example.com")}`);
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("domains: DELETE unowned domain returns 4xx (no cross-tenant delete)", async () => {
  const r = await client.delete(`/v1/domains/${encodeURIComponent("not-mine-domain-987654321.example.com")}`);
  if (r.status === 200 || r.status === 204) {
    fail(SUITE, "cross-tenant-domain-delete", "CRITICAL: deleted a domain we don't own");
  }
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}`);
});

test("domains: verify nonexistent domain returns 404", async () => {
  const r = await client.post(`/v1/domains/${encodeURIComponent("definitely-not-registered-" + Date.now() + ".example.com")}/verify`, { body: {} });
  assert.ok(r.status === 404 || (r.status >= 400 && r.status < 500), `expected 404/4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("domains: PATCH on nonexistent domain returns 4xx", async () => {
  const r = await client.patch(`/v1/domains/${encodeURIComponent("definitely-not-registered-" + Date.now() + ".example.com")}`, {
    body: { is_primary: true },
  });
  assert.ok(r.status >= 400 && r.status < 500, `expected 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("domains: PATCH with is_primary=true on owned domain promotes it (200) [needs verified domain]", async () => {
  // Promotion only works on verified domains — we can't create one. Document the gap.
  info(SUITE, "primary-promotion-coverage-limit", "is_primary promotion requires a verified domain; can't exercise on a fake test domain");
});
