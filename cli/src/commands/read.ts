import { createClient, requireAgentEmail } from "../sdk.js";

export async function read(messageId: string | undefined, from: string | undefined): Promise<void> {
  if (!messageId) {
    process.stderr.write("Usage: e2a read <message-id>\n");
    process.exit(1);
  }

  const client = createClient({ from });
  const address = requireAgentEmail(from);

  // messages.get returns a MessageView (the bearer token already
  // authenticated the channel) — no `verifySignature` step needed, no
  // second roundtrip for the raw detail.
  const msg = await client.messages.get(address, messageId);

  const recipient = msg.recipient ?? "";
  const to = msg.to ?? [];
  const cc = msg.cc ?? [];
  const replyTo = msg.replyTo ?? [];

  process.stdout.write(`Message ID: ${msg.messageId}\n`);
  process.stdout.write(`From: ${msg._from}\n`);
  process.stdout.write(`To: ${recipient}\n`);
  // Other addresses from the original To: header (the message may have been
  // addressed to several agents at once; this row is one of those fan-outs).
  const recipientLower = recipient.toLowerCase();
  const otherTo = to.filter((a) => a.toLowerCase() !== recipientLower);
  if (otherTo.length > 0) {
    process.stdout.write(`Also-To: ${otherTo.join(", ")}\n`);
  }
  if (cc.length > 0) {
    process.stdout.write(`Cc: ${cc.join(", ")}\n`);
  }
  if (replyTo.length > 0) {
    process.stdout.write(`Reply-To: ${replyTo.join(", ")}\n`);
  }
  const received = msg.createdAt instanceof Date ? msg.createdAt.toISOString() : (msg.createdAt ?? null);
  process.stdout.write(`Date: ${received ?? "unknown"}\n`);
  process.stdout.write(`Subject: ${msg.subject}\n`);
  process.stdout.write("\n");

  // Prefer the parsed (best-effort plain-text) body, falling back to the
  // raw text body the server extracted.
  const textBody = msg.parsed?.text || msg.body?.text;
  if (textBody) {
    process.stdout.write(textBody + "\n");
  }
}
