import { createClient } from "../sdk.js";

export async function read(messageId: string | undefined, from: string | undefined): Promise<void> {
  if (!messageId) {
    process.stderr.write("Usage: e2a read <message-id>\n");
    process.exit(1);
  }

  const client = createClient({ from });

  if (!client.agentEmail) {
    process.stderr.write("No agent email. Set one with: e2a config set agent_email <email>\n");
    process.exit(1);
  }

  // getMessage returns a pre-verified InboundEmail (the bearer token
  // already authenticated the channel) — no `verifySignature` step
  // needed, no second roundtrip for the raw detail.
  const email = await client.getMessage(messageId);

  process.stdout.write(`Message ID: ${email.messageId}\n`);
  process.stdout.write(`From: ${email.sender}\n`);
  process.stdout.write(`To: ${email.recipient}\n`);
  // Other addresses from the original To: header (the message may have been
  // addressed to several agents at once; this row is one of those fan-outs).
  const recipientLower = email.recipient.toLowerCase();
  const otherTo = email.to.filter((a) => a.toLowerCase() !== recipientLower);
  if (otherTo.length > 0) {
    process.stdout.write(`Also-To: ${otherTo.join(", ")}\n`);
  }
  if (email.cc.length > 0) {
    process.stdout.write(`Cc: ${email.cc.join(", ")}\n`);
  }
  process.stdout.write(`Date: ${email.receivedAt ?? "unknown"}\n`);
  process.stdout.write(`Subject: ${email.subject}\n`);
  process.stdout.write("\n");

  if (email.textBody) {
    process.stdout.write(email.textBody + "\n");
  }
}
