import type { UpdateAgentRequest } from "@e2a/sdk/v1";
import { loadConfig, saveConfig } from "../config.js";
import { createClient } from "../sdk.js";

/**
 * Expand a bare slug into a full email by appending the deployment's
 * shared domain. Inputs that already contain "@" pass through. Reads from
 * config first; if the field is missing (e.g. user upgraded from a CLI
 * version before the field existed and never re-logged-in) it discovers
 * the value from the deployment's `info()` and caches it for next time.
 * Throws when discovery fails *and* nothing is cached, since we can't
 * safely guess the right domain for the deployment.
 */
async function resolveAgentEmail(emailOrSlug: string): Promise<string> {
  if (emailOrSlug.includes("@")) return emailOrSlug;
  const config = loadConfig();
  if (config.shared_domain) {
    return `${emailOrSlug}@${config.shared_domain}`;
  }
  // Cache miss — discover and persist.
  try {
    const info = await createClient().info();
    if (!info.sharedDomain) {
      throw new Error(
        "this deployment doesn't expose a shared domain — pass the agent's full email instead",
      );
    }
    saveConfig({ shared_domain: info.sharedDomain });
    return `${emailOrSlug}@${info.sharedDomain}`;
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err);
    throw new Error(
      `cannot resolve slug "${emailOrSlug}" — pass the full email or set E2A_SHARED_DOMAIN. (${detail})`,
    );
  }
}

export async function agentsList(from: string | undefined): Promise<void> {
  const client = createClient({ from });

  const agents = await client.agents.list().toArray({ limit: 1000 });

  if (agents.length === 0) {
    process.stderr.write("No agents registered. Run: e2a agents register <slug>\n");
    return;
  }

  const config = loadConfig();
  const currentEmail = from || config.agent_email;

  for (const agent of agents) {
    const marker = agent.email === currentEmail ? " (active)" : "";
    const hitl = agent.hitlEnabled ? "hitl" : "no-hitl";
    process.stdout.write(`${agent.email}  ${hitl}${marker}\n`);
  }
}

export async function agentsRegister(slug: string | undefined, name?: string): Promise<void> {
  if (!slug) {
    process.stderr.write("Usage: e2a agents register <slug> [--name \"Display Name\"]\n");
    process.stderr.write("Example: e2a agents register my-agent --name \"My Agent\"\n");
    process.exit(1);
  }

  const client = createClient();

  const res = await client.agents.create({
    slug,
    name: name || undefined,
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
}

export async function agentsUpdate(
  emailOrSlug: string | undefined,
  opts: AgentsUpdateOpts,
): Promise<void> {
  if (!emailOrSlug) {
    process.stderr.write(
      'Usage: e2a agents update <email-or-slug> [--hitl | --no-hitl] [--hitl-ttl <seconds>] [--hitl-expiration-action approve|reject]\n',
    );
    process.exit(1);
  }
  const email = await resolveAgentEmail(emailOrSlug);

  // Build the request body from only the flags the user actually passed
  // so missing flags preserve their current server-side value.
  const body: UpdateAgentRequest = {};
  if (opts.hitlEnabled !== undefined) body.hitlEnabled = opts.hitlEnabled;
  if (opts.hitlTTLSeconds !== undefined) body.hitlTtlSeconds = opts.hitlTTLSeconds;
  if (opts.hitlExpirationAction !== undefined) {
    body.hitlExpirationAction = opts.hitlExpirationAction;
  }

  if (Object.keys(body).length === 0) {
    process.stderr.write(
      "No changes requested. Pass at least one flag (e.g. --hitl, --hitl-ttl).\n",
    );
    process.exit(1);
  }

  const client = createClient();
  const updated = await client.agents.update(email, body);

  process.stdout.write(`Updated: ${updated.email}\n`);
  const hitlStatus = updated.hitlEnabled
    ? `enabled · ${updated.hitlTtlSeconds}s · auto-${updated.hitlExpirationAction}`
    : "disabled";
  process.stdout.write(`  HITL:        ${hitlStatus}\n`);
  if (updated.inboundPolicy) {
    process.stdout.write(`  Inbound:     ${updated.inboundPolicy}\n`);
  }
}

export async function agentsDelete(emailOrSlug: string | undefined): Promise<void> {
  if (!emailOrSlug) {
    process.stderr.write("Usage: e2a agents delete <agent-email-or-slug>\n");
    process.stderr.write("Example: e2a agents delete my-agent\n");
    process.exit(1);
  }

  // Allow passing just the slug — expand to full shared-domain email
  const email = await resolveAgentEmail(emailOrSlug);

  const client = createClient();

  await client.agents.delete(email);

  process.stdout.write(`Deleted: ${email}\n`);

  // Clear from config if it was the active agent
  const config = loadConfig();
  if (config.agent_email === email) {
    saveConfig({ agent_email: "" });
    process.stderr.write("Cleared active agent from config.\n");
  }
}
