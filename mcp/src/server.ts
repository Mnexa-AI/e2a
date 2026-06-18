import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "./client.js";
import { registerMessageTools } from "./tools/messages.js";
import { registerAgentTools } from "./tools/agents.js";
import { registerDomainTools } from "./tools/domains.js";
import { registerHitlTools } from "./tools/hitl.js";
import { registerWebhookTools } from "./tools/webhooks.js";
import { registerEventTools } from "./tools/events.js";

export interface BuildServerOptions {
  client: McpClient;
  version?: string;
}

export function buildServer({ client, version = "0.1.0" }: BuildServerOptions): McpServer {
  const server = new McpServer({
    name: "e2a",
    version,
  });
  registerMessageTools(server, client);
  registerAgentTools(server, client);
  registerDomainTools(server, client);
  registerHitlTools(server, client);
  registerWebhookTools(server, client);
  registerEventTools(server, client);
  return server;
}
