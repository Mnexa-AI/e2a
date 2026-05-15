import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { registerMessageTools } from "./tools/messages.js";
import { registerAgentTools } from "./tools/agents.js";
import { registerHitlTools } from "./tools/hitl.js";

export interface BuildServerOptions {
  client: E2AClient;
  version?: string;
}

export function buildServer({ client, version = "0.1.0" }: BuildServerOptions): McpServer {
  const server = new McpServer({
    name: "e2a",
    version,
  });
  registerMessageTools(server, client);
  registerAgentTools(server, client);
  registerHitlTools(server, client);
  return server;
}
