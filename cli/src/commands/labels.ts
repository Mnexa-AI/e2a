import type { UpdateMessageRequest } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

export async function labels(
  messageId: string | undefined,
  opts: {
    add: string[];
    remove: string[];
    from?: string;
  },
): Promise<void> {
  if (!messageId) {
    process.stderr.write(
      "Usage: e2a labels <message-id> [--add <label> ...] [--remove <label> ...]\n",
    );
    process.exit(1);
  }
  if (opts.add.length === 0 && opts.remove.length === 0) {
    process.stderr.write("at least one --add or --remove is required\n");
    process.exit(1);
  }

  const client = createClient({ from: opts.from });
  const address = requireAgentEmail(opts.from);

  const body: UpdateMessageRequest = {
    addLabels: opts.add.length ? opts.add : undefined,
    removeLabels: opts.remove.length ? opts.remove : undefined,
  };

  const res = await client.messages.updateLabels(address, messageId, body);

  process.stdout.write(`${res.messageId}: ${(res.labels ?? []).join(", ") || "(none)"}\n`);
}
