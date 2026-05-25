import { createClient } from "../sdk.js";

export async function inbox(
  status: string,
  limit: number,
  token: string | undefined,
  agentFrom: string | undefined,
  sort?: "asc" | "desc",
  filters?: {
    from?: string;
    subjectContains?: string;
    conversationId?: string;
    since?: string;
    until?: string;
  },
): Promise<void> {
  const client = createClient({ from: agentFrom });

  if (!client.agentEmail) {
    process.stderr.write("No agent email. Set one with: e2a config set agent_email <email>\n");
    process.exit(1);
  }

  const res = await client.api.listMessages(client.agentEmail, {
    status,
    pageSize: limit,
    token,
    sort,
    from: filters?.from,
    subjectContains: filters?.subjectContains,
    conversationId: filters?.conversationId,
    since: filters?.since,
    until: filters?.until,
  });

  if (!res.messages || res.messages.length === 0) {
    process.stdout.write("No messages.\n");
    return;
  }

  // Compute ID column width from actual data (never truncate IDs)
  const idW = Math.max(4, ...res.messages.map((m: { message_id?: string }) => (m.message_id || "").length)) + 2;
  const fromW = 25;
  const subjW = 30;
  process.stdout.write(
    `${"ID".padEnd(idW)} ${"FROM".padEnd(fromW)} ${"SUBJECT".padEnd(subjW)} RECEIVED\n`,
  );

  for (const msg of res.messages) {
    const id = (msg.message_id || "").padEnd(idW);
    const from = (msg.from || "").padEnd(fromW).slice(0, fromW);
    const subj = (msg.subject || "").padEnd(subjW).slice(0, subjW);
    process.stdout.write(`${id} ${from} ${subj} ${msg.created_at || ""}\n`);
  }

  process.stdout.write(`\n${res.messages.length} messages`);
  if (res.next_token) {
    process.stdout.write(` (use --token ${res.next_token} for next page)`);
  }
  process.stdout.write("\n");
}
