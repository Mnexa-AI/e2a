import { createClient } from "../sdk.js";

// CLI commands for the customer-facing events API.
//   e2a events list [--type T] [--agent A] [--since RFC3339] [--limit N]
//   e2a events get <event-id>
//   e2a events redeliver <event-id> [--webhook <wh-id>]

export async function listEvents(opts: {
  type?: string;
  agentId?: string;
  conversationId?: string;
  messageId?: string;
  since?: string;
  until?: string;
  limit?: number;
  token?: string;
}): Promise<void> {
  const client = createClient({});
  const limit = opts.limit ?? 20;
  if (!Number.isFinite(limit) || limit < 1) {
    throw new Error("--limit must be a positive integer");
  }
  const events = await client.events
    .list({
      type: opts.type,
      agentId: opts.agentId,
      conversationId: opts.conversationId,
      messageId: opts.messageId,
      since: opts.since,
      until: opts.until,
      limit,
    })
    .toArray({ limit });
  if (events.length === 0) {
    process.stdout.write("No events in this window.\n");
    return;
  }
  for (const e of events) {
    const agent = e.agentId ?? "-";
    const created = e.createdAt instanceof Date ? e.createdAt.toISOString() : String(e.createdAt ?? "");
    process.stdout.write(
      `${created}  ${e.id}  ${(e.type ?? "").padEnd(24)}  ${(e.status ?? "").padEnd(10)}  agent=${agent}\n`,
    );
  }
}

export async function getEvent(eventId: string): Promise<void> {
  const client = createClient({});
  const e = await client.events.get(eventId);
  process.stdout.write(JSON.stringify(e, null, 2) + "\n");
}

export async function redeliverEvent(
  eventId: string,
  opts: { webhookId?: string },
): Promise<void> {
  const client = createClient({});
  const res = await client.events.redeliver(eventId, { webhookId: opts.webhookId });
  process.stdout.write(JSON.stringify(res, null, 2) + "\n");
}
