import type { ReplyRequest, RequestOptions } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

export async function reply(
  messageId: string | undefined,
  body: string,
  opts: {
    htmlBody?: string;
    replyAll?: boolean;
    cc?: string[];
    bcc?: string[];
    from?: string;
    idempotencyKey?: string;
  },
): Promise<void> {
  if (!messageId) {
    process.stderr.write(
      "Usage: e2a reply <message-id> --body \"...\" [--html-body \"...\"] [--reply-all] [--cc <addr>] [--bcc <addr>]\n",
    );
    process.exit(1);
  }
  if (!body) {
    process.stderr.write("--body is required\n");
    process.exit(1);
  }

  const client = createClient({ from: opts.from });
  const address = requireAgentEmail(opts.from);

  const reqBody: ReplyRequest = {
    body,
    htmlBody: opts.htmlBody,
    replyAll: opts.replyAll,
    cc: opts.cc?.length ? opts.cc : undefined,
    bcc: opts.bcc?.length ? opts.bcc : undefined,
  };
  const reqOpts: RequestOptions | undefined = opts.idempotencyKey
    ? { idempotencyKey: opts.idempotencyKey }
    : undefined;

  const res = await client.messages.reply(address, messageId, reqBody, reqOpts);

  process.stdout.write(`Sent: ${res.messageId}\n`);
}
