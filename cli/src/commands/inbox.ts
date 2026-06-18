import type { ListMessagesParams } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";

export async function inbox(
  status: "unread" | "read" | "all",
  limit: number,
  _token: string | undefined,
  agentFrom: string | undefined,
  sort?: "asc" | "desc",
  filters?: {
    from?: string;
    subjectContains?: string;
    conversationId?: string;
    since?: string;
    until?: string;
    labels?: string[];
  },
): Promise<void> {
  const client = createClient({ from: agentFrom });
  const address = requireAgentEmail(agentFrom);

  const params: ListMessagesParams = {
    status,
    sort,
    from: filters?.from,
    subjectContains: filters?.subjectContains,
    conversationId: filters?.conversationId,
    since: filters?.since,
    until: filters?.until,
    labels: filters?.labels && filters.labels.length ? filters.labels : undefined,
    limit,
  };

  const messages = await client.messages.list(address, params).toArray({ limit });

  if (messages.length === 0) {
    process.stdout.write("No messages.\n");
    return;
  }

  // Compute ID column width from actual data (never truncate IDs)
  const idW = Math.max(4, ...messages.map((m) => (m.messageId || "").length)) + 2;
  const fromW = 25;
  const subjW = 30;
  process.stdout.write(
    `${"ID".padEnd(idW)} ${"FROM".padEnd(fromW)} ${"SUBJECT".padEnd(subjW)} RECEIVED\n`,
  );

  for (const msg of messages) {
    const id = (msg.messageId || "").padEnd(idW);
    const from = (msg._from || "").padEnd(fromW).slice(0, fromW);
    const subj = (msg.subject || "").padEnd(subjW).slice(0, subjW);
    const received = msg.createdAt instanceof Date ? msg.createdAt.toISOString() : String(msg.createdAt ?? "");
    process.stdout.write(`${id} ${from} ${subj} ${received}\n`);
  }

  process.stdout.write(`\n${messages.length} messages\n`);
}
