import { createClient } from "../sdk.js";

export async function conversationsList(opts: {
  pageSize?: number;
  since?: string;
  until?: string;
  from?: string;
}): Promise<void> {
  const client = createClient({ from: opts.from });
  if (!client.agentEmail) {
    process.stderr.write("No agent email. Set one with: e2a config set agent_email <email>\n");
    process.exit(1);
  }

  const res = await client.listConversations({
    pageSize: opts.pageSize,
    since: opts.since,
    until: opts.until,
  });
  const convos = res.conversations ?? [];
  if (convos.length === 0) {
    process.stdout.write("No conversations.\n");
    return;
  }

  // Compute ID column width from actual data (never truncate IDs).
  const idW = Math.max(15, ...convos.map((c) => (c.conversation_id || "").length)) + 2;
  const subjW = 40;
  process.stdout.write(
    `${"CONVERSATION ID".padEnd(idW)} ${"MSGS".padStart(5)}  ${"UNREAD".padEnd(7)} ${"SUBJECT".padEnd(subjW)} LAST ACTIVITY\n`,
  );
  for (const c of convos) {
    const id = (c.conversation_id || "").padEnd(idW);
    const msgs = String(c.message_count ?? 0).padStart(5);
    const unread = (c.has_unread ? "yes" : "no").padEnd(7);
    const subj = (c.latest_subject || "").padEnd(subjW).slice(0, subjW);
    process.stdout.write(`${id} ${msgs}  ${unread} ${subj} ${c.last_message_at || ""}\n`);
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
  if (!client.agentEmail) {
    process.stderr.write("No agent email. Set one with: e2a config set agent_email <email>\n");
    process.exit(1);
  }

  const detail = await client.getConversation(conversationId);
  process.stdout.write(`Conversation:  ${detail.conversation_id}\n`);
  process.stdout.write(`Messages:      ${detail.message_count ?? 0} `);
  process.stdout.write(`(inbound: ${detail.inbound_count ?? 0}, outbound: ${detail.outbound_count ?? 0})\n`);
  process.stdout.write(`Unread:        ${detail.has_unread ? "yes" : "no"}\n`);
  if (detail.first_message_at) {
    process.stdout.write(`First message: ${detail.first_message_at}\n`);
  }
  if (detail.last_message_at) {
    process.stdout.write(`Last message:  ${detail.last_message_at}\n`);
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
  const idW = Math.max(15, ...msgs.map((m) => (m.message_id || "").length)) + 2;
  const fromW = 25;
  const subjW = 35;
  process.stdout.write(
    `${"ID".padEnd(idW)} ${"DIR".padEnd(4)} ${"FROM".padEnd(fromW)} ${"SUBJECT".padEnd(subjW)} CREATED\n`,
  );
  for (const m of msgs) {
    const id = (m.message_id || "").padEnd(idW);
    const dir = (m.direction || "?").padEnd(4);
    const from = (m.from || "").padEnd(fromW).slice(0, fromW);
    const subj = (m.subject || "").padEnd(subjW).slice(0, subjW);
    process.stdout.write(`${id} ${dir} ${from} ${subj} ${m.created_at || ""}\n`);
  }
}
