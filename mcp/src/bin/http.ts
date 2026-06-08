#!/usr/bin/env node
import { startHttpServer } from "../http-server.js";

interface BinConfig {
  port: number;
  baseUrl: string;
  allowedHosts: string[];
  sessionIdleMs: number;
  maxSessions: number;
  publicUrl?: string;
  authorizationServerUrl?: string;
  trustProxy: boolean | number | string;
}

class ConfigError extends Error {}

function parsePositiveInt(name: string, raw: string, def: number): number {
  if (raw === "") return def;
  const n = Number(raw);
  if (!Number.isFinite(n) || !Number.isInteger(n) || n <= 0) {
    throw new ConfigError(`${name} must be a positive integer; got ${JSON.stringify(raw)}`);
  }
  return n;
}

function parsePort(name: string, raw: string, def: number): number {
  if (raw === "") return def;
  const n = Number(raw);
  // Port 0 is valid (OS-assigned), but reject NaN / negatives / >65535.
  if (!Number.isFinite(n) || !Number.isInteger(n) || n < 0 || n > 65535) {
    throw new ConfigError(`${name} must be 0..65535; got ${JSON.stringify(raw)}`);
  }
  return n;
}

// parseTrustProxy maps E2A_TRUST_PROXY to the value Express's `trust
// proxy` setting expects. Default "loopback": only a same-host proxy (the
// prod Caddy front, forwarding over localhost) is trusted for
// X-Forwarded-* headers. "true"/"false" become booleans; a bare integer
// is a hop count (Express reads a numeric *string* as a subnet, so it must
// be converted); anything else passes through as a preset name
// ("loopback"/"uniquelocal"/...) or a CSV of IPs/subnets.
function parseTrustProxy(raw: string): boolean | number | string {
  if (raw === "") return "loopback";
  if (raw === "true") return true;
  if (raw === "false") return false;
  if (/^\d+$/.test(raw)) return Number(raw);
  return raw;
}

function parseHostList(raw: string, def: string): string[] {
  const source = raw === "" ? def : raw;
  const list = source.split(",").map((s) => s.trim()).filter(Boolean);
  if (list.length === 0) {
    throw new ConfigError(`MCP_ALLOWED_HOSTS resolved to an empty list (raw=${JSON.stringify(raw)})`);
  }
  return list;
}

// resolveBaseUrl picks the deployment URL the HTTP MCP server talks
// to. Canonical is E2A_URL (matches the CLI + SDK docs); E2A_BASE_URL
// is the legacy name this binary shipped with — still accepted so
// existing deployment manifests keep working, with a one-shot stderr
// deprecation note.
function resolveBaseUrl(env: NodeJS.ProcessEnv): string {
  const canonical = env.E2A_URL;
  const legacy = env.E2A_BASE_URL;
  if (canonical) return canonical;
  if (legacy) {
    if (!resolveBaseUrl.warned) {
      process.stderr.write(
        "[e2a-mcp-http] E2A_BASE_URL is deprecated; rename it to E2A_URL (both names work today).\n",
      );
      resolveBaseUrl.warned = true;
    }
    return legacy;
  }
  return "https://e2a.dev";
}
resolveBaseUrl.warned = false;

export function loadConfig(env: NodeJS.ProcessEnv = process.env): BinConfig {
  return {
    port: parsePort("PORT", env.PORT ?? "", 3000),
    baseUrl: resolveBaseUrl(env),
    allowedHosts: parseHostList(env.MCP_ALLOWED_HOSTS ?? "", "mcp.e2a.dev"),
    sessionIdleMs: parsePositiveInt("MCP_SESSION_IDLE_MS", env.MCP_SESSION_IDLE_MS ?? "", 5 * 60_000),
    maxSessions: parsePositiveInt("MCP_MAX_SESSIONS", env.MCP_MAX_SESSIONS ?? "", 500),
    publicUrl: env.MCP_PUBLIC_URL || undefined,
    authorizationServerUrl: env.MCP_AUTHORIZATION_SERVER_URL || undefined,
    trustProxy: parseTrustProxy(env.E2A_TRUST_PROXY ?? ""),
  };
}

export { ConfigError };

async function main(): Promise<void> {
  let cfg: BinConfig;
  try {
    cfg = loadConfig();
  } catch (err) {
    if (err instanceof ConfigError) {
      process.stderr.write(`e2a-mcp-http config error: ${err.message}\n`);
      process.exit(2);
    }
    throw err;
  }

  const { close, port: bound } = await startHttpServer(cfg.port, {
    baseUrl: cfg.baseUrl,
    allowedHosts: cfg.allowedHosts,
    sessionIdleMs: cfg.sessionIdleMs,
    maxSessions: cfg.maxSessions,
    publicUrl: cfg.publicUrl,
    authorizationServerUrl: cfg.authorizationServerUrl,
    trustProxy: cfg.trustProxy,
  });
  process.stderr.write(
    `e2a-mcp-http listening on :${bound} (base ${cfg.baseUrl}, hosts ${cfg.allowedHosts.join(",")})\n`,
  );

  // Graceful shutdown: stop accepting new connections, drain active
  // sessions, then exit. Hard ceiling at 30s to avoid hanging deploys.
  let closing = false;
  const shutdown = async (signal: NodeJS.Signals) => {
    if (closing) return;
    closing = true;
    process.stderr.write(`received ${signal}, draining...\n`);
    const drainTimeout = setTimeout(() => {
      process.stderr.write("drain timeout, forcing exit\n");
      process.exit(1);
    }, 30_000);
    drainTimeout.unref();
    try {
      await close();
      clearTimeout(drainTimeout);
      process.exit(0);
    } catch (err) {
      clearTimeout(drainTimeout);
      const message = err instanceof Error ? err.message : String(err);
      process.stderr.write(`shutdown error: ${message}\n`);
      process.exit(1);
    }
  };
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));
}

// Only run main() when invoked as the entry point — keeps `loadConfig`
// importable from tests without spinning up the server.
const isMain = import.meta.url === `file://${process.argv[1]}`;
if (isMain) {
  main().catch((err) => {
    const message = err instanceof Error ? err.stack ?? err.message : String(err);
    process.stderr.write(`e2a-mcp-http fatal: ${message}\n`);
    process.exit(1);
  });
}
