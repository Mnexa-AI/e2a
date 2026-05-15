import { E2AClient } from "@e2a/sdk/v1";
import type { McpConfig } from "./config.js";

export function makeClient(cfg: McpConfig): E2AClient {
  return new E2AClient({
    apiKey: cfg.apiKey,
    ...(cfg.baseUrl ? { baseUrl: cfg.baseUrl } : {}),
    ...(cfg.agentEmail ? { agentEmail: cfg.agentEmail } : {}),
  });
}
