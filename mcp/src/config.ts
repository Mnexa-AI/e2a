export interface McpConfig {
  apiKey: string;
  baseUrl?: string;
  agentEmail?: string;
}

export class ConfigError extends Error {}

// resolveURL returns the deployment base URL from the environment.
// Canonical name is E2A_URL (matches the CLI, SDK docs, and skill
// reference). E2A_BASE_URL is the legacy name this package shipped
// with in PR #82; we still read it so existing installs (Claude
// Desktop / Codex / ADK env blocks already populated against the
// public MCP catalog) keep working. Canonical wins when both are
// set; using the legacy name emits a single one-shot warning to
// stderr so operators know to migrate.
//
// Same dual-read pattern lives in tests/e2e-prod/harness/env.ts —
// keep them in sync if you change one.
function resolveBaseUrl(env: NodeJS.ProcessEnv): string | undefined {
  const canonical = env.E2A_URL;
  const legacy = env.E2A_BASE_URL;
  if (canonical) return canonical;
  if (legacy) {
    if (!resolveBaseUrl.warned) {
      process.stderr.write(
        "[e2a-mcp] E2A_BASE_URL is deprecated; rename it to E2A_URL (both names work today).\n",
      );
      resolveBaseUrl.warned = true;
    }
    return legacy;
  }
  return undefined;
}
resolveBaseUrl.warned = false;

export function loadConfig(env: NodeJS.ProcessEnv = process.env): McpConfig {
  const apiKey = env.E2A_API_KEY;
  if (!apiKey) {
    throw new ConfigError(
      "E2A_API_KEY is required. Set it in the MCP server's environment (typically via your ADK MCPToolset `env` config).",
    );
  }
  return {
    apiKey,
    baseUrl: resolveBaseUrl(env),
    // No default-agent env: an agent-scoped credential IS its agent (resolved
    // server-side / by the session prefetch), and account-scoped callers pass
    // `email` per tool. The legacy E2A_AGENT_EMAIL default was removed (§9a).
  };
}
