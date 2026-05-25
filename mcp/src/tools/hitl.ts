import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { z } from "zod";
import { runTool } from "./util.js";
import { attachmentsArraySchema } from "./attachments.js";

export function registerHitlTools(server: McpServer, client: E2AClient): void {
  server.registerTool(
    "list_pending_messages",
    {
      title: "List outbound messages awaiting approval",
      description:
        "Use when the user asks what's awaiting approval, or after a `send_email`/`reply_to_message` returned `pending_approval` and they want to see the queue. Lists held outbound messages from HITL-enabled agents sorted by soonest-expiring first. Body content is summary-only — call `get_pending_message` for the full draft of one. Read-only; cheap, but don't poll it on a loop.",
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
        "Use to release a held outbound message — typically after the user reviewed it via `get_pending_message`. Approve-as-is by passing only `message_id`; apply reviewer edits by supplying any subset of subject / body_text / body_html / to / cc / bcc / attachments (those override the stored draft before send). Field semantics: omit a field to keep the draft's value; pass it (including empty `attachments: []` to strip all attachments) to override. Returns 409 if the message is no longer pending.",
      inputSchema: {
        message_id: z.string(),
        subject: z.string().optional(),
        body_text: z.string().optional(),
        body_html: z.string().optional(),
        to: z.array(z.string()).optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
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
