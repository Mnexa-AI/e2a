import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";

// Slice 8: MCP tool surfaces for the customer-facing events API.
//   list_events       — paginated listing with filters
//   get_event         — single event lookup
//   redeliver_event   — replay an event to a webhook
//
// These bring the MCP catalog from 18 → 21 tools. The new tools are
// auth-bound to the API key the MCP server was launched with — the
// LLM cannot list events for other accounts.

export function registerEventTools(server: McpServer, client: E2AClient): void {
  server.registerTool(
    "list_events",
    {
      title: "List webhook events",
      description:
        "List the durable webhook event log in reverse-chronological order. Useful for reconciliation (\"did our webhook receiver see this event?\") and for debugging delivery state. Events past the 30-day retention boundary are not returned. Cursor-paginated via `token` / `next_token` — pass the previous response's `next_token` to walk further back. Returns each event's `data` payload plus a `delivery_status` summary of how many subscribers have received it.",
      inputSchema: strictInputSchema({
        type: z
          .string()
          .optional()
          .describe(
            "Exact event type filter. Today: `email.received`, `email.sent`, `email.pending_approval`, `email.approved`, `email.rejected`.",
          ),
        agent_id: z.string().optional(),
        conversation_id: z.string().optional(),
        message_id: z.string().optional(),
        since: z
          .string()
          .optional()
          .describe("RFC3339 timestamp; returns events with `created_at >= since`."),
        until: z.string().optional().describe("RFC3339; returns events with `created_at < until`."),
        page_size: z.number().int().min(1).max(100).optional(),
        token: z.string().optional().describe("Opaque cursor from a previous response's `next_token`."),
      }),
    },
    async (args) =>
      runTool(() =>
        client.api.listEvents({
          type: args.type,
          agentId: args.agent_id,
          conversationId: args.conversation_id,
          messageId: args.message_id,
          since: args.since,
          until: args.until,
          pageSize: args.page_size,
          token: args.token,
        }),
      ),
  );

  server.registerTool(
    "get_event",
    {
      title: "Get one webhook event",
      description:
        "Fetch a single event by id. The response includes the full envelope payload AND a `delivery_status` block showing how many of the matched webhooks have delivered/pending/failed. Use this to triage \"did this specific event reach my receiver?\" Returns an error with status 410 if the event has passed the 30-day retention boundary (replay is no longer possible).",
      inputSchema: strictInputSchema({
        event_id: z.string().describe("Stable event id (evt_<32hex>)."),
      }),
    },
    async (args) => runTool(() => client.api.getEvent(args.event_id)),
  );

  server.registerTool(
    "redeliver_event",
    {
      title: "Replay a webhook event",
      description:
        "Re-fire a previously-emitted event to a webhook. Pass `webhook_id` to target one subscriber. Omit `webhook_id` to fan out to every webhook that originally matched the event (per the snapshot at fan-out time; does NOT re-evaluate against the current subscriber set, by design). **Important:** the replay uses the SAME envelope id as the original delivery. Customer-side receivers that dedupe on event id will discard the replay as already-processed — replay is recovery, not re-delivery. Use this for outage recovery, not for \"send this event twice on purpose.\"",
      inputSchema: strictInputSchema({
        event_id: z.string(),
        webhook_id: z
          .string()
          .optional()
          .describe(
            "Target webhook id. Must be in the originally-matched set (otherwise 409). Omit to fan out to every originally-matched webhook.",
          ),
      }),
    },
    async (args) =>
      runTool(() =>
        client.api.redeliverEvent(args.event_id, { webhookId: args.webhook_id }),
      ),
  );
}
