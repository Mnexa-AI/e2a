import { createClient, requireAgentEmail } from "../sdk.js";

export async function conversationsList(opts: {
  pageSize?: number;
  since?: string;
  until?: string;
  from?: string;
}): Promise<void> {
  const client = createClient({ from: opts.from });
  const address = requireAgentEmail(opts.from);

  const convos = await client.conversations.list(address, {
    limit: opts.pageSize,
    since: opts.since,
    until: opts.until,
  });
  if (convos.length === 0) {
    process.stdout.write("No conversations.\n");
    return;
  }

  // Compute ID column width from actual data (never truncate IDs).
  const idW = Math.max(15, ...convos.map((c) => (c.conversationId || "").length)) + 2;
  const subjW = 40;
  process.stdout.write(
    `${"CONVERSATION ID".padEnd(idW)} ${"MSGS".padStart(5)}  ${"UNREAD".padEnd(7)} ${"SUBJECT".padEnd(subjW)} LAST ACTIVITY\n`,
  );
  for (const c of convos) {
    const id = (c.conversationId || "").padEnd(idW);
    const msgs = String(c.messageCount ?? 0).padStart(5);
    const unread = (c.hasUnread ? "yes" : "no").padEnd(7);
    const subj = (c.latestSubject || "").padEnd(subjW).slice(0, subjW);
    process.stdout.write(`${id} ${msgs}  ${unread} ${subj} ${formatDate(c.lastMessageAt)}\n`);
  }
  process.stdout.write(`\n${convos.length} conversations\n`);
}

export async function conversationsShow(
  conversationId: string | undefined,
  opts: { from?: string },
): Promise<void> {
  if (!conversationId) {
    process.stderr.write("Usage: e2a conversations show <conversation-id>\n");
    process.exit(1);
  }
  const client = createClient({ from: opts.from });
  const address = requireAgentEmail(opts.from);

  const detail = await client.conversations.get(address, conversationId);
  process.stdout.write(`Conversation:  ${detail.conversationId}\n`);
  process.stdout.write(`Messages:      ${detail.messageCount ?? 0} `);
  process.stdout.write(`(inbound: ${detail.inboundCount ?? 0}, outbound: ${detail.outboundCount ?? 0})\n`);
  process.stdout.write(`Unread:        ${detail.hasUnread ? "yes" : "no"}\n`);
  if (detail.firstMessageAt) {
    process.stdout.write(`First message: ${formatDate(detail.firstMessageAt)}\n`);
  }
  if (detail.lastMessageAt) {
    process.stdout.write(`Last message:  ${formatDate(detail.lastMessageAt)}\n`);
  }
  if (detail.participants && detail.participants.length) {
    process.stdout.write(`Participants:  ${detail.participants.join(", ")}\n`);
  }
  if (detail.labels && detail.labels.length) {
    process.stdout.write(`Labels:        ${detail.labels.join(", ")}\n`);
  }
  process.stdout.write("\n");

  const msgs = detail.messages ?? [];
  if (msgs.length === 0) {
    return;
  }
  const idW = Math.max(15, ...msgs.map((m) => (m.messageId || "").length)) + 2;
  const fromW = 25;
  const subjW = 35;
  process.stdout.write(
    `${"ID".padEnd(idW)} ${"DIR".padEnd(4)} ${"FROM".padEnd(fromW)} ${"SUBJECT".padEnd(subjW)} CREATED\n`,
  );
  for (const m of msgs) {
    const id = (m.messageId || "").padEnd(idW);
    const dir = (m.direction || "?").padEnd(4);
    const from = (m._from || "").padEnd(fromW).slice(0, fromW);
    const subj = (m.subject || "").padEnd(subjW).slice(0, subjW);
    process.stdout.write(`${id} ${dir} ${from} ${subj} ${formatDate(m.createdAt)}\n`);
  }
}

// The generated models type date-time fields as `Date`. Render them back
// to ISO strings for stable, scriptable CLI output.
function formatDate(d: Date | string | null | undefined): string {
  if (!d) return "";
  if (d instanceof Date) return d.toISOString();
  return String(d);
}
