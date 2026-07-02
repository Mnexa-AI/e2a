import { readFileSync } from "node:fs";
import type { SendResultView } from "@e2a/sdk/v1";
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
  agent?: string;
  json?: boolean;
}

export interface ReplyOptions {
  body?: string;
  bodyFile?: string;
  htmlFile?: string;
  agent?: string;
  json?: boolean;
}

const SEND_USAGE =
  "usage: e2a send --to <email> --subject <s> (--body <text> | --body-file <f> | --html-file <f>) [--conversation-id <id>] [--agent <inbox>] [--json]";
const REPLY_USAGE =
  "usage: e2a reply <message-id> (--body <text> | --body-file <f> | --html-file <f>) [--agent <inbox>] [--json]";

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
  if (!body) fail(EXIT.USAGE, usage);
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
  if (result.status === "pending_review") {
    process.stderr.write(
      "WARNING: held for review (pending_review) — the message did NOT reach the recipient. " +
        "Disable outbound protection on this agent or approve it in the review queue.\n",
    );
    process.exit(EXIT.HELD);
  }
}

export async function send(opts: SendOptions): Promise<void> {
  if (opts.to.length === 0 || !opts.subject) fail(EXIT.USAGE, SEND_USAGE);
  const { body, htmlBody } = resolveBodies(opts, SEND_USAGE);

  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);
  const result = await client.messages.send(agentEmail, {
    to: opts.to,
    subject: opts.subject,
    body,
    ...(htmlBody !== undefined ? { htmlBody } : {}),
    ...(opts.conversationId ? { conversationId: opts.conversationId } : {}),
  });
  emitSendResult(result, opts.json);
}

export async function reply(messageId: string | undefined, opts: ReplyOptions): Promise<void> {
  if (!messageId) fail(EXIT.USAGE, REPLY_USAGE);
  const { body, htmlBody } = resolveBodies(opts, REPLY_USAGE);

  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);
  const result = await client.messages.reply(agentEmail, messageId, {
    body,
    ...(htmlBody !== undefined ? { htmlBody } : {}),
  });
  emitSendResult(result, opts.json);
}
