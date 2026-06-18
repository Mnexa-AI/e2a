import type { SendEmailRequest, RequestOptions } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

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
  const address = requireAgentEmail(opts.from);

  const reqBody: SendEmailRequest = {
    to: to.length ? to : undefined,
    subject,
    body,
    htmlBody: opts.htmlBody,
    cc: opts.cc?.length ? opts.cc : undefined,
    bcc: opts.bcc?.length ? opts.bcc : undefined,
  };
  const reqOpts: RequestOptions | undefined = opts.idempotencyKey
    ? { idempotencyKey: opts.idempotencyKey }
    : undefined;

  const res = await client.messages.send(address, reqBody, reqOpts);

  process.stdout.write(`Sent: ${res.messageId}\n`);
}
