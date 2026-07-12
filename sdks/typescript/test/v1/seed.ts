/**
 * Staging API-driven seeding for the contract runner.
 *
 * The contract scenarios' store-dependent setup (verify_domain, inject_message)
 * assumes direct DB access. Against a REMOTE staging server there is no store, so
 * those scenarios normally skip. This module provides the same preconditions
 * over the public surface instead:
 *
 *   - verify_domain → mint a real <slug>.<zone> domain, publish its ownership TXT
 *     + inbound MX to an ISOLATED Cloudflare zone (never prod), wait for public
 *     propagation, then drive POST /v1/domains/{d}/verify. (Mirrors P2 suite 22.)
 *   - inject_message → deliver a real inbound email over the staging SMTP listener
 *     (raw SMTP, no auth/TLS — the plaintext inbound port).
 *
 * All opt-in via env; when unset, seedEnabled() is false and the runner keeps its
 * store-skip behavior (so local/CI Go-server runs are unaffected):
 *   CLOUDFLARE_API_TOKEN / CLOUDFLARE_ZONE_ID / CLOUDFLARE_ZONE_NAME  (verify)
 *   E2A_TEST_SMTP_HOST (default 127.0.0.1) / E2A_TEST_SMTP_PORT       (inject)
 */
import net from "node:net";
import { Resolver } from "node:dns/promises";

const CF_TOKEN = process.env.CLOUDFLARE_API_TOKEN;
const CF_ZONE = process.env.CLOUDFLARE_ZONE_ID;
const CF_ZONE_NAME = process.env.CLOUDFLARE_ZONE_NAME;
const SMTP_HOST = process.env.E2A_TEST_SMTP_HOST || "127.0.0.1";
const SMTP_PORT = Number(process.env.E2A_TEST_SMTP_PORT || "0");

const CF_API = "https://api.cloudflare.com/client/v4";
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Seeding is available only when the isolated CF zone AND the SMTP port are set. */
export function seedEnabled(): boolean {
  return Boolean(CF_TOKEN && CF_ZONE && CF_ZONE_NAME && SMTP_PORT > 0);
}

/**
 * afterAll safety-net: independent of per-test cleanup, delete any leftover seeded
 * domains (+ their agents) on the account and any CF records tagged with our seed
 * comment. So a test that times out mid-run (skipping its finally) still can't
 * permanently leak into the SHARED zone/account. Best-effort; never throws.
 */
export async function sweepLeaks(baseUrl: string, apiKey: string): Promise<void> {
  if (!seedEnabled()) return;
  const raw = (method: string, path: string) =>
    fetch(`${baseUrl}${path}`, { method, headers: { Authorization: `Bearer ${apiKey}` } });
  try {
    const domains =
      ((await (await raw("GET", "/v1/domains")).json()) as { items?: Array<{ domain: string }> }).items ?? [];
    const seeded = new Set(domains.map((d) => d.domain).filter((d) => d.endsWith(`.${CF_ZONE_NAME}`)));
    if (seeded.size) {
      const agents =
        ((await (await raw("GET", "/v1/agents")).json()) as { items?: Array<{ email: string }> }).items ?? [];
      for (const a of agents) {
        if (seeded.has(a.email.split("@")[1])) {
          await raw("DELETE", `/v1/agents/${encodeURIComponent(a.email)}?confirm=DELETE`).catch(() => {});
        }
      }
      for (const d of seeded) await raw("DELETE", `/v1/domains/${d}?confirm=DELETE`).catch(() => {});
    }
  } catch {
    /* best-effort */
  }
  try {
    const res = await fetch(`${CF_API}/zones/${CF_ZONE}/dns_records?per_page=100`, {
      headers: { Authorization: `Bearer ${CF_TOKEN}` },
    });
    const j = (await res.json()) as { result?: Array<{ id: string; comment?: string }> };
    for (const rec of j.result ?? []) {
      if (rec.comment?.includes("e2a contract-seed")) await cfDelete(rec.id);
    }
  } catch {
    /* best-effort */
  }
}

function slug(prefix: string): string {
  // Unique-enough per scenario; the runner also namespaces by scenario name.
  return `${prefix}-${Math.random().toString(36).slice(2, 8)}${Date.now().toString(36).slice(-4)}`;
}

async function cfCreate(rec: { type: string; name: string; content: string; priority?: number }): Promise<string> {
  const res = await fetch(`${CF_API}/zones/${CF_ZONE}/dns_records`, {
    method: "POST",
    headers: { Authorization: `Bearer ${CF_TOKEN}`, "Content-Type": "application/json" },
    body: JSON.stringify({ ...rec, ttl: 60, comment: "e2a contract-seed (temporary)" }),
  });
  const j = (await res.json()) as { success: boolean; result?: { id: string }; errors?: unknown };
  if (!j.success || !j.result?.id) throw new Error(`CF ${rec.type} create failed: ${JSON.stringify(j.errors)}`);
  return j.result.id;
}

async function cfDelete(id: string): Promise<void> {
  const res = await fetch(`${CF_API}/zones/${CF_ZONE}/dns_records/${id}`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${CF_TOKEN}` },
  }).catch(() => null);
  if (res && !res.ok) console.warn(`[seed] CF record ${id} delete failed HTTP ${res.status} — manual cleanup`);
}

// Wait until BOTH records resolve on Google Public DNS (the resolver family the
// GCP VM forwards to) before the first verify, so the server's live lookup can't
// negative-cache the miss for the zone SOA minimum. Generous budget: fresh-record
// propagation is variable (see P2).
async function waitForPublicDns(domain: string, txtValue: string, mxHost: string): Promise<boolean> {
  const r = new Resolver();
  r.setServers(["8.8.8.8"]);
  // Initial delay BEFORE the first query: if we query 8.8.8.8 the instant after
  // cfCreate — before the record propagates to CF's edge — 8.8.8.8 negative-caches
  // the miss (up to the zone SOA minimum), and that cached NXDOMAIN can outlast the
  // whole poll budget. Waiting for CF's edge first makes the first lookup positive.
  await sleep(12000);
  for (let i = 0; i < 60; i++) {
    let txtOk = false;
    let mxOk = false;
    try {
      txtOk = (await r.resolveTxt(domain)).some((c) => c.join("").includes(txtValue));
    } catch {
      /* propagating */
    }
    try {
      const wantMx = mxHost.replace(/\.$/, "").toLowerCase();
      mxOk = (await r.resolveMx(domain)).some((m) => m.exchange.replace(/\.$/, "").toLowerCase() === wantMx);
    } catch {
      /* propagating */
    }
    if (txtOk && mxOk) return true;
    await sleep(3000);
  }
  return false;
}

interface DnsRecord {
  type: string;
  name: string;
  value: string;
  purpose: string;
  priority?: number | null;
}

/**
 * Seeder mints + verifies real domains and injects real inbound mail for one
 * scenario, tracking everything it creates for teardown. One instance per Runner.
 * Uses its OWN non-throwing fetch (the runner's RawApi throws on 4xx; verify
 * always returns 200 with a {verified:false} body while records propagate).
 */
export class Seeder {
  private readonly domains: string[] = [];
  private readonly dnsIds: string[] = [];
  // Ownership TXT value + inbound MX host per registered domain, so verifyDomain()
  // (which may run later, e.g. a verify_and_retry step) can wait for propagation.
  private readonly pending: Record<string, { txt: string; mx: string }> = {};

  constructor(
    private readonly baseUrl: string,
    private readonly apiKey: string,
  ) {}

  private async raw(method: string, path: string, body?: unknown): Promise<Response> {
    const headers: Record<string, string> = { Authorization: `Bearer ${this.apiKey}` };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    return fetch(`${this.baseUrl}${path}`, {
      method,
      headers,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  }

  /** Mint <slug>.<zone>, register it, and publish TXT+MX — but do NOT verify yet
   *  (domain_verification_enforced needs an unverified-then-verified domain). */
  async registerDomain(prefix: string): Promise<string> {
    const domain = `${slug(prefix)}.${CF_ZONE_NAME}`;
    const reg = (await (await this.raw("POST", "/v1/domains", { domain })).json()) as { dns_records: DnsRecord[] };
    this.domains.push(domain);
    const txt = reg.dns_records.find((r) => r.purpose === "ownership" && r.type === "TXT");
    const mx = reg.dns_records.find((r) => r.purpose === "inbound_mx" && r.type === "MX");
    if (!txt || !mx) throw new Error(`register ${domain} missing TXT/MX records`);
    this.dnsIds.push(await cfCreate({ type: "TXT", name: txt.name, content: txt.value }));
    this.dnsIds.push(await cfCreate({ type: "MX", name: mx.name, content: mx.value, priority: mx.priority ?? 10 }));
    this.pending[domain] = { txt: txt.value, mx: mx.value };
    return domain;
  }

  /** Wait for the (already-published) records to propagate, then verify. */
  async verifyDomain(domain: string): Promise<void> {
    const p = this.pending[domain];
    if (!p) throw new Error(`seed: verifyDomain(${domain}) before registerDomain`);
    if (!(await waitForPublicDns(domain, p.txt, p.mx))) {
      throw new Error(`seed: DNS for ${domain} did not become public within budget`);
    }
    await sleep(5000); // margin so the VM's resolver PoP catches up before the first verify
    let verified = false;
    for (let i = 0; i < 20 && !verified; i++) {
      const v = (await (await this.raw("POST", `/v1/domains/${domain}/verify`)).json()) as { verified?: boolean };
      verified = v.verified === true;
      if (!verified) await sleep(3000);
    }
    if (!verified) throw new Error(`seed: ${domain} did not verify after propagation`);
  }

  /**
   * Deliver a real inbound email over the staging SMTP listener, then poll the
   * agent's inbox until it lands and return its message id.
   */
  async injectMessage(agentEmail: string, from: string, subject: string): Promise<string> {
    await smtpInject({ from, to: agentEmail, subject, body: "contract-seed injected inbound body" });
    for (let i = 0; i < 15; i++) {
      const page = (await (await this.raw("GET", `/v1/agents/${encodeURIComponent(agentEmail)}/messages?limit=20`)).json()) as {
        items: Array<{ id: string; subject: string }>;
      };
      const hit = page.items.find((m) => m.subject === subject);
      if (hit) return hit.id;
      await sleep(2000);
    }
    throw new Error(`seed: injected message (subject "${subject}") never appeared in ${agentEmail}`);
  }

  /** Delete every agent, domain, and CF record this seeder created. Agents MUST go
   *  first: domain delete returns 400 domain_has_agents while any agent remains, so
   *  skipping them silently leaks the domain (accumulates in the shared account). */
  async cleanup(): Promise<void> {
    if (this.domains.length) {
      const mine = new Set(this.domains);
      try {
        const page = (await (await this.raw("GET", "/v1/agents?limit=200")).json()) as {
          items?: Array<{ email: string }>;
        };
        for (const a of page.items ?? []) {
          if (mine.has(a.email.split("@")[1])) {
            await this.raw("DELETE", `/v1/agents/${encodeURIComponent(a.email)}?confirm=DELETE`).catch(() => {});
          }
        }
      } catch {
        /* best-effort: fall through to domain/record deletes */
      }
    }
    for (const d of this.domains) {
      await this.raw("DELETE", `/v1/domains/${d}?confirm=DELETE`).catch(() => {});
    }
    for (const id of this.dnsIds) await cfDelete(id);
  }
}

/**
 * Minimal plaintext SMTP client — enough to hand a message to the inbound
 * listener (no AUTH, no STARTTLS; the inbound port is plaintext). Lock-steps
 * through the exchange, sending the next command after each positive final reply.
 *
 * Resolves as soon as the message is ACCEPTED (the 250 after the DATA payload) —
 * QUIT is fire-and-forget, so a server that closes without a 221 doesn't cause a
 * spurious timeout. Multiline replies (`NNN-…` continuation lines) are ignored;
 * final lines are `NNN ` (space). Tolerates bare-LF as well as CRLF framing.
 * Only ever fails on a >=400 reply, an error, or a timeout — never false-positive.
 */
export function smtpInject(m: { from: string; to: string; subject: string; body: string }): Promise<void> {
  return new Promise((resolve, reject) => {
    const sock = net.createConnection({ host: SMTP_HOST, port: SMTP_PORT });
    sock.setEncoding("utf8");
    let done = false;
    const fail = (e: Error) => {
      if (done) return;
      done = true;
      sock.destroy();
      reject(e);
    };
    sock.setTimeout(20000, () => fail(new Error("smtp: timeout")));
    sock.on("error", fail);

    const payload =
      `From: ${m.from}\r\nTo: ${m.to}\r\nSubject: ${m.subject}\r\n` +
      `Message-ID: <${slug("seed")}@conformance.local>\r\n\r\n${m.body}\r\n.`;
    // The reply to the LAST step (the payload) is the queue-ack → accepted.
    const steps = [`EHLO conformance.local`, `MAIL FROM:<${m.from}>`, `RCPT TO:<${m.to}>`, `DATA`, payload];

    let i = 0;
    let buf = "";
    sock.on("data", (chunk: string) => {
      if (done) return;
      buf += chunk;
      let nl: number;
      while ((nl = buf.search(/\r?\n/)) !== -1) {
        const line = buf.slice(0, nl);
        buf = buf.slice(buf[nl] === "\r" ? nl + 2 : nl + 1);
        if (!/^\d{3} /.test(line)) continue; // multiline continuation line
        const code = Number(line.slice(0, 3));
        if (code >= 400) return fail(new Error(`smtp: ${line}`));
        if (i < steps.length) {
          sock.write(steps[i++] + "\r\n"); // reply to greeting/prev step → send next
        } else {
          // Positive reply to the payload = message queued. Done.
          done = true;
          try {
            sock.write("QUIT\r\n"); // best-effort; we don't wait for the 221
            sock.end();
          } catch {
            /* socket may already be closing */
          }
          resolve();
          return;
        }
      }
    });
  });
}
