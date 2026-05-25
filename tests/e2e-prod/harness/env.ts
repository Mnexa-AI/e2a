import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export interface ProdEnv {
  apiUrl: string;
  apiKey: string;
  primaryAgentEmail: string;
  sharedDomain: string;
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

export function loadEnv(): ProdEnv {
  const local = readLocalConfig();
  const env: ProdEnv = {
    apiUrl: process.env.E2A_API_URL ?? local.api_url ?? "https://e2a.dev",
    apiKey: process.env.E2A_API_KEY ?? local.api_key ?? "",
    primaryAgentEmail: process.env.E2A_PRIMARY_AGENT ?? local.agent_email ?? "",
    sharedDomain: process.env.E2A_SHARED_DOMAIN ?? local.shared_domain ?? "agents.e2a.dev",
    allowStress: process.env.E2E_PROD_STRESS === "1",
    cleanupMode: (process.env.E2E_CLEANUP as ProdEnv["cleanupMode"]) ?? "always",
    rateLimitRps: Number(process.env.E2E_RPS ?? "1"),
  };
  if (!env.apiKey) {
    throw new Error("No API key found. Set E2A_API_KEY or run `e2a login` first.");
  }
  if (!env.primaryAgentEmail) {
    throw new Error("No primary agent email. Set E2A_PRIMARY_AGENT.");
  }
  return env;
}
