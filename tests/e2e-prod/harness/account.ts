export interface AgentCapacityView {
  limits: { max_agents: number };
  usage: { agents: number };
}

export function agentHeadroom(account: unknown): number {
  if (typeof account !== "object" || account === null) {
    throw new TypeError("AccountView must contain integer limits.max_agents and usage.agents");
  }
  const { limits, usage } = account as { limits?: unknown; usage?: unknown };
  if (typeof limits !== "object" || limits === null) {
    throw new TypeError("AccountView limits.max_agents must be a non-negative integer");
  }
  if (typeof usage !== "object" || usage === null) {
    throw new TypeError("AccountView usage.agents must be a non-negative integer");
  }
  const maxAgents = (limits as { max_agents?: unknown }).max_agents;
  const usedAgents = (usage as { agents?: unknown }).agents;
  if (typeof maxAgents !== "number" || !Number.isSafeInteger(maxAgents) || maxAgents < 0) {
    throw new TypeError("AccountView limits.max_agents must be a non-negative integer");
  }
  if (typeof usedAgents !== "number" || !Number.isSafeInteger(usedAgents) || usedAgents < 0) {
    throw new TypeError("AccountView usage.agents must be a non-negative integer");
  }
  return Math.max(0, maxAgents - usedAgents);
}
