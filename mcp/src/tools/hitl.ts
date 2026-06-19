import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";
import { attachmentsArraySchema } from "./attachments.js";

export function registerHitlTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "list_pending_messages",
    {
      title: "List outbound messages awaiting approval",
      description:
        "Use when the user asks what's awaiting approval, or after a `send_email`/`reply_to_message` returned `pending_approval` and they want to see the queue. Lists held outbound messages from HITL-enabled agents sorted by soonest-expiring first. Body content is summary-only — call `get_pending_message` for the full draft of one. Read-only; cheap, but don't poll it on a loop.",
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(async () => ({ messages: await client.listPendingMessages() })),
  );

  server.registerTool(
    "get_pending_message",
    {
      title: "Get a pending-approval message",
      description:
        "Fetch the full draft (subject, recipients, body, attachments) of one outbound message awaiting human approval. Body content is only present while the message is `pending_approval` — after a terminal transition the server scrubs it.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("The pending message ID (msg_…)."),
      }),
    },
    async (args) => runTool(() => client.getPendingMessage(args.message_id)),
  );

  // Map the snake_case approve override args to the SDK's ApproveRequest
  // (camelCase body fields). An explicitly-passed empty attachments array
  // must survive as a strip override, so map by key-presence.
  const mapOverrides = (overrides: {
    subject?: string;
    body_text?: string;
    body_html?: string;
    to?: string[];
    cc?: string[];
    bcc?: string[];
    attachments?: Array<{ filename: string; content_type: string; data: string }>;
  }) => ({
    ...(overrides.subject !== undefined ? { subject: overrides.subject } : {}),
    ...(overrides.body_text !== undefined ? { body: overrides.body_text } : {}),
    ...(overrides.body_html !== undefined ? { htmlBody: overrides.body_html } : {}),
    ...(overrides.to !== undefined ? { to: overrides.to } : {}),
    ...(overrides.cc !== undefined ? { cc: overrides.cc } : {}),
    ...(overrides.bcc !== undefined ? { bcc: overrides.bcc } : {}),
    ...(overrides.attachments !== undefined
      ? {
          attachments: overrides.attachments.map((a) => ({
            filename: a.filename,
            contentType: a.content_type,
            data: a.data,
          })),
        }
      : {}),
  });

  server.registerTool(
    "approve_pending_message",
    {
      title: "Approve a pending outbound message",
      description:
        "Use to release a held outbound message — typically after the user reviewed it via `get_pending_message`. Approve-as-is by passing only `message_id`; apply reviewer edits by supplying any subset of subject / body_text / body_html / to / cc / bcc / attachments (those override the stored draft before send). Field semantics: omit a field to keep the draft's value; pass it (including empty `attachments: []` to strip all attachments) to override. Returns 409 if the message is no longer pending.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        subject: z.string().optional(),
        body_text: z.string().optional(),
        body_html: z.string().optional(),
        to: z.array(z.string()).optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        idempotency_key: z
          .string()
          .optional()
          .describe(
            "Stable key for retry-safe approves. Approve fires a real SES send, so a retried call without this header could double-send. For approve-as-is, the pending `message_id` is a natural stable key — same review event, same key, retry replays. **If you change overrides between attempts** (e.g. tweak the subject after a 5xx and retry), pick a fresh key per attempt: same key + different body returns 422.",
          ),
      }),
    },
    async (args) => {
      const { message_id, idempotency_key, ...overrides } = args;
      // The approve endpoint is agent-scoped; the client discovers the
      // owning agent (via the pending queue) before calling, keeping the
      // MCP tool surface minimal (caller passes only message_id).
      const mapped = mapOverrides(overrides);
      return runTool(() =>
        idempotency_key !== undefined
          ? client.approveMessage(message_id, mapped, { idempotencyKey: idempotency_key })
          : client.approveMessage(message_id, mapped),
      );
    },
  );

  server.registerTool(
    "reject_pending_message",
    {
      title: "Reject a pending outbound message",
      description:
        "Discard a held outbound message. The message is never sent and body columns are scrubbed; the optional `reason` is stored for audit. Returns 409 if the message is no longer pending.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        reason: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() => client.rejectMessage(args.message_id, args.reason)),
  );
}
