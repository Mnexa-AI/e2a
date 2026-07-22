export interface AgentCapacityView {
  limits: { max_agents: number };
  usage: { agents: number };
}

export function agentHeadroom(account: unknown): number {
  if (typeof account !== "object" || account === null) return 0;
  const { limits, usage } = account as { limits?: unknown; usage?: unknown };
  if (typeof limits !== "object" || limits === null || typeof usage !== "object" || usage === null) return 0;
  const maxAgents = (limits as { max_agents?: unknown }).max_agents;
  const usedAgents = (usage as { agents?: unknown }).agents;
  if (
    typeof maxAgents !== "number" ||
    typeof usedAgents !== "number" ||
    !Number.isSafeInteger(maxAgents) ||
    !Number.isSafeInteger(usedAgents) ||
    maxAgents < 0 ||
    usedAgents < 0
  ) {
    return 0;
  }
  return Math.max(0, maxAgents - usedAgents);
}
