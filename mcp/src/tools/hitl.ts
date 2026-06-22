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
      annotations: { readOnlyHint: true },
      description:
        "Use when the user asks what's awaiting approval, or after a `send_message`/`reply_to_message` returned `pending_review` and they want to see the queue. Lists held **outbound** messages (held by the agent's outbound policy or content scan) sorted by soonest-expiring first. Body content is summary-only — call `get_pending_message` for the full draft of one. Read-only; cheap, but don't poll it on a loop. Note: this lists OUTBOUND holds only — a held **inbound** message (screening review) is surfaced by the `email.pending_review` webhook (with its `message_id`), not here, and is resolved with the same `approve_message`/`reject_message` tools.",
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(async () => ({ messages: await client.listPendingMessages() })),
  );

  server.registerTool(
    "get_pending_message",
    {
      title: "Get a pending-approval message",
      annotations: { readOnlyHint: true },
      description:
        "Fetch the full draft (subject, recipients, body, attachments) of one outbound message awaiting human approval. Body content is only present while the message is `pending_review` — after a terminal transition the server scrubs it.",
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
    "approve_message",
    {
      title: "Approve a held message",
      annotations: { destructiveHint: false },
      description:
        "Release a message held in `pending_review`. The server branches on the message's direction:\n" +
        "- **Outbound** (from the `list_pending_messages` queue): the draft is SENT via SES. Approve-as-is by passing only `message_id`, or apply reviewer edits via any subset of subject / body_text / body_html / to / cc / bcc / attachments (omit a field to keep the draft's value; pass it — including empty `attachments: []` to strip — to override).\n" +
        "- **Inbound** (a screening hold, discovered via the `email.pending_review` webhook): the message is RELEASED to the agent's inbox so it becomes readable. There is no send and no draft — any override fields are ignored.\n" +
        "Returns 409 if the message is no longer pending (a human or the TTL sweep already resolved it).",
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
            "Stable key for retry-safe approves. Applies to **outbound** approves, which fire a real SES send — a retried call without this header could double-send. (Inbound releases are idempotent row updates and ignore this.) For approve-as-is, the pending `message_id` is a natural stable key — same review event, same key, retry replays. **If you change overrides between attempts** (e.g. tweak the subject after a 5xx and retry), pick a fresh key per attempt: same key + different body returns 422.",
          ),
      }),
    },
    async (args) => {
      const { message_id, idempotency_key, ...overrides } = args;
      // The approve endpoint is account-scoped and branches on direction
      // server-side: an outbound hold is sent, an inbound screening hold is
      // released to the agent's inbox (overrides ignored). The client resolves
      // the owning agent before calling, keeping the tool surface minimal
      // (caller passes only message_id).
      const mapped = mapOverrides(overrides);
      return runTool(() =>
        idempotency_key !== undefined
          ? client.approveMessage(message_id, mapped, { idempotencyKey: idempotency_key })
          : client.approveMessage(message_id, mapped),
      );
    },
  );

  server.registerTool(
    "reject_message",
    {
      title: "Reject a held message",
      annotations: { destructiveHint: false },
      description:
        "Reject a message held in `pending_review`. The server branches on direction: an **outbound** hold is discarded (never sent; body columns scrubbed), and an **inbound** screening hold is dropped so it never reaches the agent (its raw payload is retained, hidden, for forensics). The optional `reason` is stored for audit. Returns 409 if the message is no longer pending.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        reason: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() => client.rejectMessage(args.message_id, args.reason)),
  );
}
