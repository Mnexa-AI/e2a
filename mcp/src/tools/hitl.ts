import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { z } from "zod";
import { runTool } from "./util.js";

export function registerHitlTools(server: McpServer, client: E2AClient): void {
  server.registerTool(
    "list_pending_messages",
    {
      title: "List outbound messages awaiting approval",
      description:
        "List outbound emails composed by HITL-enabled agents that are held pending human review, sorted by soonest-expiring first. Use this when the user asks what's waiting for them to approve. Body content is not included in summaries — call `get_pending_message` for the full draft.",
      inputSchema: {},
    },
    async () => runTool(() => client.listPendingMessages()),
  );

  server.registerTool(
    "get_pending_message",
    {
      title: "Get a pending-approval message",
      description:
        "Fetch the full draft (subject, recipients, body, attachments) of one outbound message awaiting human approval. Body content is only present while the message is `pending_approval` — after a terminal transition the server scrubs it.",
      inputSchema: {
        message_id: z.string().describe("The pending message ID (msg_…)."),
      },
    },
    async (args) => runTool(() => client.getPendingMessage(args.message_id)),
  );

  server.registerTool(
    "approve_pending_message",
    {
      title: "Approve a pending outbound message",
      description:
        "Send a held outbound message via the upstream relay. Approve-as-is by passing only `message_id`, or apply reviewer edits by supplying any subset of subject / body_text / body_html / to / cc / bcc — those fields override the stored draft before send. Returns 409 if the message is no longer pending.",
      inputSchema: {
        message_id: z.string(),
        subject: z.string().optional(),
        body_text: z.string().optional(),
        body_html: z.string().optional(),
        to: z.array(z.string()).optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
      },
    },
    async (args) => {
      const { message_id, ...overrides } = args;
      return runTool(() => client.approveMessage(message_id, overrides));
    },
  );

  server.registerTool(
    "reject_pending_message",
    {
      title: "Reject a pending outbound message",
      description:
        "Discard a held outbound message. The message is never sent and body columns are scrubbed; the optional `reason` is stored for audit. Returns 409 if the message is no longer pending.",
      inputSchema: {
        message_id: z.string(),
        reason: z.string().optional(),
      },
    },
    async (args) => runTool(() => client.rejectMessage(args.message_id, args.reason)),
  );
}
