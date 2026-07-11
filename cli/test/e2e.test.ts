/**
 * Live binary-spawn parity harness for the CLI against a RUNNING server (staging).
 *
 * Spawns the ACTUAL built binary (dist/bin/e2a.js) and asserts on its --json
 * output and its frozen exit codes (src/exit.ts) — so a green run attests the
 * shipped CLI works end-to-end against a live deployment. The CLI is a deliberate
 * SUBSET of the API (no `domains`, no `agents delete`), so this exercises the real
 * parity surface only.
 *
 * Gated on staging creds; skips cleanly when absent (kept OUT of the default
 * `vitest run`, whose include is src/**). Run:
 *   npm run build && \
 *   E2A_URL=… E2A_API_KEY=… E2A_SHARED_DOMAIN=… npm run test:e2e --workspace @e2a/cli
 *
 * Env (note: the CLI reads E2A_URL, NOT E2A_API_URL):
 *   E2A_URL             staging base URL (or a local tunnel)
 *   E2A_API_KEY         an account-scoped key for the target account
 *   E2A_SHARED_DOMAIN   shared domain for throwaway agents (e.g. agents-staging.e2a.dev)
 */
import { describe, it, expect, afterAll } from "vitest";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const CLI = fileURLToPath(new URL("../dist/bin/e2a.js", import.meta.url));

const URL_ = process.env.E2A_URL || "";
const KEY = process.env.E2A_API_KEY || "";
const DOMAIN = process.env.E2A_SHARED_DOMAIN || (process.env.E2A_AGENT_EMAIL || "").split("@")[1] || "";
const live = Boolean(URL_ && KEY && DOMAIN);

interface Run {
  code: number;
  stdout: string;
  stderr: string;
}

// run spawns the built CLI with the staging env. HOME is isolated so a real
// ~/.e2a/config can't influence the run (env still wins over file regardless).
function run(args: string[], extra: Record<string, string> = {}): Run {
  const env: Record<string, string | undefined> = {
    ...process.env,
    E2A_URL: URL_,
    E2A_API_KEY: KEY,
    HOME: "/tmp/e2a-cli-e2e-home",
    ...extra,
  };
  // CRITICAL: the CLI entrypoint skips main() when VITEST_WORKER_ID is set (its
  // in-process import guard). We spawn the REAL binary, so those vitest markers
  // must not leak into the child or every command no-ops with exit 0 / no output.
  delete env.VITEST_WORKER_ID;
  delete env.VITEST;
  delete env.VITEST_POOL_ID;
  const r = spawnSync("node", [CLI, ...args], { encoding: "utf8", env });
  return { code: r.status ?? -1, stdout: r.stdout ?? "", stderr: r.stderr ?? "" };
}

const sleep = (ms: number) => new Promise((res) => setTimeout(res, ms));

// The CLI can't delete agents; clean up created inboxes over the API.
async function apiDeleteAgent(email: string): Promise<void> {
  await fetch(`${URL_}/v1/agents/${encodeURIComponent(email)}?confirm=DELETE`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${KEY}` },
  }).catch(() => {});
}

const createdAgents: string[] = [];
afterAll(async () => {
  for (const a of createdAgents) await apiDeleteAgent(a);
});

describe.skipIf(!live)("cli live parity", () => {
  it("whoami --json → identity (exit 0)", () => {
    const r = run(["whoami", "--json"]);
    expect(r.code, r.stderr).toBe(0);
    const j = JSON.parse(r.stdout);
    expect(j.user?.email).toBeTruthy();
    expect(j.scope).toBe("account");
  });

  it("agents create → get → list, then send → messages list (self loopback)", async () => {
    const bot = `cli-live-${Date.now().toString(36)}@${DOMAIN}`;

    const created = run(["agents", "create", bot, "--name", "cli live e2e", "--json"]);
    expect(created.code, created.stderr).toBe(0);
    createdAgents.push(bot);
    expect(JSON.parse(created.stdout).email).toBe(bot);

    const got = run(["agents", "get", bot, "--json"]);
    expect(got.code, got.stderr).toBe(0);
    expect(JSON.parse(got.stdout).email).toBe(bot);

    const list = run(["agents", "list", "--json"]);
    expect(list.code, list.stderr).toBe(0);
    expect(list.stdout).toContain(bot);

    // Send self→self on the fresh (unprotected) inbox: delivers + loops back.
    const subject = `cli-live ${Date.now()}`;
    const sent = run(["send", "--agent", bot, "--to", bot, "--subject", subject, "--body", "hi from cli e2e", "--json"]);
    expect(sent.code, sent.stderr).toBe(0); // 3 would mean HELD; a fresh inbox is unprotected
    expect(JSON.parse(sent.stdout).messageId).toBeTruthy();

    // Poll messages list until the loopback lands (NDJSON, one row per line).
    let rows: string[] = [];
    for (let i = 0; i < 12 && rows.length === 0; i++) {
      const ml = run(["messages", "list", "--agent", bot, "--limit", "20", "--json"]);
      expect(ml.code, ml.stderr).toBe(0);
      rows = ml.stdout.split("\n").filter((l) => l.trim().length > 0);
      if (rows.length === 0) await sleep(1500);
    }
    expect(rows.length, "the loopback message must appear in `messages list`").toBeGreaterThan(0);
    // Correlate to OUR send: fetch the row's message and check the subject matches
    // (a fresh inbox only holds the loopback, but this proves it's genuinely ours).
    const firstId = JSON.parse(rows[0]).id ?? JSON.parse(rows[0]).messageId;
    expect(firstId).toBeTruthy();
    const gotMsg = run(["messages", "get", firstId, "--agent", bot, "--json"]);
    expect(gotMsg.code, gotMsg.stderr).toBe(0);
    expect(JSON.parse(gotMsg.stdout).subject).toBe(subject);
  }, 40_000);

  it("keys create → list → delete (exit 0 each)", () => {
    const created = run(["keys", "create", "--name", "cli-live-key", "--json"]);
    expect(created.code, created.stderr).toBe(0);
    const key = JSON.parse(created.stdout);
    const keyId = key.id ?? key.keyId;
    expect(keyId).toBeTruthy();
    try {
      const list = run(["keys", "list", "--json"]);
      expect(list.code, list.stderr).toBe(0);
      expect(list.stdout).toContain(keyId);

      const del = run(["keys", "delete", keyId]);
      expect(del.code, del.stderr).toBe(0);
    } finally {
      // Guarantee the key never lingers on staging even if an assertion threw.
      run(["keys", "delete", keyId]);
    }
  });

  it("honors the frozen exit-code contract (usage=2, auth=4)", () => {
    // Unknown command → usage error (2).
    expect(run(["domains", "list"]).code).toBe(2); // CLI has no `domains` command
    // Bad key → auth error (4).
    expect(run(["whoami", "--json"], { E2A_API_KEY: "e2a_bogus_key_definitely_invalid" }).code).toBe(4);
  });
});
