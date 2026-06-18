import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";

/**
 * Webhook-subscriber tools (top-level webhooks-as-a-resource).
 *
 * A webhook subscriber is owned by a user, not by an agent. One user
 * can configure many webhooks, each scoped by event-type and optional
 * filters (agent_ids, conversation_ids, labels). Distinct from the
 * legacy `agent_identities.webhook_url` field — that single-URL path
 * still works for cloud-mode agents; this resource is the upgrade path
 * for fan-out, filtered subscriptions, signing-secret rotation, and
 * delivery history.
 *
 * The plaintext signing_secret is returned ONCE on create + on
 * rotate-secret. Other reads scrub it.
 */
export function registerWebhookTools(server: McpServer, client: McpClient): void {
  const filtersSchema = z
    .object({
      agent_ids: z.array(z.string()).optional(),
      conversation_ids: z.array(z.string()).optional(),
      labels: z.array(z.string()).optional(),
    })
    .strict()
    .describe(
      "Optional scope filters. Empty / missing keys mean 'no constraint of that type'. agent_ids must reference agents owned by the caller; cross-user ids are rejected. conversation_ids / labels are exact-match.",
    );

  // Map the snake_case tool filter shape to the SDK's camelCase
  // WebhookFiltersView. Returns undefined for an absent filter so we
  // don't send an empty object.
  const mapFilters = (
    f?: { agent_ids?: string[]; conversation_ids?: string[]; labels?: string[] },
  ): { agentIds?: string[]; conversationIds?: string[]; labels?: string[] } | undefined => {
    if (!f) return undefined;
    return {
      ...(f.agent_ids !== undefined ? { agentIds: f.agent_ids } : {}),
      ...(f.conversation_ids !== undefined ? { conversationIds: f.conversation_ids } : {}),
      ...(f.labels !== undefined ? { labels: f.labels } : {}),
    };
  };

  server.registerTool(
    "list_webhooks",
    {
      title: "List webhook subscribers",
      description:
        "Returns every webhook subscriber owned by the authenticated user, enabled + disabled, with their event subscriptions, filters, and last-delivered timestamp. signing_secret is omitted (it is only ever returned on create + rotate). Read-only; cheap.",
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(async () => ({ webhooks: await client.listWebhooks() })),
  );

  server.registerTool(
    "get_webhook",
    {
      title: "Show one webhook subscriber",
      description:
        "Fetch a single webhook by id. signing_secret is omitted — use rotate_webhook_secret if the secret was lost.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
      }),
    },
    async (args) => runTool(() => client.getWebhook(args.id)),
  );

  server.registerTool(
    "create_webhook",
    {
      title: "Create a webhook subscriber (returns plaintext signing_secret ONCE)",
      description:
        "Subscribe an HTTPS URL to one or more events (email.received, email.sent, email.pending_approval, email.approved, email.rejected). URL must be HTTPS and must resolve to a public IP (SSRF guard). The response includes a plaintext signing_secret which the caller MUST persist immediately — every subsequent list/get scrubs it. Per-user cap is 50 webhooks; rotate_webhook_secret rotates the secret in place with a 24h dual-sign grace window.",
      inputSchema: strictInputSchema({
        url: z.string().min(1).describe("HTTPS webhook URL. Public domain only — IPs are rejected."),
        events: z
          .array(z.string().min(1))
          .min(1)
          .describe(
            "Event types to subscribe to. Valid: email.received, email.sent, email.pending_approval, email.approved, email.rejected.",
          ),
        description: z.string().optional().describe("Optional free-form label (max 200 chars)."),
        filters: filtersSchema.optional(),
      }),
    },
    async (args) =>
      runTool(() =>
        client.createWebhook({
          url: args.url,
          events: args.events,
          ...(args.description !== undefined ? { description: args.description } : {}),
          ...(mapFilters(args.filters) ? { filters: mapFilters(args.filters) } : {}),
        }),
      ),
  );

  server.registerTool(
    "update_webhook",
    {
      title: "Update a webhook subscriber",
      description:
        "Partial update. Fields you do NOT pass are left unchanged. url / events / filters are full-replace when present (no array merge). Use enabled:false to pause delivery without losing config; enabled:true re-enables (subject to a 5-min cooldown after auto-disable).",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
        url: z.string().optional(),
        events: z.array(z.string().min(1)).optional(),
        filters: filtersSchema.optional(),
        description: z.string().optional(),
        enabled: z.boolean().optional(),
      }),
    },
    async (args) => {
      const { id, filters, ...rest } = args;
      const mapped = mapFilters(filters);
      return runTool(() =>
        client.updateWebhook(id, {
          ...rest,
          ...(mapped ? { filters: mapped } : {}),
        }),
      );
    },
  );

  server.registerTool(
    "delete_webhook",
    {
      title: "Delete a webhook subscriber (DESTRUCTIVE)",
      description:
        "Permanently remove a webhook subscription. CASCADES to pending delivery rows. Requires confirm:true so an LLM cannot delete on ambiguous context.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
        confirm: z.literal(true).describe("Must be true to proceed."),
      }),
    },
    async (args) =>
      runTool(async () => {
        if (args.confirm !== true) {
          throw new Error("delete_webhook requires confirm:true.");
        }
        await client.deleteWebhook(args.id);
        return { deleted: args.id };
      }),
  );

  server.registerTool(
    "rotate_webhook_secret",
    {
      title: "Rotate a webhook's signing secret (returns new plaintext ONCE)",
      description:
        "Generate a new signing_secret and move the current one into a 24h grace window during which the worker dual-signs each delivery (two v1= entries on X-E2A-Signature). The new plaintext is returned ONCE — every subsequent list/get scrubs it. Use when the previous secret was leaked or rotated by policy.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
      }),
    },
    async (args) => runTool(() => client.rotateWebhookSecret(args.id)),
  );

  server.registerTool(
    "test_webhook",
    {
      title: "Fire a synthetic event to a webhook for debugging",
      description:
        "Schedules a one-off delivery to the webhook with a synthetic envelope, bypassing filter matching. Returns the delivery_id which can be looked up via list_webhook_deliveries. Returns an error if the webhook is disabled. Cheap and safe — the synthetic event does not touch real inbound or HITL state.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
        event: z
          .string()
          .optional()
          .describe(
            "Event type to simulate. Defaults to email.received when omitted.",
          ),
      }),
    },
    async (args) =>
      runTool(() => client.testWebhook(args.id, { event: args.event })),
  );

  server.registerTool(
    "list_webhook_deliveries",
    {
      title: "List recent delivery attempts for a webhook",
      description:
        "Returns the most recent delivery rows for the webhook (capped at 100). Each row includes status (pending|delivered|failed), attempts, last_error, last_status_code, and timestamps. Useful for debugging why a subscriber is missing events.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
        limit: z
          .number()
          .int()
          .min(1)
          .max(100)
          .optional()
          .describe("Page size. Defaults to 20."),
        status: z
          .enum(["pending", "delivered", "failed"])
          .optional()
          .describe("Optionally restrict to one status."),
      }),
    },
    async (args) =>
      runTool(() =>
        client.listWebhookDeliveries(args.id, {
          limit: args.limit,
          status: args.status,
        }),
      ),
  );
}
