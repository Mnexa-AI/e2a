import { createClient } from "../sdk.js";

export async function send(
  to: string[],
  subject: string,
  body: string,
  opts: {
    htmlBody?: string;
    cc?: string[];
    bcc?: string[];
    from?: string;
    idempotencyKey?: string;
  },
): Promise<void> {
  const hasVisibleRecipient = to.length > 0 || (opts.cc?.length ?? 0) > 0;
  if (!hasVisibleRecipient || !subject || !body) {
    process.stderr.write(
      "Usage: e2a send [--to <addr>] [--cc <addr>] --subject \"...\" --body \"...\"\n  At least one --to or --cc is required.\n",
    );
    process.exit(1);
  }

  const client = createClient({ from: opts.from });

  if (!client.agentEmail) {
    process.stderr.write(
      "No agent email configured. Run 'e2a register' first or use --agent.\n",
    );
    process.exit(1);
  }

  const res = await client.send(to, subject, body, {
    htmlBody: opts.htmlBody,
    cc: opts.cc?.length ? opts.cc : undefined,
    bcc: opts.bcc?.length ? opts.bcc : undefined,
    idempotencyKey: opts.idempotencyKey,
  });

  process.stdout.write(`Sent: ${res.message_id}\n`);
}
