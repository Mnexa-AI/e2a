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
        "Use first when starting work on e2a to learn which agent you're acting AS — remember an agent IS an email address, and other tools default to this one. Resolution order: (1) `E2A_AGENT_EMAIL` from the server env when set, (2) the sole agent on the account when there's exactly one. Errors only when neither path resolves — typically because the account owns multiple agents. In that case the error inlines the available emails so you can pass one as `agent_email` to other tools (or have the user pin a default via `E2A_AGENT_EMAIL`); no follow-up `list_agents` call needed. If the error says zero agents, run `create_agent` first.",
      inputSchema: {},
    },
    async () =>
      runTool(async () => {
        // Preferred: env-pinned default. Cheap, no roundtrip.
        if (client.agentEmail) {
          return client.getAgent(client.agentEmail);
        }
        // Fallback: list the account's agents. If there's exactly one
        // we can unambiguously surface it — mirrors send_email's
        // "from field required only when user has multiple agents"
        // behavior on the backend. For 0 or 2+ agents the LLM needs
        // explicit input, so we error with a directive next-step.
        const { agents } = await client.listAgents();
        if (!agents || agents.length === 0) {
          throw new Error(
            "no agents on this account. Use `create_agent` to register one before sending mail.",
          );
        }
        if (agents.length === 1) {
          return client.getAgent(agents[0].email);
        }
        // Inline the agent emails in the error so the LLM can act
        // on this without a follow-up list_agents call. The list is
        // already in hand here — surfacing it costs nothing and
        // turns a two-tool-call recovery into a one-tool-call one.
        const emails = agents.map((a) => a.email).join(", ");
        throw new Error(
          `account owns ${agents.length} agents (${emails}); whoami can't auto-resolve. Pass one as \`agent_email\` to other tools, or set E2A_AGENT_EMAIL in the server environment to pin a default.`,
        );
      }),
  );

  server.registerTool(
    "create_agent",
    {
      title: "Create a new agent inbox on the shared domain",
      description:
        "Register a new agent using a slug on the deployment's shared domain (e.g. slug 'support-bot' → support-bot@<shared-domain>). Defaults to `local` mode so the agent receives mail via `list_messages` polling — no webhook server required. Pass `agent_mode: 'cloud'` and `webhook_url` for push delivery; in that case the webhook handler MUST HMAC-verify every delivery against the account's webhook signing secret (`E2A_WEBHOOK_SECRET`, shown in the dashboard) — the e2a SDK exposes `parseWebhook(body, secret)` for this. For a custom (non-shared) domain, use `register_domain` to start the verification flow. Slug must be lowercase letters, numbers, and hyphens.",
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

  server.registerTool(
    "update_agent",
    {
      title: "Update an agent's configuration",
      description:
        "Mutate a subset of an agent's settings. **This is the path to enable HITL approval gates** on an existing agent (HITL is NOT in the create_agent flow): set `hitl_enabled: true`, optionally with `hitl_ttl_seconds` and `hitl_expiration_action`. Same path to disable HITL, change the approval window, switch between local and cloud delivery, or rebind the webhook URL when an agent's downstream service moves. Omitted fields keep their current value server-side; an explicitly-passed zero is honored (e.g. `hitl_ttl_seconds: 0` sets the window to immediate, not a no-op).",
      inputSchema: {
        agent_email: z
          .string()
          .email()
          .optional()
          .describe(
            "Agent to update. Defaults to E2A_AGENT_EMAIL when set; required otherwise.",
          ),
        agent_mode: z
          .enum(["local", "cloud"])
          .optional()
          .describe(
            "`local` → poll-based delivery (via `list_messages`). `cloud` → push delivery; requires `webhook_url` to be set (now or already on the agent).",
          ),
        webhook_url: z
          .string()
          .url()
          .optional()
          .describe(
            "URL the cloud-mode delivery will POST to. The handler MUST HMAC-verify each delivery against the account's webhook signing secret.",
          ),
        hitl_enabled: z
          .boolean()
          .optional()
          .describe(
            "Hold outbound mail for human approval before it ships. When true, `send_email`/`reply_to_message` return a pending message id rather than a sent receipt; reviewers approve via the dashboard or the magic link.",
          ),
        hitl_ttl_seconds: z
          .number()
          .int()
          .min(0)
          .optional()
          .describe(
            "How long a pending outbound stays in the approval queue before it expires.",
          ),
        hitl_expiration_action: z
          .enum(["approve", "reject"])
          .optional()
          .describe(
            "What happens to a pending message at TTL expiry: `approve` ships it; `reject` drops it.",
          ),
      },
    },
    async (args) =>
      runTool(() => {
        const { agent_email, ...body } = args;
        return client.updateAgent(body, {
          ...(agent_email !== undefined ? { agentEmail: agent_email } : {}),
        });
      }),
  );

  server.registerTool(
    "delete_agent",
    {
      title: "Delete an agent inbox (DESTRUCTIVE)",
      description:
        "Permanently delete the agent identity and CASCADE-remove every message, pending outbound, and webhook-delivery record bound to it. Irreversible. Existing OAuth tokens bound to this agent are revoked automatically. Requires `confirm: true` — set it explicitly to acknowledge the destructive action.",
      inputSchema: {
        agent_email: z
          .string()
          .email()
          .optional()
          .describe(
            "Agent to delete. Defaults to E2A_AGENT_EMAIL when set; required otherwise.",
          ),
        confirm: z
          .literal(true)
          .describe(
            "Must be set to true to proceed. Guard against an LLM hallucinating a delete from ambiguous context.",
          ),
      },
    },
    async (args) =>
      runTool(async () => {
        if (args.confirm !== true) {
          throw new Error(
            "delete_agent requires confirm:true — refusing to proceed without explicit confirmation.",
          );
        }
        await client.deleteAgent(args.agent_email);
        return { deleted: args.agent_email ?? client.agentEmail };
      }),
  );
}
