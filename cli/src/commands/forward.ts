import { createClient } from "../sdk.js";

export async function forward(
  messageId: string | undefined,
  opts: {
    to: string[];
    cc?: string[];
    bcc?: string[];
    body?: string;
    htmlBody?: string;
    from?: string;
    idempotencyKey?: string;
  },
): Promise<void> {
  if (!messageId) {
    process.stderr.write(
      "Usage: e2a forward <message-id> --to <addr> [--cc <addr>] [--bcc <addr>] [--body \"...\"] [--html-body \"...\"]\n",
    );
    process.exit(1);
  }
  if (!opts.to.length) {
    process.stderr.write("--to is required (at least one recipient)\n");
    process.exit(1);
  }

  const client = createClient({ from: opts.from });

  if (!client.agentEmail) {
    process.stderr.write(
      "No agent email configured. Run 'e2a register' first or use --agent.\n",
    );
    process.exit(1);
  }

  const res = await client.forward(messageId, opts.to, {
    cc: opts.cc?.length ? opts.cc : undefined,
    bcc: opts.bcc?.length ? opts.bcc : undefined,
    body: opts.body,
    htmlBody: opts.htmlBody,
    idempotencyKey: opts.idempotencyKey,
  });

  process.stdout.write(`Sent: ${res.message_id}\n`);
}
