import type {
  CreateWebhookRequest,
  UpdateWebhookRequest,
  WebhookFiltersView,
} from "@e2a/sdk/v1";
import { createClient } from "../sdk.js";

// The generated models type date-time fields as `Date`. Render them back
// to ISO strings for stable, scriptable CLI output.
function fmtDate(d: Date | string | null | undefined): string {
  if (!d) return "";
  return d instanceof Date ? d.toISOString() : String(d);
}

// `e2a webhooks list` — one row per webhook, owner-scoped.
export async function webhooksList(): Promise<void> {
  const client = createClient();
  const hooks = await client.webhooks.list().toArray({ limit: 1000 });
  if (hooks.length === 0) {
    process.stderr.write("No webhooks. Create one with: e2a webhooks create --url <url> --events email.received\n");
    return;
  }
  for (const h of hooks) {
    const enabled = h.enabled ? "enabled" : "disabled";
    const events = (h.events || []).join(",");
    process.stdout.write(`${h.id}  ${enabled}  ${h.url}  [${events}]\n`);
  }
}

export interface WebhooksCreateOptions {
  url?: string;
  events?: string[];
  description?: string;
  agentId?: string[];
  conversationId?: string[];
  label?: string[];
}

// `e2a webhooks create --url <url> --events email.received [--events email.sent]
//                     [--description "…"] [--agent-id …] [--conversation-id …] [--label …]`
export async function webhooksCreate(opts: WebhooksCreateOptions): Promise<void> {
  if (!opts.url) {
    process.stderr.write("Usage: e2a webhooks create --url <url> --events <event> [--events <event> …]\n");
    process.exit(1);
  }
  if (!opts.events || opts.events.length === 0) {
    process.stderr.write("--events is required (e.g. --events email.received)\n");
    process.exit(1);
  }
  const filters: WebhookFiltersView = {};
  if (opts.agentId && opts.agentId.length > 0) filters.agentIds = opts.agentId;
  if (opts.conversationId && opts.conversationId.length > 0) filters.conversationIds = opts.conversationId;
  if (opts.label && opts.label.length > 0) filters.labels = opts.label;

  const body: CreateWebhookRequest = {
    url: opts.url,
    events: opts.events,
    description: opts.description ?? "",
    filters: Object.keys(filters).length > 0 ? filters : undefined,
  };

  const client = createClient();
  const res = await client.webhooks.create(body);
  process.stdout.write(`Created: ${res.id}\n`);
  // The plaintext secret is printed exactly once, here. Subsequent
  // get/list calls scrub it.
  if (res.signingSecret) {
    process.stdout.write(`Signing secret: ${res.signingSecret}\n`);
    process.stdout.write("Store this somewhere safe — it won't be shown again.\n");
  }
}

export async function webhooksGet(id: string | undefined): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a webhooks get <id>\n");
    process.exit(1);
  }
  const client = createClient();
  const w = await client.webhooks.get(id);
  process.stdout.write(JSON.stringify(w, null, 2) + "\n");
}

export interface WebhooksUpdateOptions {
  url?: string;
  events?: string[];
  description?: string;
  enabled?: boolean;
}

// `e2a webhooks update <id> [--url …] [--events …] [--description …]
//                            [--enable | --disable]`
export async function webhooksUpdate(
  id: string | undefined,
  opts: WebhooksUpdateOptions,
): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a webhooks update <id> [--url …] [--events …] [--enable|--disable]\n");
    process.exit(1);
  }
  const body: UpdateWebhookRequest = {};
  if (opts.url !== undefined) body.url = opts.url;
  if (opts.events !== undefined && opts.events.length > 0) body.events = opts.events;
  if (opts.description !== undefined) body.description = opts.description;
  if (opts.enabled !== undefined) body.enabled = opts.enabled;
  if (Object.keys(body).length === 0) {
    process.stderr.write("nothing to update — pass at least one of --url/--events/--description/--enable/--disable\n");
    process.exit(1);
  }
  const client = createClient();
  const w = await client.webhooks.update(id, body);
  process.stdout.write(`Updated: ${w.id}  enabled=${w.enabled}\n`);
}

export async function webhooksDelete(id: string | undefined): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a webhooks delete <id>\n");
    process.exit(1);
  }
  const client = createClient();
  await client.webhooks.delete(id);
  process.stdout.write(`Deleted: ${id}\n`);
}

export async function webhooksRotateSecret(id: string | undefined): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a webhooks rotate-secret <id>\n");
    process.exit(1);
  }
  const client = createClient();
  const res = await client.webhooks.rotateSecret(id);
  process.stdout.write(`New signing secret: ${res.signingSecret}\n`);
  process.stdout.write(`Previous secret expires at: ${res.previousSecretExpiresAt}\n`);
  process.stdout.write("Store the new secret — it won't be shown again.\n");
}

export interface WebhooksTestOptions {
  event?: string;
}

// `e2a webhooks test <id> [--event email.received]`
export async function webhooksTest(
  id: string | undefined,
  opts: WebhooksTestOptions,
): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a webhooks test <id> [--event <event>]\n");
    process.exit(1);
  }
  const client = createClient();
  const res = await client.webhooks.test(id, {
    event: opts.event ?? "",
    data: { test: true },
  });
  process.stdout.write(`Scheduled test delivery: ${res.deliveryId}\n`);
  process.stdout.write("Use `e2a webhooks deliveries " + id + "` to see status.\n");
}

export interface WebhooksDeliveriesOptions {
  limit?: number;
  status?: "pending" | "delivered" | "failed";
}

export async function webhooksDeliveries(
  id: string | undefined,
  opts: WebhooksDeliveriesOptions,
): Promise<void> {
  if (!id) {
    process.stderr.write("Usage: e2a webhooks deliveries <id> [--limit N] [--status pending|delivered|failed]\n");
    process.exit(1);
  }
  const client = createClient();
  const limit = opts.limit ?? 50;
  const rows = await client.webhooks
    .deliveries(id, { limit, status: opts.status })
    .toArray({ limit });
  if (rows.length === 0) {
    process.stdout.write("No deliveries yet.\n");
    return;
  }
  for (const r of rows) {
    const code = r.lastStatusCode !== undefined ? `(${r.lastStatusCode})` : "";
    process.stdout.write(`${r.id}  ${r.status}  attempts=${r.attempts}  ${r.eventType}  ${code}  ${fmtDate(r.createdAt)}\n`);
  }
}
