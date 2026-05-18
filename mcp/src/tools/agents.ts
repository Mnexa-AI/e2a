import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { z } from "zod";
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

  server.registerTool(
    "whoami",
    {
      title: "Get the default agent's identity",
      description:
        "Return the agent inbox this server is scoped to (the value of E2A_AGENT_EMAIL). Use this to discover your own address before sending. If no default agent is configured, errors — call `list_agents` and pass `agent_email` explicitly to other tools.",
      inputSchema: {},
    },
    async () =>
      runTool(() => {
        const email = client.agentEmail;
        if (!email) {
          throw new Error(
            "no default agent configured. Set E2A_AGENT_EMAIL in the server environment, or call list_agents.",
          );
        }
        return client.api.getAgent(email);
      }),
  );

  server.registerTool(
    "create_agent",
    {
      title: "Create a new agent inbox on the shared domain",
      description:
        "Register a new agent using a slug on the deployment's shared domain (e.g. slug 'support-bot' → support-bot@<shared-domain>). Defaults to `local` mode so the agent receives mail via `list_messages` polling — no webhook server required. Pass `agent_mode: 'cloud'` and `webhook_url` for push delivery. For custom (non-shared) domains, use the e2a CLI or dashboard. Slug must be lowercase letters, numbers, and hyphens.",
      inputSchema: {
        slug: z
          .string()
          .regex(/^[a-z0-9][a-z0-9-]*$/)
          .describe(
            "Local part of the new email address. Lowercase letters, numbers, and hyphens; must start with a letter or number.",
          ),
        name: z
          .string()
          .optional()
          .describe("Display name for the agent. Defaults to the slug."),
        agent_mode: z
          .enum(["local", "cloud"])
          .optional()
          .describe(
            "`local` (default) for poll-based delivery via this MCP server. `cloud` requires `webhook_url` and pushes inbound mail to that URL.",
          ),
        webhook_url: z
          .string()
          .url()
          .optional()
          .describe("Required when `agent_mode` is `cloud`. Ignored in local mode."),
      },
    },
    async (args) =>
      runTool(() =>
        client.registerAgent({
          slug: args.slug,
          agent_mode: args.agent_mode ?? "local",
          ...(args.name !== undefined ? { name: args.name } : {}),
          ...(args.webhook_url !== undefined ? { webhook_url: args.webhook_url } : {}),
        }),
      ),
  );
}
