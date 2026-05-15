import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { runTool } from "./util.js";

export function registerAgentTools(server: McpServer, client: E2AClient): void {
  server.registerTool(
    "list_agents",
    {
      title: "List agents",
      description:
        "List every agent inbox owned by the authenticated user. Useful for orientation — which inbox to send `from` or query messages against. Read-only.",
      inputSchema: {},
    },
    async () => runTool(() => client.listAgents()),
  );
}
