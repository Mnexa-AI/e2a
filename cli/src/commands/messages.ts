import type { ListMessagesParams } from "@e2a/sdk/v1";
import { createClient, requireAgentEmail } from "../sdk.js";
import { EXIT, fail } from "../exit.js";

export interface MessagesListOptions {
  agent?: string;
  direction?: string;
  since?: string;
  conversation?: string;
  limit?: string;
  json?: boolean;
}

export interface MessagesGetOptions {
  agent?: string;
  text?: boolean;
  json?: boolean;
}

const LIST_USAGE =
  "usage: e2a messages list [--direction inbound|outbound|all] [--since <ISO>] [--conversation <id>] [--limit <n>] [--agent <inbox>] [--json]";
const GET_USAGE = "usage: e2a messages get <message-id> [--text] [--agent <inbox>] [--json]";

const DIRECTIONS = ["inbound", "outbound", "all"] as const;

// The server pages at 50 by default but allows 100; always ask for the max so
// draining a mailbox costs half the round trips.
const MAX_PAGE_SIZE = 100;

// Default output is TSV (message_id, from, created_at) in ascending order —
// the shape a shell poll loop wants (`while IFS=$'\t' read -r id from at`).
// --json emits one full summary object per line (NDJSON), in the SDK's
// camelCase model shape like `listen --json`.
export async function messagesList(opts: MessagesListOptions): Promise<void> {
  const params: ListMessagesParams = { sort: "asc" };

  if (opts.direction) {
    if (!(DIRECTIONS as readonly string[]).includes(opts.direction)) fail(EXIT.USAGE, LIST_USAGE);
    params.direction = opts.direction as (typeof DIRECTIONS)[number];
  }
  if (opts.since) params.since = opts.since;
  if (opts.conversation) params.conversationId = opts.conversation;

  let max: number | undefined;
  if (opts.limit !== undefined) {
    max = Number(opts.limit);
    if (!Number.isInteger(max) || max <= 0) fail(EXIT.USAGE, LIST_USAGE);
  }
  params.limit = Math.min(max ?? MAX_PAGE_SIZE, MAX_PAGE_SIZE);

  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);

  let count = 0;
  for await (const m of client.messages.list(agentEmail, params)) {
    if (opts.json) {
      process.stdout.write(JSON.stringify(m) + "\n");
    } else {
      process.stdout.write(`${m.messageId}\t${m._from}\t${m.createdAt.toISOString()}\n`);
    }
    count++;
    if (max !== undefined && count >= max) break;
  }
}

export async function messagesGet(
  messageId: string | undefined,
  opts: MessagesGetOptions,
): Promise<void> {
  if (!messageId) fail(EXIT.USAGE, GET_USAGE);
  // JSON is the default output, so --json is accepted as an explicit alias —
  // but combining it with --text is a contradiction, not a precedence puzzle.
  if (opts.text && opts.json) fail(EXIT.USAGE, "--text and --json are mutually exclusive");

  const client = createClient();
  const agentEmail = requireAgentEmail(opts.agent);
  const message = await client.messages.get(agentEmail, messageId);

  if (opts.text) {
    // Parsed text (quoted history stripped) beats the raw body when available;
    // a just-received message can have neither for a moment (async parse).
    const text = message.parsed?.text || message.body?.text || "";
    process.stdout.write(text.trim() + "\n");
    return;
  }
  process.stdout.write(JSON.stringify(message) + "\n");
}
