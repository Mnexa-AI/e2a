import { createClient } from "../sdk.js";

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

  if (!client.agentEmail) {
    process.stderr.write(
      "No agent email configured. Run 'e2a register' first or use --agent.\n",
    );
    process.exit(1);
  }

  const res = await client.reply(messageId, body, {
    htmlBody: opts.htmlBody,
    replyAll: opts.replyAll,
    cc: opts.cc?.length ? opts.cc : undefined,
    bcc: opts.bcc?.length ? opts.bcc : undefined,
    idempotencyKey: opts.idempotencyKey,
  });

  process.stdout.write(`Sent: ${res.message_id}\n`);
}
