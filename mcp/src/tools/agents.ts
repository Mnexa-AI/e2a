import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";

export function registerAgentTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "list_agents",
    {
      title: "List agents",
      description:
        "List every agent inbox owned by the authenticated user. Useful for orientation — which inbox to send `from` or query messages against. Read-only.",
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(async () => ({ agents: await client.listAgents() })),
  );

  server.registerTool(
    "get_agent",
    {
      title: "Get one agent's configuration",
      description:
        "Fetch a single agent by its email address — full config: name, HITL settings (hitl_enabled/hitl_mode/hitl_ttl_seconds/hitl_expiration_action), inbound policy (inbound_policy/inbound_allowlist), and sending status. Use to inspect or confirm one agent's settings without listing every agent. Read-only.",
      inputSchema: strictInputSchema({
        email: z
          .string()
          .email()
          .describe("Agent to fetch (full email, e.g. support@acme.com)."),
      }),
    },
    async (args) => runTool(() => client.getAgent(args.email)),
  );

  server.registerTool(
    "whoami",
    {
      title: "Get the authenticated account's identity",
      description:
        "Use first when starting work on e2a to learn WHO you are: the authenticated user (email), the credential's scope (`account` or `agent`), and your plan + usage limits. For an agent-scoped credential it also returns `agent_address` — the single agent that credential IS. Account-scoped credentials own many agents; discover them with `list_agents`. This is identity, not an agent — it never guesses a 'default' agent.",
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(() => client.whoami()),
  );

  server.registerTool(
    "create_agent",
    {
      title: "Create a new agent inbox",
      description:
        "Register a new agent by its full email address. For a custom domain, the domain must be one you've registered and verified (`register_domain` → `verify_domain` → poll `get_domain` for sending). For the deployment's shared domain, just use an email on it (e.g. support-bot@<shared-domain>). Inbound is always available via `list_messages` (poll) or a `create_webhook` subscription — there is no delivery 'mode' to choose. Returns the full agent.",
      inputSchema: strictInputSchema({
        email: z
          .string()
          .email()
          .describe(
            "Full email address of the new agent, e.g. support@acme.com. The domain must be a verified domain you own, or the deployment's shared domain.",
          ),
        name: z
          .string()
          .optional()
          .describe("Display name for the agent."),
      }),
    },
    async (args) =>
      runTool(() =>
        client.createAgent({
          email: args.email,
          ...(args.name !== undefined ? { name: args.name } : {}),
        }),
      ),
  );

  server.registerTool(
    "update_agent",
    {
      title: "Update an agent's configuration",
      description:
        "Mutate an agent's HITL and inbound-policy settings. **The path to enable HITL approval gates** (HITL is NOT in create_agent): set `hitl_enabled: true`, optionally with `hitl_ttl_seconds`, `hitl_expiration_action`, and `hitl_mode` (`all` holds every outbound; `high_impact` holds only high-impact actions). Also sets the inbound trust gate: `inbound_policy` (`open`/`allowlist`/`domain`/`verified_only`) + `inbound_allowlist`. Omitted fields keep their current value; an explicit zero is honored (e.g. `hitl_ttl_seconds: 0`).",
      inputSchema: strictInputSchema({
        email: z
          .string()
          .email()
          .optional()
          .describe(
            "Agent to update (full email). Defaults to the credential's bound agent (agent-scoped credentials); required otherwise.",
          ),
        hitl_enabled: z
          .boolean()
          .optional()
          .describe(
            "Hold outbound mail for human approval before it ships. When true, send/reply/forward return a pending message id rather than a sent receipt; reviewers approve via the dashboard or the magic link.",
          ),
        hitl_ttl_seconds: z
          .number()
          .int()
          .min(0)
          .optional()
          .describe("How long a pending outbound stays in the approval queue before it expires."),
        hitl_expiration_action: z
          .enum(["approve", "reject"])
          .optional()
          .describe("At TTL expiry: `approve` ships the pending message; `reject` drops it."),
        hitl_mode: z
          .enum(["all", "high_impact"])
          .optional()
          .describe(
            "What HITL holds: `all` (every outbound) or `high_impact` (only high-impact actions on weakly-authenticated inbound). Meaningful only when hitl_enabled.",
          ),
        inbound_policy: z
          .enum(["open", "allowlist", "domain", "verified_only"])
          .optional()
          .describe(
            "Inbound ingestion gate: `open` (accept all), `allowlist`/`domain` (accept only listed senders/domains), `verified_only` (require SPF+DKIM+DMARC alignment). Non-matches are flagged, not dropped.",
          ),
        inbound_allowlist: z
          .array(z.string())
          .optional()
          .describe("Trusted sender addresses (for `allowlist`) or domains (for `domain`)."),
      }),
    },
    async (args) =>
      runTool(() => {
        const patch: {
          hitlEnabled?: boolean;
          hitlTtlSeconds?: number;
          hitlExpirationAction?: string;
          hitlMode?: string;
          inboundPolicy?: string;
          inboundAllowlist?: Array<string>;
        } = {
          ...(args.hitl_enabled !== undefined ? { hitlEnabled: args.hitl_enabled } : {}),
          ...(args.hitl_ttl_seconds !== undefined ? { hitlTtlSeconds: args.hitl_ttl_seconds } : {}),
          ...(args.hitl_expiration_action !== undefined
            ? { hitlExpirationAction: args.hitl_expiration_action }
            : {}),
          ...(args.hitl_mode !== undefined ? { hitlMode: args.hitl_mode } : {}),
          ...(args.inbound_policy !== undefined ? { inboundPolicy: args.inbound_policy } : {}),
          ...(args.inbound_allowlist !== undefined
            ? { inboundAllowlist: args.inbound_allowlist }
            : {}),
        };
        return client.updateAgent(patch, args.email);
      }),
  );

  server.registerTool(
    "delete_agent",
    {
      title: "Delete an agent inbox (DESTRUCTIVE)",
      description:
        "Permanently delete the agent identity and CASCADE-remove every message, pending outbound, and webhook-delivery record bound to it. Irreversible. Existing OAuth tokens bound to this agent are revoked automatically. Requires `confirm: true` — set it explicitly to acknowledge the destructive action.",
      inputSchema: strictInputSchema({
        email: z
          .string()
          .email()
          .optional()
          .describe(
            "Agent to delete (full email). Defaults to the credential's bound agent (agent-scoped credentials); required otherwise.",
          ),
        confirm: z
          .literal(true)
          .describe(
            "Must be set to true to proceed. Guard against an LLM hallucinating a delete from ambiguous context.",
          ),
      }),
    },
    async (args) =>
      runTool(async () => {
        if (args.confirm !== true) {
          throw new Error(
            "delete_agent requires confirm:true — refusing to proceed without explicit confirmation.",
          );
        }
        const deleted = await client.deleteAgent(args.email);
        return { deleted };
      }),
  );
}
