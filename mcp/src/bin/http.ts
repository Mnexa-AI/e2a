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
}

class ConfigError extends Error {}

type LogSeverity = "INFO" | "WARNING" | "ERROR";

// logJson emits one operational event as a single-line JSON object on
// stderr. GCE Cloud Logging parses a single-line JSON payload into
// structured `jsonPayload` fields and honors two special keys: `severity`
// (sets the entry's log level) and `message` (the human-readable summary
// shown in the console). Keeping each event on one line also means
// multi-line content like a stack trace in `error` stays a single log
// entry instead of being split into several fragmented ones. The result
// is both human-skimmable (via `message`) and queryable (filter on
// `severity`, `event`, or any structured field).
function logJson(
  severity: LogSeverity,
  event: string,
  message: string,
  fields: Record<string, unknown> = {},
): void {
  process.stderr.write(`${JSON.stringify({ severity, event, message, ...fields })}\n`);
}

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

function parseHostList(raw: string, def: string): string[] {
  const source = raw === "" ? def : raw;
  const list = source.split(",").map((s) => s.trim()).filter(Boolean);
  if (list.length === 0) {
    throw new ConfigError(`MCP_ALLOWED_HOSTS resolved to an empty list (raw=${JSON.stringify(raw)})`);
  }
  return list;
}

// resolveBaseUrl picks the API host this server talks to. Canonical is
// E2A_API_URL — the same concept the backend names with E2A_API_URL (its
// externally visible API base) and the SDKs read. This server is a pure API
// client, so it wants the API host, NOT the deployment root that the CLI's
// E2A_URL points at (that one also serves the dashboard).
//
// E2A_URL and E2A_BASE_URL are both legacy names this binary has shipped with;
// still accepted so existing deployment manifests keep working, with a stderr
// deprecation note. main() calls loadConfig exactly once, so the note is emitted
// once per process without needing a module-level guard to dedupe it.
function resolveBaseUrl(env: NodeJS.ProcessEnv): string {
  const canonical = env.E2A_API_URL;
  if (canonical) return canonical;
  for (const legacy of ["E2A_URL", "E2A_BASE_URL"] as const) {
    const v = env[legacy];
    if (!v) continue;
    logJson(
      "WARNING",
      "e2a_api_url_legacy_name",
      `${legacy} is deprecated; rename it to E2A_API_URL (the old names still work today).`,
    );
    return v;
  }
  return "https://api.e2a.dev";
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): BinConfig {
  return {
    port: parsePort("PORT", env.PORT ?? "", 3000),
    baseUrl: resolveBaseUrl(env),
    allowedHosts: parseHostList(env.MCP_ALLOWED_HOSTS ?? "", "api.e2a.dev"),
    sessionIdleMs: parsePositiveInt("MCP_SESSION_IDLE_MS", env.MCP_SESSION_IDLE_MS ?? "", 5 * 60_000),
    maxSessions: parsePositiveInt("MCP_MAX_SESSIONS", env.MCP_MAX_SESSIONS ?? "", 500),
    publicUrl: env.MCP_PUBLIC_URL || undefined,
    authorizationServerUrl: env.MCP_AUTHORIZATION_SERVER_URL || undefined,
  };
}

export { ConfigError, logJson };

async function main(): Promise<void> {
  let cfg: BinConfig;
  try {
    cfg = loadConfig();
  } catch (err) {
    if (err instanceof ConfigError) {
      logJson("ERROR", "config_error", `config error: ${err.message}`, { error: err.message });
      process.exit(2);
    }
    throw err;
  }

  const { close, port: bound } = await startHttpServer(cfg.port, {
    baseUrl: cfg.baseUrl,
    allowedHosts: cfg.allowedHosts,
    // The server is stateless; the legacy session knobs now size the
    // bearer→principal resolution cache (TTL + max entries). Env names are
    // kept so existing deploy manifests keep working.
    resolveCacheTtlMs: cfg.sessionIdleMs,
    resolveCacheMaxEntries: cfg.maxSessions,
    publicUrl: cfg.publicUrl,
    authorizationServerUrl: cfg.authorizationServerUrl,
  });
  logJson("INFO", "listening", `e2a-mcp-http listening on :${bound}`, {
    port: bound,
    baseUrl: cfg.baseUrl,
    allowedHosts: cfg.allowedHosts,
  });

  // Graceful shutdown: stop accepting new connections, drain active
  // sessions, then exit. Hard ceiling at 30s to avoid hanging deploys.
  let closing = false;
  const shutdown = async (signal: NodeJS.Signals) => {
    if (closing) return;
    closing = true;
    logJson("INFO", "draining", `received ${signal}, draining...`, { signal });
    const drainTimeout = setTimeout(() => {
      logJson("ERROR", "drain_timeout", "drain timeout, forcing exit");
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
      logJson("ERROR", "shutdown_error", `shutdown error: ${message}`, { error: message });
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
    logJson("ERROR", "fatal", `e2a-mcp-http fatal: ${message}`, { error: message });
    process.exit(1);
  });
}
