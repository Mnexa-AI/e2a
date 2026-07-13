import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema, paginationInput } from "./util.js";

/**
 * API-key tools (admin tier — account scope only).
 *
 * Deliberately NARROWER than the REST surface (/v1/account/api-keys): the
 * create tool mints ONLY agent-scoped keys. `scope` is not an input — it is
 * hardwired to "agent" in the McpClient wrapper — so no MCP code path can
 * mint an account-scoped (workspace-admin) credential. An agent session that
 * could mint itself an admin key would be a privilege-escalation footgun;
 * account-scoped keys come from the dashboard or the raw API, where a human
 * is in the loop. List and delete cover the full key set (revocation is
 * de-escalation, and list is metadata-only — secrets are never retrievable
 * after creation).
 */
export function registerApiKeyTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "list_api_keys",
    {
      title: "List API keys (metadata only)",
      annotations: { readOnlyHint: true },
      description:
        "Returns the account's API keys — metadata only: `id`, `name`, non-secret `keyPrefix`, `scope`, bound `agentEmail` (agent-scoped keys only), and created/last-used/expiry timestamps. The secret is shown ONCE at creation and can never be retrieved again; a lost key must be deleted and re-minted. **Cursor-paginated:** returns one page in `api_keys` plus a `next_cursor` when more remain — pass it back as `cursor` for the next page. Read-only; cheap.",
      inputSchema: strictInputSchema({ ...paginationInput }),
    },
    async (args) =>
      runTool(async () => {
        const page = await client.listApiKeys({
          ...(args.cursor !== undefined ? { cursor: args.cursor } : {}),
          ...(args.limit !== undefined ? { limit: args.limit } : {}),
        });
        return { api_keys: page.items, ...(page.next_cursor ? { next_cursor: page.next_cursor } : {}) };
      }),
  );

  server.registerTool(
    "create_api_key",
    {
      title: "Mint an AGENT-scoped API key (returns plaintext key ONCE)",
      annotations: { destructiveHint: false },
      description:
        "Mint a new API key bound to one agent inbox. The key can act ONLY as that agent (send/read its mail); it cannot manage agents, domains, webhooks, or other keys. This tool mints agent-scoped keys exclusively — account-scoped (workspace-admin) keys cannot be created over MCP; use the dashboard or the raw API. The response includes the plaintext `key` which the caller MUST persist immediately — it is shown once and every subsequent list/get returns metadata only.",
      inputSchema: strictInputSchema({
        agent_email: z
          .string()
          .min(1)
          .describe("Inbox email to bind the key to (an agent owned by this account)."),
        name: z.string().optional().describe("Human label for the key."),
        expires_at: z
          .string()
          .datetime({ offset: true })
          .optional()
          .describe(
            "Optional hard expiry (RFC 3339, must be in the future). Omit for a never-expiring key.",
          ),
      }),
    },
    async (args) =>
      runTool(() =>
        client.createAgentApiKey({
          agentEmail: args.agent_email,
          ...(args.name !== undefined ? { name: args.name } : {}),
          ...(args.expires_at !== undefined ? { expiresAt: new Date(args.expires_at) } : {}),
        }),
      ),
  );

  server.registerTool(
    "delete_api_key",
    {
      title: "Revoke an API key (DESTRUCTIVE)",
      annotations: { destructiveHint: true, idempotentHint: true },
      description:
        "Permanently revoke an API key. Every request authenticated with it fails immediately; the key cannot be restored — mint a new one instead. CAUTION: revoking the key this session is authenticated with kills the session (and anything else using that key) mid-flight — when cleaning up keys, confirm with the user which key the current session uses before revoking anything that might be it. Requires confirm:true so an LLM cannot revoke on ambiguous context. Get the id from list_api_keys.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("API key id (from list_api_keys — NOT the secret key)."),
        confirm: z.literal(true).describe("Must be true to proceed."),
      }),
    },
    async (args) =>
      runTool(async () => {
        if (args.confirm !== true) {
          throw new Error("delete_api_key requires confirm:true.");
        }
        await client.deleteApiKey(args.id);
        return { deleted: args.id };
      }),
  );
}
