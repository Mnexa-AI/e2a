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

  const detail = await client.api.getMessage(client.agentEmail, messageId);
  const email = await client.parse(detail);

  process.stdout.write(`Message ID: ${email.messageId}\n`);
  process.stdout.write(`From: ${email.sender}\n`);
  process.stdout.write(`To: ${email.recipient}\n`);
  if (email.cc.length > 0) {
    process.stdout.write(`Cc: ${email.cc.join(", ")}\n`);
  }
  process.stdout.write(`Date: ${detail.created_at ?? "unknown"}\n`);
  process.stdout.write(`Subject: ${email.subject}\n`);
  process.stdout.write("\n");

  if (email.textBody) {
    process.stdout.write(email.textBody + "\n");
  }
}
