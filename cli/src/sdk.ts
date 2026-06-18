import { E2AClient } from "@e2a/sdk/v1";
import { loadConfig, requireApiKey } from "./config.js";

export function createClient(_opts?: { from?: string }): E2AClient {
  const config = loadConfig();
  const apiKey = requireApiKey(config);
  return new E2AClient({ apiKey, baseUrl: config.api_url });
}

export function requireAgentEmail(fromOverride?: string): string {
  const config = loadConfig();
  const email = fromOverride || config.agent_email;
  if (!email) {
    process.stderr.write(
      "No agent email. Use --agent or run: e2a config set agent_email <email>\n",
    );
    process.exit(1);
  }
  return email;
}
