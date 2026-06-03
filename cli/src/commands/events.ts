import { createClient } from "../sdk.js";

// CLI commands for the slice 6/7 customer-facing events API.
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
  const res = await client.api.listEvents({
    type: opts.type,
    agentId: opts.agentId,
    conversationId: opts.conversationId,
    messageId: opts.messageId,
    since: opts.since,
    until: opts.until,
    pageSize: opts.limit ?? 20,
    token: opts.token,
  });
  if (!res.events || res.events.length === 0) {
    process.stdout.write("No events in this window.\n");
    return;
  }
  for (const e of res.events) {
    const agent = e.agent_id ?? "-";
    process.stdout.write(
      `${e.created_at}  ${e.id}  ${(e.type ?? "").padEnd(24)}  ${(e.status ?? "").padEnd(10)}  agent=${agent}\n`,
    );
  }
  if (res.next_token) {
    process.stdout.write(`\nNext page: --token ${res.next_token}\n`);
  }
}

export async function getEvent(eventId: string): Promise<void> {
  const client = createClient({});
  const e = await client.api.getEvent(eventId);
  process.stdout.write(JSON.stringify(e, null, 2) + "\n");
}

export async function redeliverEvent(
  eventId: string,
  opts: { webhookId?: string },
): Promise<void> {
  const client = createClient({});
  const res = await client.api.redeliverEvent(eventId, { webhookId: opts.webhookId });
  process.stdout.write(JSON.stringify(res, null, 2) + "\n");
}
