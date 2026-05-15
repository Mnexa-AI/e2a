import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { z } from "zod";
import { runTool } from "./util.js";

export function registerMessageTools(server: McpServer, client: E2AClient): void {
  server.registerTool(
    "send_email",
    {
      title: "Send email",
      description:
        "Send a new email from the agent's inbox. Use this for outbound mail to a fresh recipient. To reply to a thread you received, use `reply_to_message` instead so threading headers (In-Reply-To, References) are preserved. If the agent has HITL approval enabled, the message is held for human review and the response indicates `status: pending_approval` rather than `sent`.",
      inputSchema: {
        to: z.array(z.string()).describe("Recipient email addresses (one or more)."),
        subject: z.string(),
        body: z.string().describe("Plain-text body. Use `html_body` for HTML."),
        html_body: z.string().optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        conversation_id: z
          .string()
          .optional()
          .describe("Optional conversation grouping ID. Server generates one if omitted."),
        agent_email: z
          .string()
          .optional()
          .describe(
            "Sending agent's inbox. Omit when E2A_AGENT_EMAIL is set in the server environment.",
          ),
      },
    },
    async (args) =>
      runTool(() =>
        client.send(args.to, args.subject, args.body, {
          ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
          ...(args.cc !== undefined ? { cc: args.cc } : {}),
          ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
          ...(args.conversation_id !== undefined
            ? { conversationId: args.conversation_id }
            : {}),
          ...(args.agent_email !== undefined ? { agentEmail: args.agent_email } : {}),
        }),
      ),
  );

  server.registerTool(
    "reply_to_message",
    {
      title: "Reply to a received message",
      description:
        "Reply to an inbound message identified by `message_id`. Preserves the References and In-Reply-To headers so the reply lands in the same email thread as the original. Pass `reply_all: true` to copy the original Cc list. Subject is auto-derived (Re: …) by the server.",
      inputSchema: {
        message_id: z.string().describe("ID of the inbound message to reply to (e.g. msg_…)."),
        body: z.string().describe("Plain-text reply body."),
        html_body: z.string().optional(),
        reply_all: z
          .boolean()
          .optional()
          .describe("If true, copy the original message's Cc list."),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        conversation_id: z.string().optional(),
        agent_email: z.string().optional(),
      },
    },
    async (args) =>
      runTool(() =>
        client.reply(args.message_id, args.body, {
          ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
          ...(args.reply_all !== undefined ? { replyAll: args.reply_all } : {}),
          ...(args.cc !== undefined ? { cc: args.cc } : {}),
          ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
          ...(args.conversation_id !== undefined
            ? { conversationId: args.conversation_id }
            : {}),
          ...(args.agent_email !== undefined ? { agentEmail: args.agent_email } : {}),
        }),
      ),
  );

  server.registerTool(
    "list_messages",
    {
      title: "List inbound messages",
      description:
        "List messages the agent has received, newest first. Filter by `status` (unread/read/all; default unread) and paginate with `page_size` + `token`. Returns summaries only — use `get_message` for the full body.",
      inputSchema: {
        status: z.enum(["unread", "read", "all"]).optional(),
        page_size: z.number().int().positive().max(100).optional(),
        token: z.string().optional().describe("Pagination token from a previous response."),
        agent_email: z.string().optional(),
      },
    },
    async (args) =>
      runTool(() =>
        client.listMessages({
          ...(args.status !== undefined ? { status: args.status } : {}),
          ...(args.page_size !== undefined ? { pageSize: args.page_size } : {}),
          ...(args.token !== undefined ? { token: args.token } : {}),
          ...(args.agent_email !== undefined ? { agentEmail: args.agent_email } : {}),
        }),
      ),
  );

  server.registerTool(
    "get_message",
    {
      title: "Get a message",
      description:
        "Fetch full detail for one inbound message — body, headers, auth results, and attachment metadata. Pass the `message_id` from `list_messages`.",
      inputSchema: {
        message_id: z.string(),
        agent_email: z.string().optional(),
      },
    },
    async (args) =>
      runTool(() => {
        const agentEmail = args.agent_email ?? client.agentEmail;
        if (!agentEmail) {
          throw new Error(
            "agent_email is required (no E2A_AGENT_EMAIL in environment).",
          );
        }
        return client.api.getMessage(agentEmail, args.message_id);
      }),
  );
}
