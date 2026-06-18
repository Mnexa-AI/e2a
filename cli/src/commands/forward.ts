import type { ForwardRequest, RequestOptions } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

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
  const address = requireAgentEmail(opts.from);

  const reqBody: ForwardRequest = {
    to: opts.to,
    cc: opts.cc?.length ? opts.cc : undefined,
    bcc: opts.bcc?.length ? opts.bcc : undefined,
    body: opts.body,
    htmlBody: opts.htmlBody,
  };
  const reqOpts: RequestOptions | undefined = opts.idempotencyKey
    ? { idempotencyKey: opts.idempotencyKey }
    : undefined;

  const res = await client.messages.forward(address, messageId, reqBody, reqOpts);

  process.stdout.write(`Sent: ${res.messageId}\n`);
}
