import { createClient } from "../sdk.js";
import { loadConfig } from "../config.js";

export interface WhoamiOptions {
  json?: boolean;
}

// `whoami` is the preflight for scripts: one call that answers "is this key
// valid, what scope is it, and which inbox is it bound to". Works for both
// account- and agent-scoped keys (GET /v1/account is scope-aware).
export async function whoami(opts: WhoamiOptions): Promise<void> {
  const client = createClient();
  const account = await client.account.get();

  if (opts.json) {
    process.stdout.write(JSON.stringify(account) + "\n");
    return;
  }

  process.stdout.write(`user:  ${account.user.email} (${account.user.id})\n`);
  process.stdout.write(`scope: ${account.scope}\n`);
  if (account.agentAddress) {
    process.stdout.write(`agent: ${account.agentAddress}\n`);
  } else {
    // Account-scoped keys aren't bound to an inbox — show what send/reply
    // will actually use, so the preflight answers "which inbox am I?".
    const agentEmail = loadConfig().agent_email;
    process.stdout.write(
      agentEmail
        ? `agent: ${agentEmail} (default from config/E2A_AGENT_EMAIL)\n`
        : "agent: (none set — use --agent, E2A_AGENT_EMAIL, or e2a config set agent_email)\n",
    );
  }
  process.stdout.write(`plan:  ${account.planCode}\n`);
  process.stdout.write(
    `usage: ${account.usage.agents}/${account.limits.maxAgents} agents, ` +
      `${account.usage.messagesMonth}/${account.limits.maxMessagesMonth} messages this month\n`,
  );
}
