import { saveConfig } from "../config.js";
import { createClient } from "../sdk.js";

export async function agentsList(from: string | undefined): Promise<void> {
  const client = createClient({ from });

  const res = await client.api.listAgents();
  const agents = res.agents || [];

  if (agents.length === 0) {
    process.stderr.write("No agents registered. Run: e2a agents register <slug>\n");
    return;
  }

  const currentEmail = client.agentEmail;

  for (const agent of agents) {
    const marker = agent.email === currentEmail ? " (active)" : "";
    const mode = agent.agent_mode === "local" ? "local" : "cloud";
    process.stdout.write(`${agent.email}  ${mode}${marker}\n`);
  }
}

export async function agentsRegister(slug: string | undefined, name?: string): Promise<void> {
  if (!slug) {
    process.stderr.write("Usage: e2a agents register <slug> [--name \"Display Name\"]\n");
    process.stderr.write("Example: e2a agents register my-agent --name \"My Agent\"\n");
    process.exit(1);
  }

  const client = createClient();

  const res = await client.api.registerAgent({
    slug,
    name: name || undefined,
    agent_mode: "local",
  });

  saveConfig({ agent_email: res.email });

  process.stdout.write(`Registered: ${res.email}\n`);
  process.stdout.write(`Agent email saved to ~/.e2a/config.json\n`);
}

// AgentsUpdateOpts is the shape of flags accepted by `e2a agents update`.
// Only fields the caller provides are sent; omitted fields keep their
// current value server-side. hitlEnabled uses a tri-state (true / false
// / undefined) so `--hitl` and `--no-hitl` stay distinguishable from
// "not provided".
export interface AgentsUpdateOpts {
  hitlEnabled?: boolean;
  hitlTTLSeconds?: number;
  hitlExpirationAction?: "approve" | "reject";
  webhookUrl?: string;
}

export async function agentsUpdate(
  emailOrSlug: string | undefined,
  opts: AgentsUpdateOpts,
): Promise<void> {
  if (!emailOrSlug) {
    process.stderr.write(
      'Usage: e2a agents update <email-or-slug> [--hitl | --no-hitl] [--hitl-ttl <seconds>] [--hitl-expiration-action approve|reject] [--webhook-url <url>]\n',
    );
    process.exit(1);
  }
  const email = emailOrSlug.includes("@")
    ? emailOrSlug
    : `${emailOrSlug}@agents.e2a.dev`;

  // Build the request body from only the flags the user actually passed
  // so missing flags preserve their current server-side value.
  const body: Record<string, unknown> = {};
  if (opts.hitlEnabled !== undefined) body.hitl_enabled = opts.hitlEnabled;
  if (opts.hitlTTLSeconds !== undefined) body.hitl_ttl_seconds = opts.hitlTTLSeconds;
  if (opts.hitlExpirationAction !== undefined) {
    body.hitl_expiration_action = opts.hitlExpirationAction;
  }
  if (opts.webhookUrl !== undefined) body.webhook_url = opts.webhookUrl;

  if (Object.keys(body).length === 0) {
    process.stderr.write(
      "No changes requested. Pass at least one flag (e.g. --hitl, --hitl-ttl, --webhook-url).\n",
    );
    process.exit(1);
  }

  const client = createClient();
  const updated = await client.updateAgent(body, { agentEmail: email });

  process.stdout.write(`Updated: ${updated.email}\n`);
  const hitlStatus = updated.hitl_enabled
    ? `enabled · ${updated.hitl_ttl_seconds}s · auto-${updated.hitl_expiration_action}`
    : "disabled";
  process.stdout.write(`  HITL:        ${hitlStatus}\n`);
  if (updated.webhook_url) {
    process.stdout.write(`  Webhook:     ${updated.webhook_url}\n`);
  }
  if (updated.agent_mode) {
    process.stdout.write(`  Agent mode:  ${updated.agent_mode}\n`);
  }
}

export async function agentsDelete(emailOrSlug: string | undefined): Promise<void> {
  if (!emailOrSlug) {
    process.stderr.write("Usage: e2a agents delete <agent-email-or-slug>\n");
    process.stderr.write("Example: e2a agents delete my-agent\n");
    process.exit(1);
  }

  // Allow passing just the slug — expand to full shared-domain email
  const email = emailOrSlug.includes("@") ? emailOrSlug : `${emailOrSlug}@agents.e2a.dev`;

  const client = createClient();

  await client.api.deleteAgent(email);

  process.stdout.write(`Deleted: ${email}\n`);

  // Clear from config if it was the active agent
  if (client.agentEmail === email) {
    saveConfig({ agent_email: "" });
    process.stderr.write("Cleared active agent from config.\n");
  }
}
