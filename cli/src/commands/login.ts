import { execFile } from "node:child_process";
import { randomBytes } from "node:crypto";
import { createServer, type IncomingMessage, type Server } from "node:http";
import { hostname } from "node:os";
import {
  E2AClient,
  E2ANotFoundError,
  E2APermissionError,
  type CreateAPIKeyRequest,
} from "@e2a/sdk/v1";
import { loadConfig, saveConfig, type Config } from "../config.js";
import { EXIT, fail } from "../exit.js";

/**
 * Render the URL's host for human-facing log lines. Falls back to the raw
 * input if it isn't a valid URL — better than throwing in the middle of
 * a status message.
 */
function hostLabel(apiUrl: string): string {
  try {
    return new URL(apiUrl).host;
  } catch {
    return apiUrl;
  }
}

const LOGIN_TIMEOUT_MS = 2 * 60 * 1000;

function openBrowser(url: string): void {
  if (process.platform === "win32") {
    const child = execFile("cmd", ["/c", "start", "", url], (err) => {
      if (err) {
        process.stderr.write(`Could not open browser. Visit manually: ${url}\n`);
      }
    });
    child.unref();
    return;
  }

  const cmd = process.platform === "darwin" ? "open" : "xdg-open";
  const child = execFile(cmd, [url], (err) => {
    if (err) {
      process.stderr.write(`Could not open browser. Visit manually: ${url}\n`);
    }
  });
  child.unref();
}

function renderCallbackPage(title: string, message: string): string {
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${title}</title>
  <style>
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f5f7fb;
      color: #111827;
      display: grid;
      place-items: center;
      min-height: 100vh;
      padding: 24px;
    }
    main {
      width: 100%;
      max-width: 460px;
      background: white;
      border: 1px solid #e5e7eb;
      border-radius: 16px;
      padding: 28px;
      box-shadow: 0 18px 50px rgba(15, 23, 42, 0.08);
    }
    h1 { margin: 0 0 10px; font-size: 24px; }
    p { margin: 0; color: #4b5563; line-height: 1.5; }
  </style>
</head>
<body>
  <main>
    <h1>${title}</h1>
    <p>${message}</p>
  </main>
</body>
</html>`;
}

function readRequestBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

function closeServer(server: Server): Promise<void> {
  return new Promise((resolve) => {
    if (!server.listening) {
      resolve();
      return;
    }
    server.close(() => resolve());
  });
}

function buildBrowserLoginURL(apiUrl: string, callbackUrl: string, cliState: string): string {
  const loginUrl = new URL("/api/auth/login", apiUrl);
  loginUrl.searchParams.set("cli_callback", callbackUrl);
  loginUrl.searchParams.set("cli_state", cliState);
  return loginUrl.toString();
}

type BrowserLoginResult = {
  apiKey: string;
  agentEmail: string;
};

async function waitForBrowserLogin(apiUrl: string, timeoutMs = LOGIN_TIMEOUT_MS): Promise<BrowserLoginResult> {
  const cliState = randomBytes(16).toString("hex");

  let settled = false;
  let server: Server | null = null;
  // Single timeout handle that's cleared in finish() and the outer
  // finally — earlier versions had two setTimeouts and one of them
  // wasn't cleared on success, keeping the Node event loop alive for
  // the full timeout (2 min) after a successful login. Symptom was
  // the terminal hanging after "Logged in to <host>" printed.
  let timeoutHandle: ReturnType<typeof setTimeout> | undefined;

  try {
    return await new Promise<BrowserLoginResult>((resolve, reject) => {
      const finish = async (value: BrowserLoginResult | Error, isError: boolean) => {
        if (settled) return;
        settled = true;
        if (timeoutHandle) clearTimeout(timeoutHandle);
        if (server) {
          await closeServer(server);
        }
        if (isError) reject(value);
        else resolve(value as BrowserLoginResult);
      };

      timeoutHandle = setTimeout(
        () => void finish(new Error("timed out waiting for browser login"), true),
        timeoutMs,
      );

      server = createServer(async (req, res) => {
        res.setHeader("Connection", "close");
        try {
          if (req.method !== "POST") {
            res.statusCode = 405;
            res.setHeader("Content-Type", "text/html; charset=utf-8");
            res.end(renderCallbackPage("Return to your terminal", "Run e2a login again to finish connecting the CLI."));
            return;
          }

          const body = await readRequestBody(req);
          const params = new URLSearchParams(body);
          const returnedState = params.get("cli_state") || "";
          const apiKey = params.get("api_key") || "";
          const agentEmail = params.get("agent_email") || "";
          const error = params.get("error") || "";

          if (returnedState != cliState) {
            res.statusCode = 400;
            res.setHeader("Content-Type", "text/html; charset=utf-8");
            res.end(renderCallbackPage("e2a login failed", "The browser login state did not match this terminal session. Return to the terminal and try again."));
            await finish(new Error("browser login state mismatch"), true);
            return;
          }

          if (error) {
            res.statusCode = 400;
            res.setHeader("Content-Type", "text/html; charset=utf-8");
            res.end(renderCallbackPage("e2a login failed", error));
            await finish(new Error(error), true);
            return;
          }

          if (!apiKey.startsWith("e2a_")) {
            res.statusCode = 400;
            res.setHeader("Content-Type", "text/html; charset=utf-8");
            res.end(renderCallbackPage("e2a login failed", "The browser did not return a valid API key."));
            await finish(new Error("browser login did not return a valid api key"), true);
            return;
          }

          res.statusCode = 200;
          res.setHeader("Content-Type", "text/html; charset=utf-8");
          res.end(renderCallbackPage("e2a CLI connected", "You can return to the terminal. Your config has been updated automatically."));

          await finish({ apiKey, agentEmail }, false);
        } catch (error) {
          res.statusCode = 500;
          res.setHeader("Content-Type", "text/html; charset=utf-8");
          res.end(renderCallbackPage("e2a login failed", "The CLI could not process the browser callback."));
          await finish(error instanceof Error ? error : new Error("failed to process browser callback"), true);
        }
      });

      server.on("error", async (error) => {
        await finish(error instanceof Error ? error : new Error("browser login server failed"), true);
      });

      server.listen(0, "127.0.0.1", () => {
        const address = server?.address();
        if (!address || typeof address === "string") {
          void finish(new Error("failed to start local login callback"), true);
          return;
        }

        const callbackUrl = `http://127.0.0.1:${address.port}/callback`;
        const loginUrl = buildBrowserLoginURL(apiUrl, callbackUrl, cliState);

        // Always print the URL — `open`/`xdg-open` can return success even
        // when no browser actually launches (headless box, SSH session,
        // container without a desktop), and without the URL printed the
        // user has no way to recover. Frame it as a fallback so the happy
        // path still reads cleanly.
        process.stderr.write("\n");
        process.stderr.write(`Opening ${hostLabel(apiUrl)} in your browser to finish login...\n`);
        process.stderr.write(`If it doesn't open, visit:\n  ${loginUrl}\n`);
        process.stderr.write("\n");

        openBrowser(loginUrl);
      });
    });
  } finally {
    // Defensive: finish() should already clear these on every code path,
    // but if somebody throws before we register them or after settled is
    // set, the finally block guarantees no orphan timer / open socket
    // keeps the process alive.
    if (timeoutHandle) clearTimeout(timeoutHandle);
    if (server) {
      await closeServer(server);
    }
  }
}

export interface LoginOptions {
  /**
   * Mint a least-privilege agent-scoped key bound to this inbox (bare slugs
   * expand on the deployment's shared domain), then revoke the account-scoped
   * bootstrap key from the browser handoff. The machine ends up holding only
   * a single-inbox credential — the setup harnesses like tether want.
   */
  agent?: string;
  /** Headless mode: validate a pasted/piped/env key instead of the browser. */
  withKey?: boolean;
  /** Explicit key value for --with-key (else $E2A_API_KEY, else stdin). */
  key?: string;
}

export async function login(opts: LoginOptions = {}): Promise<void> {
  const config = loadConfig();

  // The combination reads as "headless agent-scoped login" but the exchange
  // needs an account credential we'd have to trust unvalidated — refuse
  // loudly rather than silently ignoring --agent (the old behavior saved the
  // pasted key at whatever scope it had, voiding the least-privilege promise).
  if (opts.withKey && opts.agent) {
    fail(
      EXIT.USAGE,
      "--with-key and --agent cannot be combined. Headless agent-scoped setup: " +
        "e2a login --with-key <account-key>, then e2a keys create --agent <inbox>.",
    );
  }

  if (opts.withKey) {
    await loginWithKey(config, opts.key);
    return;
  }

  // Preflight: hit the deployment's public info endpoint before opening
  // the browser. Two wins —
  //   1. Fast-fail when the server is unreachable instead of opening a
  //      browser tab that loads nothing and timing out 2 minutes later.
  //   2. Captures shared_domain in the same request so we don't need a
  //      separate fetch after login completes.
  // `/v1/info` is unauthenticated and login runs before we have a key, so we
  // probe it with a raw fetch rather than the SDK client (which requires an
  // apiKey to construct). Distinguish a connection failure (refused, DNS,
  // timeout) — which should abort — from an HTTP-level error (server up, /info
  // absent on an older deployment), which should let login continue.
  let discoveredSharedDomain: string | undefined;
  try {
    const resp = await fetch(`${config.api_url.replace(/\/+$/, "")}/v1/info`);
    if (resp.ok) {
      const info = (await resp.json()) as { shared_domain?: string };
      if (info.shared_domain) {
        discoveredSharedDomain = info.shared_domain;
      }
    }
    // A non-ok response means the server is up but /info is unavailable
    // (older deployment) — continue with login without shared_domain.
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err);
    throw new Error(
      `could not reach ${config.api_url}: ${detail}. ` +
        `Check the server is running and E2A_URL is correct.`,
    );
  }

  const { apiKey, agentEmail } = await waitForBrowserLogin(config.api_url);

  if (opts.agent) {
    await exchangeForAgentKey(config, apiKey, opts.agent, discoveredSharedDomain);
    return;
  }

  saveConfig({
    api_key: apiKey,
    agent_email: agentEmail,
    key_scope: "account",
    ...(discoveredSharedDomain ? { shared_domain: discoveredSharedDomain } : {}),
  });

  process.stdout.write(`Logged in to ${hostLabel(config.api_url)}.\n`);
  if (discoveredSharedDomain) {
    process.stdout.write(`  Shared domain: ${discoveredSharedDomain}\n`);
  }
  process.stdout.write("Config saved to ~/.e2a/config.json\n");
  if (agentEmail) {
    process.stdout.write(`Active agent: ${agentEmail}\n`);
  } else {
    process.stderr.write(
      `No agents found yet. Run: e2a agents create <name>@<shared-domain> — or visit ${config.api_url.replace(/\/+$/, "")}/get-started\n`,
    );
  }
}

/**
 * Read the key for --with-key: explicit value → piped stdin → $E2A_API_KEY.
 * Stdin outranks the env on purpose: `echo $NEW_KEY | e2a login --with-key`
 * is a key ROTATION, and the box doing it very likely still has the old key
 * exported — env-first would silently "rotate" to the stale key.
 */
async function resolveWithKeyInput(explicit?: string): Promise<string> {
  if (explicit) return explicit;
  if (!process.stdin.isTTY) {
    let data = "";
    for await (const chunk of process.stdin) data += chunk;
    const key = data.trim();
    if (key) return key;
  }
  if (process.env.E2A_API_KEY) return process.env.E2A_API_KEY;
  return fail(
    EXIT.USAGE,
    "usage: e2a login --with-key <key>  (or pipe the key on stdin / set E2A_API_KEY)",
  );
}

/**
 * Headless login: no browser, no local callback server — validate the key
 * against GET /v1/account (which also reveals its scope and bound inbox) and
 * persist it. This is the path for CI boxes and SSH sessions, where the
 * browser flow's 127.0.0.1 callback can never complete.
 */
export async function loginWithKey(config: Config, explicitKey?: string): Promise<void> {
  const key = await resolveWithKeyInput(explicitKey);
  const client = new E2AClient({ apiKey: key, baseUrl: config.api_url });
  const account = await client.account.get(); // throws E2AAuthError → exit 4

  saveConfig({
    api_key: key,
    key_scope: account.scope,
    ...(account.agentAddress ? { agent_email: account.agentAddress } : {}),
  });

  process.stdout.write(`Logged in to ${hostLabel(config.api_url)} with a ${account.scope}-scoped key.\n`);
  if (account.agentAddress) {
    process.stdout.write(`Bound agent: ${account.agentAddress}\n`);
  }
  process.stdout.write("Config saved to ~/.e2a/config.json\n");
}

/**
 * `login --agent <inbox>`: exchange the account-scoped bootstrap key from the
 * browser handoff for a least-privilege agent-scoped key, then revoke the
 * bootstrap so this machine holds only the single-inbox credential.
 */
async function exchangeForAgentKey(
  config: Config,
  bootstrapKey: string,
  inbox: string,
  sharedDomain?: string,
): Promise<void> {
  const client = new E2AClient({ apiKey: bootstrapKey, baseUrl: config.api_url });

  // Bare slug → expand on the deployment's shared domain.
  const domain = sharedDomain || config.shared_domain;
  const agentEmail = inbox.includes("@") ? inbox : `${inbox}@${domain}`;

  // Verify the inbox before minting: a typo must fail with the owned list,
  // not mint a key against nothing. Only a definitive "doesn't exist / not
  // yours" (404/403) takes this path — a transient error must NOT masquerade
  // as permanent exit 5 (retry wrappers would give up on a server blip).
  try {
    await client.agents.get(agentEmail);
  } catch (err) {
    if (!(err instanceof E2ANotFoundError || err instanceof E2APermissionError)) throw err;
    process.stderr.write(`Agent not found or not owned: ${agentEmail}\nOwned agents:\n`);
    try {
      for await (const a of client.agents.list()) {
        process.stderr.write(`  ${a.email}\n`);
      }
    } catch {
      process.stderr.write("  (could not list agents)\n");
    }
    process.exit(EXIT.REQUEST);
  }

  const created = await client.account.apiKeys.create({
    name: `cli @${hostname()}`,
    // Cast: the generated model types scope as a TS enum; "agent" is its wire value.
    scope: "agent" as CreateAPIKeyRequest["scope"],
    agent: agentEmail,
  });

  // Persist the new key BEFORE revoking the bootstrap: the agent key's
  // plaintext exists only in this process, so if the config write fails
  // (read-only $HOME, ENOSPC) after a revoke, the machine would be left with
  // ZERO working credentials. Save-then-revoke makes every failure mode
  // recoverable — worst case an extra "CLI login" key survives server-side.
  saveConfig({
    api_key: created.key,
    agent_email: agentEmail,
    key_scope: "agent",
    ...(sharedDomain ? { shared_domain: sharedDomain } : {}),
  });

  // Revoke the bootstrap key. The handoff only gave us its plaintext, so
  // match its visible prefix against the key inventory; refuse to guess if
  // the match isn't unique — deleting the wrong credential is worse than
  // leaving one extra "CLI login" key for the dashboard to clean up.
  let revoked = false;
  try {
    const candidates: string[] = [];
    for await (const k of client.account.apiKeys.list()) {
      const prefix = k.keyPrefix.replace(/[^A-Za-z0-9_]+$/g, "");
      if (k.id !== created.id && prefix && bootstrapKey.startsWith(prefix)) {
        candidates.push(k.id);
      }
    }
    if (candidates.length === 1) {
      // Still holding the account-scoped credential here (the agent key
      // can't manage keys) — this must stay before the client is dropped.
      await client.account.apiKeys.delete(candidates[0]);
      revoked = true;
    }
  } catch {
    // fall through to the warning below
  }

  process.stdout.write(`Logged in to ${hostLabel(config.api_url)}.\n`);
  process.stdout.write(`Agent-scoped key minted for ${agentEmail} (key "cli @${hostname()}").\n`);
  if (revoked) {
    process.stdout.write("Bootstrap account key revoked — this machine holds only the agent key.\n");
  } else {
    process.stderr.write(
      'Could not uniquely identify the bootstrap key to revoke it — remove the "CLI login" key at your dashboard\'s API-keys page.\n',
    );
  }
  process.stdout.write("Config saved to ~/.e2a/config.json\n");
}
