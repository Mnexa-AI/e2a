import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";

/**
 * Webhook-subscriber tools (top-level webhooks-as-a-resource).
 *
 * A webhook subscriber is owned by a user, not by an agent. One user
 * can configure many webhooks, each scoped by event-type and optional
 * filters (agent_ids, conversation_ids, labels). This is the sole push
 * mechanism — the legacy per-agent `webhook_url` / `agent_mode` was
 * removed (migration 029); inbound is poll (list_messages) / WebSocket /
 * these subscriptions. Supports fan-out, filtered subscriptions,
 * signing-secret rotation, and delivery history (via the events log).
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
      annotations: { readOnlyHint: true },
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
      annotations: { readOnlyHint: true },
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
      annotations: { destructiveHint: false },
      description:
        "Subscribe an HTTPS URL to one or more events. URL must be HTTPS and must resolve to a public IP (SSRF guard). The response includes a plaintext signing_secret which the caller MUST persist immediately — every subsequent list/get scrubs it. Per-user cap is 50 webhooks; rotate_webhook_secret rotates the secret in place with a 24h dual-sign grace window.",
      inputSchema: strictInputSchema({
        url: z.string().min(1).describe("HTTPS webhook URL. Public domain only — IPs are rejected."),
        events: z
          .array(z.string().min(1))
          .min(1)
          .describe(
            "Event types to subscribe to. Valid values: email.received, email.sent, email.pending_approval, email.approval_accepted, email.approval_rejected, email.delivered, email.bounced, email.complained, email.flagged, domain.sending_verified, domain.sending_failed, domain.suppression_added.",
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
      annotations: { idempotentHint: true, destructiveHint: false },
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
      annotations: { destructiveHint: true, idempotentHint: true },
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
      annotations: { destructiveHint: false },
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
      annotations: { destructiveHint: false },
      description:
        "Schedules a one-off delivery to the webhook with a synthetic envelope, bypassing filter matching. Returns the delivery_id; inspect the outcome (status/attempts/last_error) via `list_webhook_deliveries`. Returns an error if the webhook is disabled. Cheap and safe — the synthetic event does not touch real inbound or HITL state.",
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
      annotations: { readOnlyHint: true },
      description:
        "Returns the most recent delivery rows for one webhook. Each row includes status (pending|delivered|failed), attempts, last_error, last_status_code, and timestamps. The way to debug why a subscriber is missing events, or to check the outcome of a `test_webhook` call. Read-only. Distinct from `list_events` (the account-wide event log); this is the per-webhook delivery ledger.",
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Webhook id (wh_…)."),
        status: z
          .enum(["pending", "delivered", "failed"])
          .optional()
          .describe("Optionally restrict to one delivery status."),
        limit: z
          .number()
          .int()
          .min(1)
          .max(100)
          .optional()
          .describe("Max rows to return."),
      }),
    },
    async (args) =>
      runTool(async () => ({
        deliveries: await client.listWebhookDeliveries(args.id, {
          ...(args.status !== undefined ? { status: args.status } : {}),
          ...(args.limit !== undefined ? { limit: args.limit } : {}),
        }),
      })),
  );
}
