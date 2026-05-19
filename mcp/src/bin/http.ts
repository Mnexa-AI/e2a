#!/usr/bin/env node
import { startHttpServer } from "../http-server.js";

const port = Number(process.env.PORT ?? 3000);
const baseUrl = process.env.E2A_BASE_URL ?? "https://e2a.dev";
const allowedHosts = (process.env.MCP_ALLOWED_HOSTS ?? "mcp.e2a.dev")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);
const sessionIdleMs = Number(process.env.MCP_SESSION_IDLE_MS ?? 5 * 60_000);
const maxSessions = Number(process.env.MCP_MAX_SESSIONS ?? 500);

async function main(): Promise<void> {
  const { close, port: bound } = await startHttpServer(port, {
    baseUrl,
    allowedHosts,
    sessionIdleMs,
    maxSessions,
  });
  process.stderr.write(
    `e2a-mcp-http listening on :${bound} (base ${baseUrl}, hosts ${allowedHosts.join(",")})\n`,
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
      process.exit(0);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      process.stderr.write(`shutdown error: ${message}\n`);
      process.exit(1);
    }
  };
  process.on("SIGTERM", () => void shutdown("SIGTERM"));
  process.on("SIGINT", () => void shutdown("SIGINT"));
}

main().catch((err) => {
  const message = err instanceof Error ? err.stack ?? err.message : String(err);
  process.stderr.write(`e2a-mcp-http fatal: ${message}\n`);
  process.exit(1);
});
