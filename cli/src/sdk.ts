import { E2AClient } from "@e2a/sdk/v1";
import { loadConfig, requireApiKey, type Config } from "./config.js";
import { EXIT, fail } from "./exit.js";

export interface CreateClientOptions {
  /** Per-attempt request timeout in ms (SDK default 30000). */
  timeoutMs?: number;
  /** Retry budget (SDK default 2). `doctor` sets 0: it reports, not retries. */
  maxRetries?: number;
}

export function createClient(
  options: CreateClientOptions = {},
  // Callers that already loaded (and possibly normalized) the config pass it
  // through so the client uses the exact same URL and the config file isn't
  // parsed twice per invocation.
  config: Config = loadConfig(),
): E2AClient {
  const apiKey = requireApiKey(config);
  return new E2AClient({ apiKey, baseUrl: config.api_url, ...options });
}

export function requireAgentEmail(fromOverride?: string): string {
  const config = loadConfig();
  const email = fromOverride || config.agent_email;
  if (!email) {
    // A missing inbox selection is a fixable invocation problem → USAGE (2),
    // per the documented exit-code contract.
    fail(
      EXIT.USAGE,
      "No agent email. Use --agent, set E2A_AGENT_EMAIL, or run: e2a config set agent_email <email>",
    );
  }
  return email;
}
