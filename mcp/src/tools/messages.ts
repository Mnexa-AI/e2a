import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { simpleParser } from "mailparser";
import type { McpClient, SendOpts } from "../client.js";
import type { MessageView } from "@e2a/sdk/v1";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";
import { attachmentsArraySchema, type AttachmentInput } from "./attachments.js";

// Map the snake_case attachment wire shape (filename, content_type, data)
// to the SDK Attachment model (filename, contentType, data).
function mapAttachments(
  atts?: AttachmentInput[],
): Array<{ filename: string; contentType: string; data: string }> | undefined {
  if (atts === undefined) return undefined;
  return atts.map((a) => ({
    filename: a.filename,
    contentType: a.content_type,
    data: a.data,
  }));
}

// Decoded attachment from the message's raw MIME.
interface ParsedAttachment {
  filename: string;
  contentType: string;
  size: number;
  content: Buffer;
}

// The v1 MessageView no longer exposes decoded attachments inline (the
// flat SDK's InboundEmail did). It carries the full MIME blob in
// `rawMessage`, so we decode attachments on demand from that. Returns
// [] when there is no raw MIME (e.g. a body-only summary view).
async function parseAttachments(email: MessageView): Promise<ParsedAttachment[]> {
  if (!email.rawMessage) return [];
  // rawMessage is base64-encoded on the wire (contentEncoding: base64); decode
  // to the raw RFC822 bytes before parsing — simpleParser on the base64 text
  // finds nothing.
  const parsed = await simpleParser(Buffer.from(email.rawMessage, "base64"));
  return (parsed.attachments ?? []).map((a, i) => ({
    filename: a.filename ?? `attachment-${i}`,
    contentType: a.contentType ?? "application/octet-stream",
    size: a.size ?? a.content.length,
    content: a.content,
  }));
}

export function registerMessageTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "send_message",
    {
      title: "Send email",
      description:
        "Use when starting a NEW email thread to a fresh recipient. To respond to a message you can see in `list_messages`, use `reply_to_message` instead — it preserves the In-Reply-To / References headers so the reply lands in the same thread, which this tool deliberately does not do. Attach files via `attachments`; pass base64 strings produced by other tools (e.g. `get_attachment`) verbatim — don't hand-encode raw text. **`pending_approval` is not failure.** If the agent has HITL enabled, the response is `{ status: \"pending_approval\", message_id: ... }`; the message is held for human review — do not retry. Check on it with `list_messages` (held drafts show hitl_status=pending_approval) / `get_message`.",
      inputSchema: strictInputSchema({
        to: z.array(z.string()).describe("Recipient email addresses (one or more)."),
        subject: z.string(),
        body: z.string().describe("Plain-text body. Use `html_body` for HTML."),
        html_body: z.string().optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z
          .string()
          .optional()
          .describe("Optional conversation grouping ID. Server generates one if omitted."),
        idempotency_key: z
          .string()
          .optional()
          .describe(
            "Stable key for retry-safe sends. Set to deduplicate when the caller has its own retry loop (e.g. a stable triggering event id). When omitted the SDK mints a fresh UUIDv4 per call — protects against network-layer retries only, not user-driven retries.",
          ),
        email: z
          .string()
          .optional()
          .describe(
            "Sending agent's inbox. Omit to use the credential's bound agent (agent-scoped credentials).",
          ),
      }),
    },
    async (args) =>
      runTool(() => {
        const opts: SendOpts =
          args.idempotency_key !== undefined
            ? { idempotencyKey: args.idempotency_key }
            : {};
        return client.send(
          {
            to: args.to,
            subject: args.subject,
            body: args.body,
            ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
            ...(args.cc !== undefined ? { cc: args.cc } : {}),
            ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
            ...(mapAttachments(args.attachments) !== undefined
              ? { attachments: mapAttachments(args.attachments) }
              : {}),
            ...(args.conversation_id !== undefined
              ? { conversationId: args.conversation_id }
              : {}),
          },
          opts,
          args.email,
        );
      }),
  );

  server.registerTool(
    "reply_to_message",
    {
      title: "Reply to a received message",
      description:
        "Use whenever you're responding to a message you can see in the inbox — preserves the In-Reply-To and References headers so the reply joins the original email thread instead of starting a new one. Prefer this over `send_message` for any response to an inbound; thread fragmentation (broken conversation view in the recipient's mail client) is the most visible symptom of using `send_message` by mistake. Pass `reply_all: true` to copy the original Cc list; subject is auto-derived as `Re: …` by the server. Same HITL caveat as `send_message`: a `pending_approval` status is success, not failure.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("ID of the inbound message to reply to (e.g. msg_…)."),
        body: z.string().describe("Plain-text reply body."),
        html_body: z.string().optional(),
        reply_all: z
          .boolean()
          .optional()
          .describe("If true, copy the original message's Cc list."),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z.string().optional(),
        idempotency_key: z
          .string()
          .optional()
          .describe(
            "Stable key for retry-safe replies. A natural choice is the inbound `message_id` you're replying to — the same triggering event yields the same key, so a retry replays the original response instead of double-sending. Omit to let the SDK mint a fresh UUIDv4 per call.",
          ),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() => {
        const opts: SendOpts =
          args.idempotency_key !== undefined
            ? { idempotencyKey: args.idempotency_key }
            : {};
        return client.reply(
          args.message_id,
          {
            body: args.body,
            ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
            ...(args.reply_all !== undefined ? { replyAll: args.reply_all } : {}),
            ...(args.cc !== undefined ? { cc: args.cc } : {}),
            ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
            ...(mapAttachments(args.attachments) !== undefined
              ? { attachments: mapAttachments(args.attachments) }
              : {}),
            ...(args.conversation_id !== undefined
              ? { conversationId: args.conversation_id }
              : {}),
          },
          opts,
          args.email,
        );
      }),
  );

  server.registerTool(
    "forward_message",
    {
      title: "Forward an inbound message",
      description:
        "Forward a message the agent has received to one or more new recipients. The server auto-prepends a Gmail-style header block (From/Date/Subject/To/Cc) and the original body to whatever optional comment you pass in `body`/`html_body`. **Unlike `reply_to_message`, a forward is a NEW thread** — no In-Reply-To / References headers are emitted, so the recipient sees a fresh conversation. Use this when the user asks to share a received email with someone else; use `reply_to_message` when continuing the existing conversation. Same HITL behavior as send/reply: `pending_approval` is success, not failure.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("ID of the inbound message to forward (e.g. msg_…)."),
        to: z.array(z.string()).describe("Forward target addresses (one or more)."),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        body: z
          .string()
          .optional()
          .describe(
            "Optional plain-text comment to prepend above the forwarded content. The original body is appended automatically.",
          ),
        html_body: z.string().optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z
          .string()
          .optional()
          .describe(
            "Optional conversation grouping ID. A forward is a new thread by default — set this only to bind it to an existing thread explicitly.",
          ),
        idempotency_key: z
          .string()
          .optional()
          .describe(
            "Stable key for retry-safe forwards. The inbound `message_id` plus target list is a natural choice.",
          ),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() => {
        const opts: SendOpts =
          args.idempotency_key !== undefined
            ? { idempotencyKey: args.idempotency_key }
            : {};
        return client.forward(
          args.message_id,
          args.to,
          {
            // body is required on the wire (MSG-3); the original is auto-quoted,
            // so an empty comment is fine — default to "".
            body: args.body ?? "",
            ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
            ...(args.cc !== undefined ? { cc: args.cc } : {}),
            ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
            ...(mapAttachments(args.attachments) !== undefined
              ? { attachments: mapAttachments(args.attachments) }
              : {}),
            ...(args.conversation_id !== undefined
              ? { conversationId: args.conversation_id }
              : {}),
          },
          opts,
          args.email,
        );
      }),
  );

  server.registerTool(
    "update_message_labels",
    {
      title: "Add or remove labels on an inbound message",
      description:
        "Apply a labels delta — `add_labels` and/or `remove_labels`. Labels are lowercase strings drawn from `[a-z0-9:_-]+`, capped at 64 chars each; the `e2a:` prefix is reserved for server-applied system labels and rejected on writes. A label appearing in both lists is removed (remove wins). Per-request cap is 50 entries per list; per-message cap is 100 total labels. The response includes the post-update label set so you can echo back to the user without a follow-up read. Use this when the user wants to categorize a message (e.g. `add: urgent`) or clear a tag (`remove: follow-up`).",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("ID of the message to label."),
        add_labels: z
          .array(z.string())
          .optional()
          .describe("Labels to add. Already-set entries are no-ops."),
        remove_labels: z
          .array(z.string())
          .optional()
          .describe("Labels to remove. Entries not on the message are no-ops."),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() =>
        client.updateMessageLabels(
          args.message_id,
          {
            ...(args.add_labels !== undefined ? { addLabels: args.add_labels } : {}),
            ...(args.remove_labels !== undefined ? { removeLabels: args.remove_labels } : {}),
          },
          args.email,
        ),
      ),
  );

  server.registerTool(
    "list_conversations",
    {
      title: "List conversations for the agent",
      description:
        "Lists the agent's conversations — groups of messages sharing a `conversation_id` — one row per conversation, sorted by most recent activity. Each row carries `message_count`, `inbound_count`, `outbound_count`, `has_unread`, and the latest message's subject + sender so you can render an inbox without drilling into each thread. The server caps the response at 100. Use this when the user wants to see threads rather than individual messages — e.g. \"what conversations are unread?\" or \"show recent threads with Alice\". To read a single conversation's messages, call `get_conversation`.",
      inputSchema: strictInputSchema({
        page_size: z.number().int().positive().max(100).optional(),
        since: z
          .string()
          .optional()
          .describe(
            "RFC3339 timestamp. Only conversations whose latest message is >= since.",
          ),
        until: z
          .string()
          .optional()
          .describe(
            "RFC3339 timestamp. Only conversations whose latest message is < until.",
          ),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(async () => ({
        conversations: await client.listConversations(
          {
            ...(args.page_size !== undefined ? { limit: args.page_size } : {}),
            ...(args.since !== undefined ? { since: args.since } : {}),
            ...(args.until !== undefined ? { until: args.until } : {}),
          },
          args.email,
        ),
      })),
  );

  server.registerTool(
    "get_conversation",
    {
      title: "Get a single conversation with all member messages",
      description:
        "Returns the full thread — aggregate counts, the participants union (sender + recipient + to + cc + bcc across members), the labels union, and every member message in chronological order (oldest first). Returns a not-found error when no non-expired messages exist for `(agent, conversation_id)`. Use this after `list_conversations` (or whenever you have a `conversation_id` from an inbound/outbound payload) to read the full thread.",
      inputSchema: strictInputSchema({
        conversation_id: z.string(),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(() => client.getConversation(args.conversation_id, args.email)),
  );

  server.registerTool(
    "list_messages",
    {
      title: "List inbound messages",
      description:
        "List messages the agent has received, newest first by default. Filter by `read_status` (unread/read/all; default unread) and cap results with `page_size`. Pass `sort: \"asc\"` for FIFO order (oldest unread first) when the caller wants to drain the inbox in arrival order. **Search filters** (`from`, `subject_contains`, `conversation_id`, `since`, `until`) narrow the result set server-side — use them instead of paginating the full inbox client-side. Returns summaries only — use `get_message` for the full body.",
      inputSchema: strictInputSchema({
        read_status: z.enum(["unread", "read", "all"]).optional(),
        page_size: z.number().int().positive().max(100).optional(),
        sort: z
          .enum(["asc", "desc"])
          .optional()
          .describe(
            "Sort order by created_at. Defaults to `desc` (newest first). Pass `asc` for FIFO polling — drain the inbox in arrival order. Switching sort mid-pagination rejects the existing token.",
          ),
        from: z
          .string()
          .max(200)
          .optional()
          .describe(
            "Case-insensitive substring on the sender address. Example: `acme.com` matches every message from any `*@acme.com` sender.",
          ),
        subject_contains: z
          .string()
          .max(200)
          .optional()
          .describe(
            "Case-insensitive substring on the subject line. Example: `invoice` matches `Invoice #123` and `Your invoice`.",
          ),
        conversation_id: z
          .string()
          .max(200)
          .optional()
          .describe("Exact match on the thread/conversation id."),
        since: z
          .string()
          .optional()
          .describe(
            "RFC3339 timestamp. Only messages with `created_at >= since` are returned. Example: `2026-05-25T00:00:00Z`.",
          ),
        until: z
          .string()
          .optional()
          .describe(
            "RFC3339 timestamp. Only messages with `created_at < until` are returned. Combine with `since` to bracket a date range.",
          ),
        labels: z
          .array(z.string())
          .optional()
          .describe(
            "AND-match filter on labels. A row is returned only if ALL given labels are present. Use lowercase strings matching `[a-z0-9:_-]+`; `e2a:*` system labels can be filtered even though setting them is server-only.",
          ),
        token: z.string().optional().describe("Pagination token from a previous response."),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      // The v1 client auto-paginates; `token` is accepted in the schema
      // for contract stability but unused — the pager walks cursors
      // internally up to `page_size` rows.
      runTool(async () => ({
        messages: await client.listMessages({
          ...(args.read_status !== undefined ? { readStatus: args.read_status } : {}),
          ...(args.page_size !== undefined ? { limit: args.page_size } : {}),
          ...(args.sort !== undefined ? { sort: args.sort } : {}),
          ...(args.from !== undefined ? { from: args.from } : {}),
          ...(args.subject_contains !== undefined
            ? { subjectContains: args.subject_contains }
            : {}),
          ...(args.conversation_id !== undefined
            ? { conversationId: args.conversation_id }
            : {}),
          ...(args.since !== undefined ? { since: args.since } : {}),
          ...(args.until !== undefined ? { until: args.until } : {}),
          ...(args.labels !== undefined ? { labels: args.labels } : {}),
          ...(args.email !== undefined ? { explicitAddress: args.email } : {}),
        }),
      })),
  );

  server.registerTool(
    "get_message",
    {
      title: "Get a message",
      description:
        "Use after `list_messages` to read one inbound message in full — body (text + html), headers, conversation id, and attachment metadata. Pass the `message_id` from the list response. Attachment bytes are NOT included (would blow context for any non-trivial PDF); the response lists each attachment's filename, content_type, and 0-based `index` plus size_bytes. To get the actual bytes of one attachment (inspect, forward, hand off), call `get_attachment` with that index. The raw MIME blob is also omitted for the same reason.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(async () => {
        // McpClient.getMessage resolves the address (explicit arg →
        // pinned default) and throws a directive error when neither is
        // available, so we don't pre-check here.
        const email = await client.getMessage(args.message_id, args.email);
        // Attachment metadata is decoded from the raw MIME (the v1
        // MessageView no longer exposes attachments inline). Bytes are
        // NOT returned here — call get_attachment for one. Omit
        // `raw_message` entirely so the LLM never sees the full MIME
        // blob unless it explicitly fetches an attachment.
        const attachments = await parseAttachments(email);
        return {
          message_id: email.messageId,
          conversation_id: email.conversationId,
          from: email._from,
          recipient: email.recipient,
          to: email.to,
          cc: email.cc,
          reply_to: email.replyTo,
          subject: email.subject,
          read_status: email.readStatus,
          // Inbound messages carry the decoded text in `parsed`; only outbound
          // held drafts populate `body` (mirror the CLI's read fallback).
          body_text: email.parsed?.text ?? email.body?.text,
          body_html: email.body?.html,
          received_at: email.createdAt,
          attachments: attachments.map((a, index) => ({
            index,
            filename: a.filename,
            content_type: a.contentType,
            size_bytes: a.size,
          })),
        };
      }),
  );

  // 2 MB hard cap on inline attachment-fetch. Bigger than the typical
  // PDF/image inline-share pattern this tool is designed for; small
  // enough that a single tool result stays under most LLM context
  // budgets even after base64 inflation. Files above this are an
  // anti-pattern for inline retrieval — the LLM should ask the user
  // / a host-side tool to handle them out of band.
  const MAX_INLINE_BYTES = 2 * 1024 * 1024;

  server.registerTool(
    "get_attachment",
    {
      title: "Fetch one attachment's bytes from an inbound message",
      description:
        "Returns the base64-encoded content of one attachment from an inbound message. Use this when you want to inspect, forward, or hand off an attachment surfaced by `get_message`. Indexes are 0-based and stable within a message (see `attachments[].index` from get_message). To forward to another recipient, pass the returned `{filename, content_type, data}` verbatim as an entry in `send_message`'s or `reply_to_message`'s `attachments[]` array. Refuses attachments larger than 2 MB after decoding — these are too big for inline retrieval and the LLM context cost would be prohibitive.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        attachment_index: z
          .number()
          .int()
          .min(0)
          .describe(
            "0-based index into the `attachments[]` returned by get_message. The index reflects the order attachments appear in the MIME message and is stable for a given message_id.",
          ),
        email: z
          .string()
          .optional()
          .describe(
            "Agent inbox holding the message. Omit to use the credential's bound agent (agent-scoped credentials).",
          ),
      }),
    },
    async (args) =>
      runTool(async () => {
        // McpClient.getMessage resolves + validates the address.
        const email = await client.getMessage(args.message_id, args.email);
        const list = await parseAttachments(email);
        if (args.attachment_index < 0 || args.attachment_index >= list.length) {
          throw new Error(
            `attachment_index ${args.attachment_index} out of range (message has ${list.length} attachment${list.length === 1 ? "" : "s"})`,
          );
        }
        const a = list[args.attachment_index]!;
        if (a.size > MAX_INLINE_BYTES) {
          throw new Error(
            `attachment too large for inline retrieval: ${a.size} bytes (max ${MAX_INLINE_BYTES}). Ask a host-side tool to write the raw_message MIME to disk and extract this attachment out of band.`,
          );
        }
        return {
          filename: a.filename,
          content_type: a.contentType,
          size_bytes: a.size,
          // Buffer → standard-alphabet base64. This matches the wire
          // shape send_message/reply_to_message expect on the way back
          // out, so a forward-attachment workflow is a verbatim copy.
          data: a.content.toString("base64"),
        };
      }),
  );
}
