import { E2AClient } from "@e2a/sdk/v1";
import { loadConfig, requireApiKey } from "./config.js";
import { EXIT, fail } from "./exit.js";

export function createClient(): E2AClient {
  const config = loadConfig();
  const apiKey = requireApiKey(config);
  return new E2AClient({ apiKey, baseUrl: config.api_url });
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
