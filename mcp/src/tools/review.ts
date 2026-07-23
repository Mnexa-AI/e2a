import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { paginationInput, runTool, strictInputSchema } from "./util.js";
import { attachmentsArraySchema } from "./attachments.js";
import { messageViewForTool } from "./messages.js";

export function registerReviewTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "list_reviews",
    {
      title: "List messages awaiting review",
      annotations: { readOnlyHint: true },
      description:
        "Account scope only. Use when the account owner asks what's awaiting approval, or after a send returned `pending_review`. Lists inbound screening holds and outbound send holds across every inbox in the authenticated account, sorted by soonest-expiring first. Body content is summary-only; call `get_review` with the review's `id` for full detail, then use that same ID with `approve_review` or `reject_review`. **Cursor-paginated:** returns one page in `reviews` plus `next_cursor` only when more remain; pass it back as `cursor`. The `email.review_requested` webhook is an additional push notification for either direction. Read-only; don't poll it on a loop.",
      inputSchema: strictInputSchema({ ...paginationInput }),
    },
    async (args) =>
      runTool(async () => {
        const page = await client.listReviews({
          ...(args.cursor !== undefined ? { cursor: args.cursor } : {}),
          ...(args.limit !== undefined ? { limit: args.limit } : {}),
        });
        return {
          reviews: page.items,
          ...(page.next_cursor ? { next_cursor: page.next_cursor } : {}),
        };
      }),
  );

  server.registerTool(
    "get_review",
    {
      title: "Get a review (full detail)",
      annotations: { readOnlyHint: true },
      description:
        "Account scope only. Fetch the full detail of one inbound screening hold or outbound send hold, including subject, recipients, body, attachments, and screening context. A review's `id` is the held message's id (msg_…) and is the same ID accepted by `approve_review` and `reject_review`. Body content is only present while the message is `pending_review`; after a terminal transition the server scrubs it. Like `get_message`, raw MIME (`raw_message`) and attachment bytes are intentionally omitted to protect context — attachments surface as metadata only.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("The held message / review ID (msg_…)."),
      }),
    },
    // Strip raw_message / attachment bytes via the same context-safe
    // projection get_message uses — an inbound hold's raw MIME can be a
    // multi-MB base64 blob. approve_review is unaffected: it builds its
    // payload from the caller's own override fields, not the held body.
    async (args) =>
      runTool(async () => messageViewForTool(await client.getReview(args.message_id))),
  );

  // Map the snake_case approve override args to the SDK's ApproveRequest
  // (camelCase body fields). An explicitly-passed empty attachments array
  // must survive as a strip override, so map by key-presence.
  const mapOverrides = (overrides: {
    subject?: string;
    text?: string;
    html?: string;
    to?: string[];
    cc?: string[];
    bcc?: string[];
    attachments?: Array<{ filename: string; content_type: string; data: string }>;
  }) => ({
    ...(overrides.subject !== undefined ? { subject: overrides.subject } : {}),
    ...(overrides.text !== undefined ? { text: overrides.text } : {}),
    ...(overrides.html !== undefined ? { html: overrides.html } : {}),
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
    "approve_review",
    {
      title: "Approve a held message (review)",
      annotations: { destructiveHint: false },
      description:
        "Release a message held in `pending_review` (a review). The server branches on the message's direction:\n" +
        "- **Outbound** (a draft awaiting send approval): the approval persists the send and it is queued for asynchronous submission (202 accepted — the terminal sent/failed outcome arrives later via webhook events or by polling; delivery may go via SMTP relay, upstream SMTP, or loopback, not SES specifically). Approve-as-is by passing only `message_id`, or apply reviewer edits via any subset of subject / text / html / to / cc / bcc / attachments (omit a field to keep the draft's value; pass it — including empty `attachments: []` to strip — to override).\n" +
        "- **Inbound** (a screening hold, available in `list_reviews` and also announced by the `email.review_requested` webhook): the message is RELEASED to the agent's inbox so it becomes readable. There is no send and no draft — any override fields are ignored.\n" +
        "Returns 409 if the message is no longer pending (a human or the TTL sweep already resolved it).",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        subject: z.string().optional(),
        text: z.string().optional(),
        html: z.string().optional(),
        to: z.array(z.string()).optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        idempotency_key: z
          .string()
          .optional()
          .describe(
            "Stable key for retry-safe approves. Applies to **outbound** approves, which queue a real send — a retried call without this header could double-send. (Inbound releases are idempotent row updates and ignore this.) For approve-as-is, the pending `message_id` is a natural stable key — same review event, same key, retry replays. **If you change overrides between attempts** (e.g. tweak the subject after a 5xx and retry), pick a fresh key per attempt: same key + different body returns 422.",
          ),
      }),
    },
    async (args) => {
      const { message_id, idempotency_key, ...overrides } = args;
      // The approve endpoint is account-scoped and id-addressed
      // (/v1/reviews/{id}/approve): it branches on direction server-side — an
      // outbound hold is sent, an inbound screening hold is released to the
      // agent's inbox (overrides ignored). Caller passes only message_id; no
      // owning-agent lookup needed.
      const mapped = mapOverrides(overrides);
      return runTool(() =>
        idempotency_key !== undefined
          ? client.approveReview(message_id, mapped, { idempotencyKey: idempotency_key })
          : client.approveReview(message_id, mapped),
      );
    },
  );

  server.registerTool(
    "reject_review",
    {
      title: "Reject a held message (review)",
      annotations: { destructiveHint: true },
      description:
        "Reject a message held in `pending_review` (a review). The server branches on direction: an **outbound** hold is discarded without sending and retained with its body and attachments, while an **inbound** screening hold is dropped so it never reaches the agent and its raw payload remains retained and hidden for forensics. The optional `reason` is stored for audit. Returns 409 if the message is no longer pending.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        reason: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() => client.rejectReview(args.message_id, args.reason)),
  );
}
