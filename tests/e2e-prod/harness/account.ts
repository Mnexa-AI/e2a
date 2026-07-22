export interface AgentCapacityView {
  limits: { max_agents: number };
  usage: { agents: number };
}

export function agentHeadroom(account: AgentCapacityView): number {
  return Math.max(0, account.limits.max_agents - account.usage.agents);
}
