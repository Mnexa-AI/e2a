import { readFileSync } from "node:fs";
import { basename, extname } from "node:path";
import type { SendResultView, Attachment } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";
import { EXIT, fail } from "../exit.js";
import { htmlToText } from "../html.js";

export interface SendOptions {
  to: string[];
  subject?: string;
  body?: string;
  bodyFile?: string;
  htmlFile?: string;
  conversationId?: string;
  replyTo?: string;
  agent?: string;
  json?: boolean;
  idempotencyKey?: string;
  attach?: string[];
}

export interface ReplyOptions {
  body?: string;
  bodyFile?: string;
  htmlFile?: string;
  replyTo?: string;
  agent?: string;
  json?: boolean;
  idempotencyKey?: string;
  attach?: string[];
}

const MIME_BY_EXT: Record<string, string> = {
  ".pdf": "application/pdf",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".txt": "text/plain",
  ".md": "text/markdown",
  ".html": "text/html",
  ".json": "application/json",
  ".csv": "text/csv",
  ".zip": "application/zip",
};

function readAttachments(paths: string[] | undefined): Attachment[] | undefined {
  if (!paths || paths.length === 0) return undefined;
  return paths.map((p) => {
    let buf: Buffer;
    try {
      buf = readFileSync(p);
    } catch {
      return fail(EXIT.USAGE, `--attach file not found or unreadable: ${p}`);
    }
    return {
      filename: basename(p),
      contentType: MIME_BY_EXT[extname(p).toLowerCase()] ?? "application/octet-stream",
      data: buf.toString("base64"),
    };
  });
}

const SEND_USAGE =
  "usage: e2a send --to <email> --subject <s> (--body <text> | --body-file <f> | --html-file <f>) [--conversation-id <id>] [--reply-to <email>] [--agent <inbox>] [--json]";
const REPLY_USAGE =
  "usage: e2a reply <message-id> (--body <text> | --body-file <f> | --html-file <f>) [--reply-to <email>] [--agent <inbox>] [--json]";

function readFileOrUsage(path: string, flag: string): string {
  try {
    return readFileSync(path, "utf-8");
  } catch {
    return fail(EXIT.USAGE, `${flag} file not found or unreadable: ${path}`);
  }
}

/**
 * Resolve the text body + optional HTML body from the flag combination.
 * `--html-file` without an explicit text body derives a plain-text fallback
 * from the HTML, so multipart sends never ship an empty text alternative.
 */
export function resolveBodies(
  opts: { body?: string; bodyFile?: string; htmlFile?: string },
  usage: string,
): { body: string; htmlBody?: string } {
  const htmlBody = opts.htmlFile ? readFileOrUsage(opts.htmlFile, "--html-file") : undefined;
  let body = opts.body ?? (opts.bodyFile ? readFileOrUsage(opts.bodyFile, "--body-file") : undefined);
  if (body === undefined && htmlBody !== undefined) body = htmlToText(htmlBody);
  // Only "no body source at all" is a usage error. An empty string is legal:
  // markup-only HTML (images/tables, no text nodes) derives "" and must still
  // send — the HTML part is the real content.
  if (body === undefined) fail(EXIT.USAGE, usage);
  return { body, htmlBody };
}

/**
 * Print the send result and enforce the held contract: a held send is an HTTP
 * success with `status: "pending_review"` — the recipient got NOTHING. Scripts
 * must be able to branch on that without parsing JSON, hence exit HELD (3).
 */
function emitSendResult(result: SendResultView, json?: boolean): void {
  if (json) {
    process.stdout.write(JSON.stringify(result) + "\n");
  } else {
    process.stdout.write(result.messageId + "\n");
  }
  // `status` is an OPEN SET per the spec ("tolerate unknown values") — so the
  // delivered check is `=== "sent"`, inverted. A future held-variant status
  // must fail loud (exit HELD), never slip through as exit 0: an unknown
  // outcome is "not known to be delivered".
  if (result.status !== "sent") {
    process.stderr.write(
      `WARNING: send returned status "${result.status}" — the message did NOT go out now. ` +
        "pending_review means it is held for approval: disable outbound protection on this " +
        "agent or approve it in the review queue.\n",
    );
    // exitCode + return, NOT process.exit(): a hard exit can truncate piped
    // stdout before the message id above flushes, and scripts need that id to
    // approve the held message.
    process.exitCode = EXIT.HELD;
  }
}

export async function send(opts: SendOptions): Promise<void> {
  if (opts.to.length === 0 || !opts.subject) fail(EXIT.USAGE, SEND_USAGE);
  const { body, htmlBody } = resolveBodies(opts, SEND_USAGE);

  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);
  // Optional fields may be undefined — the generated serializer drops
  // undefined-valued keys before they reach the wire. An explicit
  // --idempotency-key makes a *re-invocation* after an ambiguous failure
  // dedupe server-side (the SDK's auto-minted key only survives in-process
  // retries).
  const result = await client.messages.send(
    agentEmail,
    {
      to: opts.to,
      subject: opts.subject,
      body,
      htmlBody,
      conversationId: opts.conversationId,
      replyTo: opts.replyTo,
      attachments: readAttachments(opts.attach),
    },
    opts.idempotencyKey ? { idempotencyKey: opts.idempotencyKey } : undefined,
  );
  emitSendResult(result, opts.json);
}

export async function reply(messageId: string | undefined, opts: ReplyOptions): Promise<void> {
  if (!messageId) fail(EXIT.USAGE, REPLY_USAGE);
  const { body, htmlBody } = resolveBodies(opts, REPLY_USAGE);

  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);
  const result = await client.messages.reply(
    agentEmail,
    messageId,
    { body, htmlBody, replyTo: opts.replyTo, attachments: readAttachments(opts.attach) },
    opts.idempotencyKey ? { idempotencyKey: opts.idempotencyKey } : undefined,
  );
  emitSendResult(result, opts.json);
}
