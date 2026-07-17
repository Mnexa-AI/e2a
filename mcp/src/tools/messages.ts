import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient, SendOpts } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema, paginationInput } from "./util.js";
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

export function registerMessageTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "send_message",
    {
      title: "Send email",
      annotations: { destructiveHint: false },
      description:
        "Use when starting a NEW email thread to a fresh recipient. To respond to a message you can see in `list_messages`, use `reply_to_message` instead — it preserves the In-Reply-To / References headers so the reply lands in the same thread, which this tool deliberately does not do. Attach files via `attachments`; pass base64 strings produced by other tools (e.g. `get_attachment`) verbatim — don't hand-encode raw text. **`accepted` and `pending_review` are both success, not failure — do NOT re-send.** `{ status: \"accepted\", message_id: ... }` means the send was durably persisted and queued for submission (async pipeline); it is on its way. `{ status: \"pending_review\", message_id: ... }` means a human review hold caught it first. Either way, re-calling this tool (especially without reusing the same `idempotency_key`) risks a real second send — the terminal outcome (delivered or failed) arrives later via webhook events (`email.sent` / `email.failed`) or by polling `get_message`/`list_messages`, not by retrying. **Templates (beta):** instead of literal subject/text, reference a stored template with `template_id` XOR `template_alias` plus `template_data` — a template reference is mutually exclusive with subject/text/html (pass neither literal field). The server renders before any review hold, so a reviewer sees final content. Missing variables render as empty strings (no error) — validate data against the template's variables first. Only send supports templates; reply/forward do not.",
      inputSchema: strictInputSchema({
        to: z.array(z.string()).describe("Recipient email addresses (one or more)."),
        subject: z.string().optional().describe("Literal subject. Required unless a template reference is used (then it must be omitted)."),
        text: z.string().optional().describe("Literal plain-text body; use `html` for HTML. Required unless a template reference is used (then it must be omitted)."),
        html: z.string().optional(),
        template_id: z
          .string()
          .optional()
          .describe(
            "Send using a stored template by id (tmpl_…), rendered server-side. Mutually exclusive with template_alias and with literal subject/text/html. Beta.",
          ),
        template_alias: z
          .string()
          .optional()
          .describe(
            "Send using a stored template by its per-user alias (see `list_templates`). Mutually exclusive with template_id and with literal subject/text/html. Beta.",
          ),
        template_data: z
          .record(z.string(), z.unknown())
          .optional()
          .describe(
            "Variables for the referenced template ({{name}}, dot paths into nested objects). Missing variables render as EMPTY strings — no error. For raw {{{…_html}}} fragment slots, HTML-escape any user content you splice in. Requires template_id or template_alias. Beta.",
          ),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z
          .string()
          .optional()
          .describe("Optional conversation grouping ID. Server generates one if omitted."),
        reply_to: z
          .string()
          .optional()
          .describe(
            "Sets the Reply-To header — where replies to this message are directed. A single address, optionally with a display name (e.g. \"Support <support@acme.com>\"). Defaults to the sending agent's own address.",
          ),
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
            // subject/body are optional on the wire when a template reference
            // is used — only forward what the caller passed so the server's
            // mutual-exclusivity check sees the true shape.
            ...(args.subject !== undefined ? { subject: args.subject } : {}),
            ...(args.text !== undefined ? { text: args.text } : {}),
            ...(args.html !== undefined ? { html: args.html } : {}),
            ...(args.template_id !== undefined ? { templateId: args.template_id } : {}),
            ...(args.template_alias !== undefined ? { templateAlias: args.template_alias } : {}),
            ...(args.template_data !== undefined ? { templateData: args.template_data } : {}),
            ...(args.cc !== undefined ? { cc: args.cc } : {}),
            ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
            ...(mapAttachments(args.attachments) !== undefined
              ? { attachments: mapAttachments(args.attachments) }
              : {}),
            ...(args.conversation_id !== undefined
              ? { conversationId: args.conversation_id }
              : {}),
            ...(args.reply_to !== undefined ? { replyTo: args.reply_to } : {}),
          },
          opts,
          args.email,
        );
      }),
  );

  server.registerTool(
    "reply_to_message",
    {
      title: "Reply to a message",
      annotations: { destructiveHint: false },
      description:
        "Use whenever you're responding to a message you can see — preserves the In-Reply-To and References headers so the reply joins the original email thread instead of starting a new one. Works on both a message the agent RECEIVED (replies to its sender) and a message the agent SENT (continues the thread to its original recipients, i.e. a Gmail-style follow-up on your own message). Prefer this over `send_message` for any in-thread response; thread fragmentation (broken conversation view in the recipient's mail client) is the most visible symptom of using `send_message` by mistake. Pass `reply_all: true` to copy the original Cc list; subject is auto-derived as `Re: …` by the server. Same review caveat as `send_message`: **`accepted` and `pending_review` are both success, not failure — do NOT re-send.** `accepted` means the reply was durably persisted and queued for submission (async pipeline); `pending_review` means a human review hold caught it first. The terminal outcome arrives later via webhook events (`email.sent` / `email.failed`) or by polling `get_message`, not by retrying.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("ID of the message to reply to — inbound or one the agent sent (e.g. msg_…)."),
        text: z.string().describe("Plain-text reply body."),
        html: z.string().optional(),
        reply_all: z
          .boolean()
          .optional()
          .describe("If true, copy the original message's Cc list."),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z.string().optional(),
        reply_to: z
          .string()
          .optional()
          .describe(
            "Sets the Reply-To header — where replies to this message are directed. A single address, optionally with a display name. Defaults to the sending agent's own address.",
          ),
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
            text: args.text,
            ...(args.html !== undefined ? { html: args.html } : {}),
            ...(args.reply_all !== undefined ? { replyAll: args.reply_all } : {}),
            ...(args.cc !== undefined ? { cc: args.cc } : {}),
            ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
            ...(mapAttachments(args.attachments) !== undefined
              ? { attachments: mapAttachments(args.attachments) }
              : {}),
            ...(args.conversation_id !== undefined
              ? { conversationId: args.conversation_id }
              : {}),
            ...(args.reply_to !== undefined ? { replyTo: args.reply_to } : {}),
          },
          opts,
          args.email,
        );
      }),
  );

  server.registerTool(
    "forward_message",
    {
      title: "Forward a message",
      annotations: { destructiveHint: false },
      description:
        "Forward a message the agent has received OR one it sent to one or more new recipients. The server auto-prepends a Gmail-style header block (From/Date/Subject/To/Cc) and the original body to whatever optional comment you pass in `text`/`html`, **and carries over the original message's attachments by default** — you do NOT need to re-fetch them via `get_attachment`. Anything you pass in `attachments[]` is added on top of the originals. **Unlike `reply_to_message`, a forward is a NEW thread** — no In-Reply-To / References headers are emitted, so the recipient sees a fresh conversation. Use this when the user asks to share an email with someone else; use `reply_to_message` when continuing the existing conversation. Same review behavior as send/reply: **`accepted` and `pending_review` are both success, not failure — do NOT re-send.** `accepted` means the forward was durably persisted and queued for submission (async pipeline); `pending_review` means a human review hold caught it first. The terminal outcome arrives later via webhook events (`email.sent` / `email.failed`) or by polling `get_message`, not by retrying.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("ID of the message to forward — inbound or one the agent sent (e.g. msg_…)."),
        to: z.array(z.string()).describe("Forward target addresses (one or more)."),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        text: z
          .string()
          .optional()
          .describe(
            "Optional plain-text comment to prepend above the forwarded content. The original body is appended automatically.",
          ),
        html: z.string().optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z
          .string()
          .optional()
          .describe(
            "Optional conversation grouping ID. A forward is a new thread by default — set this only to bind it to an existing thread explicitly.",
          ),
        reply_to: z
          .string()
          .optional()
          .describe(
            "Sets the Reply-To header — where replies to the forward are directed. A single address, optionally with a display name. Defaults to the sending agent's own address.",
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
            // text is required on the wire (MSG-3); the original is auto-quoted,
            // so an empty comment is fine — default to "".
            text: args.text ?? "",
            ...(args.html !== undefined ? { html: args.html } : {}),
            ...(args.cc !== undefined ? { cc: args.cc } : {}),
            ...(args.bcc !== undefined ? { bcc: args.bcc } : {}),
            ...(mapAttachments(args.attachments) !== undefined
              ? { attachments: mapAttachments(args.attachments) }
              : {}),
            ...(args.conversation_id !== undefined
              ? { conversationId: args.conversation_id }
              : {}),
            ...(args.reply_to !== undefined ? { replyTo: args.reply_to } : {}),
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
      annotations: { idempotentHint: true, destructiveHint: false },
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
      annotations: { readOnlyHint: true },
      description:
        "Lists the agent's conversations — groups of messages sharing a `conversation_id` — one row per conversation, sorted by most recent activity. Each row carries `message_count`, `inbound_count`, `outbound_count`, `has_unread`, and the latest message's subject + sender so you can render an inbox without drilling into each thread. **Cursor-paginated:** returns one page in `conversations` plus a `next_cursor` when more remain — pass it back as `cursor` for the next page. To read a single conversation's messages, call `get_conversation`.",
      inputSchema: strictInputSchema({
        ...paginationInput,
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
      runTool(async () => {
        const page = await client.listConversations(
          {
            ...(args.cursor !== undefined ? { cursor: args.cursor } : {}),
            ...(args.limit !== undefined ? { limit: args.limit } : {}),
            ...(args.since !== undefined ? { since: args.since } : {}),
            ...(args.until !== undefined ? { until: args.until } : {}),
          },
          args.email,
        );
        return { conversations: page.items, ...(page.next_cursor ? { next_cursor: page.next_cursor } : {}) };
      }),
  );

  server.registerTool(
    "get_conversation",
    {
      title: "Get a single conversation with all member messages",
      annotations: { readOnlyHint: true },
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
      title: "List messages (inbox or sent)",
      annotations: { readOnlyHint: true },
      description:
        "List the agent's messages, newest first by default. Use `direction` to pick the folder: `inbound` (the Inbox — received mail, the default), `outbound` (the Sent folder — mail this agent sent, including held drafts), or `all` (both). Filter received mail by `read_status` (unread/read/all; default unread — applies to inbound only; sent mail has no read-state). **Cursor-paginated:** returns one page in `messages` plus a `next_cursor` when more remain — pass it back as `cursor` for the next page (keep the same filters + sort). Pass `sort: \"asc\"` for FIFO order (oldest first) to drain in arrival order. **Search filters** (`from_`, `subject_contains`, `conversation_id`, `since`, `until`) narrow server-side — use them instead of paging the whole folder. Returns summaries only — use `get_message` for the full body.",
      inputSchema: strictInputSchema({
        direction: z
          .enum(["inbound", "outbound", "all"])
          .optional()
          .describe(
            "Which folder to list: `inbound` = Inbox (received, default), `outbound` = Sent (this agent's sent mail + held drafts), `all` = both.",
          ),
        read_status: z.enum(["unread", "read", "all"]).optional(),
        ...paginationInput,
        sort: z
          .enum(["asc", "desc"])
          .optional()
          .describe(
            "Sort order by created_at. Defaults to `desc` (newest first). Pass `asc` for FIFO polling — drain the inbox in arrival order. Switching sort mid-pagination rejects the existing cursor.",
          ),
        from_: z
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
        deleted: z
          .boolean()
          .optional()
          .describe("List the message trash instead of live messages."),
        email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(async () => {
        const page = await client.listMessages({
          ...(args.direction !== undefined ? { direction: args.direction } : {}),
          ...(args.read_status !== undefined ? { readStatus: args.read_status } : {}),
          ...(args.cursor !== undefined ? { cursor: args.cursor } : {}),
          ...(args.limit !== undefined ? { limit: args.limit } : {}),
          ...(args.sort !== undefined ? { sort: args.sort } : {}),
          ...(args.from_ !== undefined ? { from_: args.from_ } : {}),
          ...(args.subject_contains !== undefined
            ? { subjectContains: args.subject_contains }
            : {}),
          ...(args.conversation_id !== undefined
            ? { conversationId: args.conversation_id }
            : {}),
          ...(args.since !== undefined ? { since: args.since } : {}),
          ...(args.until !== undefined ? { until: args.until } : {}),
          ...(args.labels !== undefined ? { labels: args.labels } : {}),
          ...(args.deleted !== undefined ? { deleted: args.deleted } : {}),
          ...(args.email !== undefined ? { explicitAddress: args.email } : {}),
        });
        return { messages: page.items, ...(page.next_cursor ? { next_cursor: page.next_cursor } : {}) };
      }),
  );

  server.registerTool(
    "restore_message",
    {
      title: "Restore a message from trash",
      annotations: { destructiveHint: false, idempotentHint: false },
      description:
        "Restore a soft-deleted message before its trash-retention window expires. Time spent in trash does not consume the message's normal retention. Returns the restored message; a live message returns `not_in_trash`.",
      inputSchema: strictInputSchema({
        message_id: z.string().describe("ID of the trashed message to restore."),
        email: z.string().optional().describe("Owning agent; defaults to the bound agent."),
      }),
    },
    async (args) => runTool(() => client.restoreMessage(args.message_id, args.email)),
  );

  server.registerTool(
    "get_message",
    {
      title: "Get a message",
      annotations: { readOnlyHint: true },
      description:
        "Use after `list_messages` to read one inbound message in full — body (text + html), headers, conversation id, and attachment metadata. Pass the message's `id` from the list response. Attachment bytes are NOT included (would blow context for any non-trivial PDF); the response lists each attachment's filename, content_type, and 0-based `index` plus size_bytes. To get the actual bytes of one attachment (inspect, forward, hand off), call `get_attachment` with that index. The raw MIME blob is also omitted for the same reason.",
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
        // Attachment metadata comes from the server (MessageView.attachments,
        // parsed server-side) — the authoritative, stable index the download
        // route also uses. Bytes are NOT returned here; call get_attachment for
        // one. `raw_message` is omitted so the LLM never sees the full MIME blob.
        return {
          id: email.id,
          conversation_id: email.conversationId,
          from_: email.from_,
          delivered_to: email.deliveredTo,
          to: email.to,
          cc: email.cc,
          reply_to: email.replyTo,
          subject: email.subject,
          read_status: email.readStatus,
          // Inbound messages carry the decoded text in `parsed`; only outbound
          // held drafts populate `body` (mirror the CLI's read fallback).
          text: email.parsed?.text ?? email.body?.text,
          html: email.body?.html,
          received_at: email.createdAt,
          attachments: (email.attachments ?? []).map((a) => ({
            index: a.index,
            filename: a.filename,
            content_type: a.contentType,
            size_bytes: a.sizeBytes,
          })),
        };
      }),
  );

  server.registerTool(
    "get_attachment",
    {
      title: "Get one attachment (download URL; bytes by reference)",
      annotations: { readOnlyHint: true },
      description:
        "Returns one attachment's metadata plus a short-lived `download_url` (+ `expires_at`) — fetch the bytes out of band so binary content never streams through your context (no size limit). `attachment_index` is the 0-based `attachments[].index` from `get_message`. Pass `inline: true` to ALSO get base64 `data` for small attachments (≤256 KB; larger inline requests error) — use this only when you must re-attach the bytes (e.g. forwarding a small file via `send_message`'s `attachments[]`); otherwise hand the `download_url` to whatever needs the file.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        attachment_index: z
          .number()
          .int()
          .min(0)
          .describe(
            "0-based index from `get_message`'s `attachments[].index` (stable for a given message_id).",
          ),
        inline: z
          .boolean()
          .optional()
          .describe(
            "When true, also include the bytes as base64 `data` — ONLY for attachments ≤256 KB (larger requests error). Default false: use `download_url`.",
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
        // Server-side: mints the signed URL + (optionally) inlines small bytes;
        // index-out-of-range (404) and inline-too-large (413) surface as the
        // structured error code. No client-side MIME re-parse or size wall.
        const att = await client.getAttachment(
          args.message_id,
          args.attachment_index,
          args.inline ? { inline: true } : {},
          args.email,
        );
        return {
          index: att.index,
          filename: att.filename,
          content_type: att.contentType,
          size_bytes: att.sizeBytes,
          download_url: att.downloadUrl,
          expires_at: att.expiresAt,
          ...(att.data ? { data: att.data } : {}),
        };
      }),
  );
}
