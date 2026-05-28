import { createClient } from "../sdk.js";

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

  if (!client.agentEmail) {
    process.stderr.write(
      "No agent email configured. Run 'e2a register' first or use --agent.\n",
    );
    process.exit(1);
  }

  const res = await client.updateMessageLabels(messageId, {
    addLabels: opts.add.length ? opts.add : undefined,
    removeLabels: opts.remove.length ? opts.remove : undefined,
  });

  process.stdout.write(`${res.message_id}: ${(res.labels ?? []).join(", ") || "(none)"}\n`);
}
