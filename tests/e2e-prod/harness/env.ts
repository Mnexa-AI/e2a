import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export interface ProdEnv {
  apiUrl: string;
  siteUrl: string;
  apiKey: string;
  primaryAgentEmail: string;
  sinkEmail: string;
  sharedDomain: string;
  // Deployed streamable-HTTP MCP endpoint the MCP suites target. Defaults to
  // `${apiUrl}/mcp` (Caddy routes /mcp to the co-versioned mcp-server image);
  // override with E2A_MCP_URL for an out-of-band host (e.g. an in-cluster URL).
  mcpUrl: string;
  // Optional separate STANDARD-class, low-cap account for enforcement tests
  // (limit + rate-limit). Absent → the enforcement suite skips, since the main
  // conformance account is internal-class and by construction exempt.
  quotaApiKey?: string;
  quotaAgentEmail?: string;
  allowStress: boolean;
  cleanupMode: "always" | "on_success" | "never";
  rateLimitRps: number;
}

function normalizeBaseUrl(value: string): string {
  return value.trim().replace(/\/+$/, "");
}

export function resolveSiteUrl(apiUrl: string, explicitSiteUrl?: string): string {
  if (explicitSiteUrl?.trim()) return normalizeBaseUrl(explicitSiteUrl);

  const normalizedApiUrl = normalizeBaseUrl(apiUrl);
  const apiOrigin = new URL(normalizedApiUrl).origin;
  if (apiOrigin === "https://api.e2a.dev") return "https://e2a.dev";
  if (apiOrigin === "https://api-staging.e2a.dev") return "https://staging.e2a.dev";
  return normalizedApiUrl;
}

export function resolveSinkEmail(explicitSinkEmail?: string): string {
  const sinkEmail = explicitSinkEmail?.trim();
  if (!sinkEmail) {
    throw new Error("E2E_SINK_EMAIL is required and must name a safe test sink; never use a real agent address");
  }
  return sinkEmail;
}

function readLocalConfig(): { api_key?: string; api_url?: string; agent_email?: string; shared_domain?: string } {
  try {
    const raw = readFileSync(join(homedir(), ".e2a", "config.json"), "utf-8");
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// pickEnv returns the first non-empty env var from `names`, with a
// one-shot stderr warning when a non-canonical (i.e. non-first) name
// is what actually carries the value. Mirrors the dual-read pattern
// in cli/src/config.ts and mcp/src/config.ts so all three surfaces
// drift on the same migration schedule.
const warned = new Set<string>();
function pickEnv(canonical: string, ...legacy: string[]): string | undefined {
  if (process.env[canonical]) return process.env[canonical];
  for (const name of legacy) {
    const v = process.env[name];
    if (v) {
      if (!warned.has(name)) {
        process.stderr.write(
          `[e2e-prod] ${name} is deprecated; rename it to ${canonical} (both names work today).\n`,
        );
        warned.add(name);
      }
      return v;
    }
  }
  return undefined;
}

export function loadEnv(): ProdEnv {
  const local = readLocalConfig();
  const apiUrl = pickEnv("E2A_URL", "E2A_API_URL") ?? local.api_url ?? "https://e2a.dev";
  const primaryAgentEmail = pickEnv("E2A_AGENT_EMAIL", "E2A_PRIMARY_AGENT") ?? local.agent_email ?? "";
  const env: ProdEnv = {
    apiUrl,
    siteUrl: resolveSiteUrl(apiUrl, process.env.E2A_SITE_URL),
    mcpUrl: "", // filled below once apiUrl is known
    apiKey: process.env.E2A_API_KEY ?? local.api_key ?? "",
    primaryAgentEmail,
    sinkEmail: resolveSinkEmail(process.env.E2E_SINK_EMAIL),
    sharedDomain: process.env.E2A_SHARED_DOMAIN ?? local.shared_domain ?? "agents.e2a.dev",
    quotaApiKey: process.env.E2A_QUOTA_API_KEY || undefined,
    quotaAgentEmail: process.env.E2A_QUOTA_AGENT_EMAIL || undefined,
    allowStress: process.env.E2E_PROD_STRESS === "1",
    cleanupMode: (process.env.E2E_CLEANUP as ProdEnv["cleanupMode"]) ?? "always",
    rateLimitRps: Number(process.env.E2E_RPS ?? "1"),
  };
  // Default the MCP endpoint to the API host's /mcp route; E2A_MCP_URL overrides.
  env.mcpUrl = process.env.E2A_MCP_URL || new URL("/mcp", env.apiUrl).toString();
  if (!env.apiKey) {
    throw new Error("No API key found. Set E2A_API_KEY or run `e2a login` first.");
  }
  if (!env.primaryAgentEmail) {
    throw new Error("No primary agent email. Set E2A_AGENT_EMAIL.");
  }
  return env;
}
