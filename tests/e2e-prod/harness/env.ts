import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export interface ProdEnv {
  apiUrl: string;
  apiKey: string;
  primaryAgentEmail: string;
  sharedDomain: string;
  // Optional separate STANDARD-class, low-cap account for enforcement tests
  // (limit + rate-limit). Absent → the enforcement suite skips, since the main
  // conformance account is internal-class and by construction exempt.
  quotaApiKey?: string;
  quotaAgentEmail?: string;
  allowStress: boolean;
  cleanupMode: "always" | "on_success" | "never";
  rateLimitRps: number;
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
  const env: ProdEnv = {
    apiUrl: pickEnv("E2A_URL", "E2A_API_URL") ?? local.api_url ?? "https://e2a.dev",
    apiKey: process.env.E2A_API_KEY ?? local.api_key ?? "",
    primaryAgentEmail: pickEnv("E2A_AGENT_EMAIL", "E2A_PRIMARY_AGENT") ?? local.agent_email ?? "",
    sharedDomain: process.env.E2A_SHARED_DOMAIN ?? local.shared_domain ?? "agents.e2a.dev",
    quotaApiKey: process.env.E2A_QUOTA_API_KEY || undefined,
    quotaAgentEmail: process.env.E2A_QUOTA_AGENT_EMAIL || undefined,
    allowStress: process.env.E2E_PROD_STRESS === "1",
    cleanupMode: (process.env.E2E_CLEANUP as ProdEnv["cleanupMode"]) ?? "always",
    rateLimitRps: Number(process.env.E2E_RPS ?? "1"),
  };
  if (!env.apiKey) {
    throw new Error("No API key found. Set E2A_API_KEY or run `e2a login` first.");
  }
  if (!env.primaryAgentEmail) {
    throw new Error("No primary agent email. Set E2A_AGENT_EMAIL.");
  }
  return env;
}
