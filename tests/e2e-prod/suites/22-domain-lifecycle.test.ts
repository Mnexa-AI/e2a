import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { writeReport, info } from "../harness/report.ts";

// The FULL custom-domain lifecycle — the one flow prod e2e structurally can't do
// because it needs REAL DNS: register → publish the verify TXT → verifyDomain
// HAPPY PATH → create a custom-domain agent → delete → tear down DNS.
//
// It runs against an ISOLATED Cloudflare zone (never prod e2a.dev), so the
// DNS:Edit token can't touch production records. Opt-in via three env vars,
// skips cleanly when absent:
//   CLOUDFLARE_API_TOKEN  — a DNS:Edit token scoped to the conformance zone ONLY
//   CLOUDFLARE_ZONE_ID    — that zone's id
//   CLOUDFLARE_ZONE_NAME  — that zone's apex (e.g. ct.e2a.dev); conformance
//                           domains are minted as <slug>.<zone-name>
const SUITE = "22-domain-lifecycle";
const client = new ApiClient();

const CF_TOKEN = process.env.CLOUDFLARE_API_TOKEN;
const CF_ZONE = process.env.CLOUDFLARE_ZONE_ID;
const CF_ZONE_NAME = process.env.CLOUDFLARE_ZONE_NAME;
const skip =
  CF_TOKEN && CF_ZONE && CF_ZONE_NAME
    ? false
    : "CLOUDFLARE_API_TOKEN + CLOUDFLARE_ZONE_ID + CLOUDFLARE_ZONE_NAME not set (isolated conformance DNS zone)";

const CF_API = "https://api.cloudflare.com/client/v4";

// cfCreateTxt publishes a TXT record in the isolated zone and returns its id.
async function cfCreateTxt(name: string, content: string): Promise<string> {
  const res = await fetch(`${CF_API}/zones/${CF_ZONE}/dns_records`, {
    method: "POST",
    headers: { Authorization: `Bearer ${CF_TOKEN}`, "Content-Type": "application/json" },
    body: JSON.stringify({ type: "TXT", name, content, ttl: 60, comment: "e2a conformance domain-lifecycle (temporary)" }),
  });
  const j = (await res.json()) as { success: boolean; result?: { id: string }; errors?: unknown };
  if (!j.success || !j.result?.id) throw new Error(`CF TXT create failed: ${JSON.stringify(j.errors)}`);
  return j.result.id;
}

async function cfDeleteRecord(id: string): Promise<void> {
  await fetch(`${CF_API}/zones/${CF_ZONE}/dns_records/${id}`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${CF_TOKEN}` },
  });
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

test("domain lifecycle: register → DNS TXT → verify (happy path) → custom-domain agent → teardown", { skip }, async () => {
  const domain = `${uniqueSlug("dl")}.${CF_ZONE_NAME}`;
  const dnsIds: string[] = [];
  let registered = false;
  let agentEmail = "";
  try {
    // 1. register the throwaway domain → returns the ownership TXT to publish.
    const reg = await client.post<{ dns_records: Array<{ type: string; name: string; value: string; purpose: string }> }>(
      "/v1/domains",
      { body: { domain } },
    );
    assert.equal(reg.status, 201, `register ${domain}: ${reg.raw.slice(0, 200)}`);
    registered = true;
    const txt = reg.body?.dns_records?.find((r) => r.purpose === "ownership" && r.type === "TXT");
    assert.ok(txt, "register returns an ownership TXT record");

    // 2. publish the verify TXT in the ISOLATED zone.
    dnsIds.push(await cfCreateTxt(txt!.name, txt!.value));

    // 3. verifyDomain HAPPY PATH — poll until DNS propagates and the server's
    //    live lookup finds the token (bounded ~90s; TTL 60 propagates in seconds).
    let verified = false;
    for (let i = 0; i < 30; i++) {
      const v = await client.post<{ verified: boolean }>(`/v1/domains/${domain}/verify`);
      if (v.status === 200 && v.body?.verified) {
        verified = true;
        info(SUITE, "verify", `domain verified after ~${i * 3}s of DNS propagation`);
        break;
      }
      await sleep(3000);
    }
    assert.ok(verified, "domain reached verified=true after the TXT was published");

    // 4. the domain now reads back verified, and a custom-domain agent can be
    //    created on it and is itself verified.
    const got = await client.get<{ verified: boolean }>(`/v1/domains/${domain}`);
    assert.equal(got.body?.verified, true, "GET domain reflects verified=true");

    agentEmail = `bot@${domain}`;
    const ag = await client.post<{ email: string; domain_verified: boolean }>("/v1/agents", {
      body: { email: agentEmail, name: "lifecycle bot" },
    });
    assert.equal(ag.status, 201, `create custom-domain agent: ${ag.raw.slice(0, 200)}`);
    assert.equal(ag.body?.domain_verified, true, "custom-domain agent inherits domain_verified=true");
  } finally {
    // 5. teardown — agent, domain, then the DNS record (a leaked record is a failure).
    if (agentEmail) await client.delete(`/v1/agents/${encodeURIComponent(agentEmail)}?confirm=DELETE`);
    if (registered) await client.delete(`/v1/domains/${domain}?confirm=DELETE`);
    for (const id of dnsIds) await cfDeleteRecord(id);
  }
});

after(async () => {
  await writeReport(`./reports/${SUITE}.json`);
});
