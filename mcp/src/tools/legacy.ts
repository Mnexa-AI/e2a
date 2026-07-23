import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient, SendOpts } from "../client.js";
import { z } from "zod";
import { attachmentsArraySchema, type AttachmentInput } from "./attachments.js";
import { messageViewForTool } from "./messages.js";
import { runTool, strictInputSchema } from "./util.js";

function mapAttachments(
  attachments?: AttachmentInput[],
): Array<{ filename: string; contentType: string; data: string }> | undefined {
  if (attachments === undefined) return undefined;
  return attachments.map((attachment) => ({
    filename: attachment.filename,
    contentType: attachment.content_type,
    data: attachment.data,
  }));
}

type ReviewOverrides = {
  subject?: string;
  text?: string;
  html?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  attachments?: AttachmentInput[];
};

function mapReviewOverrides(overrides: ReviewOverrides) {
  return {
    ...(overrides.subject !== undefined ? { subject: overrides.subject } : {}),
    ...(overrides.text !== undefined ? { text: overrides.text } : {}),
    ...(overrides.html !== undefined ? { html: overrides.html } : {}),
    ...(overrides.to !== undefined ? { to: overrides.to } : {}),
    ...(overrides.cc !== undefined ? { cc: overrides.cc } : {}),
    ...(overrides.bcc !== undefined ? { bcc: overrides.bcc } : {}),
    ...(mapAttachments(overrides.attachments) !== undefined
      ? { attachments: mapAttachments(overrides.attachments) }
      : {}),
  };
}

/**
 * Register frozen v1 compatibility names. These adapters intentionally retain
 * their historical schemas; new callers should use the canonical tools named
 * in each description.
 */
export function registerLegacyTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "send_email",
    {
      title: "Deprecated alias: send_message",
      annotations: { destructiveHint: false },
      description:
        "DEPRECATED: use `send_message`. This compatibility alias preserves the historical body/html_body/agent_email inputs for cached MCP clients.",
      inputSchema: strictInputSchema({
        to: z.array(z.string()).describe("Recipient email addresses (one or more)."),
        subject: z.string(),
        body: z.string().describe("Plain-text body. Use html_body for HTML."),
        html_body: z.string().optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        conversation_id: z.string().optional(),
        idempotency_key: z.string().optional(),
        agent_email: z.string().optional(),
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
            text: args.body,
            ...(args.html_body !== undefined ? { html: args.html_body } : {}),
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
          args.agent_email,
        );
      }),
  );

  server.registerTool(
    "get_attachment_data",
    {
      title: "Deprecated alias: get_attachment",
      annotations: { readOnlyHint: true },
      description:
        "DEPRECATED: use `get_attachment` with inline:true. This compatibility alias preserves attachment_index/agent_email inputs and the historical inline base64 response. The current 256 KB inline limit applies.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        attachment_index: z.number().int().min(0),
        agent_email: z.string().optional(),
      }),
    },
    async (args) =>
      runTool(async () => {
        const attachment = await client.getAttachment(
          args.message_id,
          args.attachment_index,
          { inline: true },
          args.agent_email,
        );
        if (!attachment.data) {
          throw new Error("inline attachment response omitted base64 data");
        }
        return {
          filename: attachment.filename,
          content_type: attachment.contentType,
          size_bytes: attachment.sizeBytes,
          data: attachment.data,
        };
      }),
  );

  server.registerTool(
    "list_pending_messages",
    {
      title: "Deprecated alias: list_reviews",
      annotations: { readOnlyHint: true },
      description:
        "DEPRECATED: use `list_reviews`. This account-only compatibility alias walks the unified review queue and preserves the historical messages response envelope.",
      inputSchema: strictInputSchema({}),
    },
    async () =>
      runTool(async () => {
        const messages: unknown[] = [];
        let cursor: string | undefined;
        do {
          const page = await client.listReviews({
            ...(cursor !== undefined ? { cursor } : {}),
            limit: 100,
          });
          messages.push(...page.items);
          cursor = page.next_cursor ?? undefined;
        } while (cursor !== undefined);
        return { messages };
      }),
  );

  server.registerTool(
    "get_pending_message",
    {
      title: "Deprecated alias: get_review",
      annotations: { readOnlyHint: true },
      description:
        "DEPRECATED: use `get_review`. This account-only compatibility alias accepts the historical pending message ID and returns canonical review detail.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
      }),
    },
    // Same context-safe projection as get_review: raw_message and attachment
    // bytes stay out of the model's context.
    async (args) =>
      runTool(async () => messageViewForTool(await client.getReview(args.message_id))),
  );

  server.registerTool(
    "approve_pending_message",
    {
      title: "Deprecated alias: approve_review",
      annotations: { destructiveHint: false },
      description:
        "DEPRECATED: use `approve_review`. This account-only compatibility alias preserves the historical body_text/body_html reviewer override fields.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        subject: z.string().optional(),
        body_text: z.string().optional(),
        body_html: z.string().optional(),
        to: z.array(z.string()).optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        idempotency_key: z.string().optional(),
      }),
    },
    async (args) => {
      const { message_id, idempotency_key, body_text, body_html, ...rest } = args;
      const overrides = mapReviewOverrides({
        ...rest,
        ...(body_text !== undefined ? { text: body_text } : {}),
        ...(body_html !== undefined ? { html: body_html } : {}),
      });
      return runTool(() =>
        idempotency_key !== undefined
          ? client.approveReview(message_id, overrides, { idempotencyKey: idempotency_key })
          : client.approveReview(message_id, overrides),
      );
    },
  );

  server.registerTool(
    "approve_message",
    {
      title: "Deprecated alias: approve_review",
      annotations: { destructiveHint: false },
      description:
        "DEPRECATED: use `approve_review`. This account-only compatibility alias preserves the previous approve_message name and override fields.",
      inputSchema: strictInputSchema({
        message_id: z.string(),
        subject: z.string().optional(),
        text: z.string().optional(),
        html: z.string().optional(),
        to: z.array(z.string()).optional(),
        cc: z.array(z.string()).optional(),
        bcc: z.array(z.string()).optional(),
        attachments: attachmentsArraySchema,
        idempotency_key: z.string().optional(),
      }),
    },
    async (args) => {
      const { message_id, idempotency_key, ...rest } = args;
      const overrides = mapReviewOverrides(rest);
      return runTool(() =>
        idempotency_key !== undefined
          ? client.approveReview(message_id, overrides, { idempotencyKey: idempotency_key })
          : client.approveReview(message_id, overrides),
      );
    },
  );

  for (const [name, replacement] of [
    ["reject_pending_message", "reject_review"],
    ["reject_message", "reject_review"],
  ] as const) {
    server.registerTool(
      name,
      {
        title: `Deprecated alias: ${replacement}`,
        annotations: { destructiveHint: true },
        description: `DEPRECATED: use \`${replacement}\`. This account-only compatibility alias preserves message_id and the optional rejection reason.`,
        inputSchema: strictInputSchema({
          message_id: z.string(),
          reason: z.string().optional(),
        }),
      },
      async (args) => runTool(() => client.rejectReview(args.message_id, args.reason)),
    );
  }
}
