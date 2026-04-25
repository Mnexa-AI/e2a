// Pure selectors that derive UI state from domains + agents data.
// No side effects, no React — easy to test.

import type { DomainInfo, DomainProgress } from "./types";
import type { DashboardAgent } from "../types";
import { buildDomainProgress } from "./state";

/** Group agents by their domain. */
export function groupAgentsByDomain(
  agents: DashboardAgent[],
): Map<string, DashboardAgent[]> {
  const map = new Map<string, DashboardAgent[]>();
  for (const agent of agents) {
    const list = map.get(agent.domain) ?? [];
    list.push(agent);
    map.set(agent.domain, list);
  }
  return map;
}

/** Get progress for a single domain. */
export function getDomainProgress(
  domain: DomainInfo,
  agents: DashboardAgent[],
): DomainProgress {
  return buildDomainProgress([domain], agents)[0];
}

/** Find unverified domains (need attention). */
export function getUnverifiedDomains(domains: DomainInfo[]): DomainInfo[] {
  return domains.filter((d) => !d.verified);
}

/** Find verified domains with no agents (ready for agent creation). */
export function getDomainsReadyForAgents(
  domains: DomainInfo[],
  agents: DashboardAgent[],
): DomainInfo[] {
  return domains.filter(
    (d) => d.verified && !agents.some((a) => a.domain === d.domain),
  );
}

/** Get shared-domain agents. */
export function getSharedAgents(agents: DashboardAgent[]): DashboardAgent[] {
  return agents.filter((a) => a.domain === "agents.e2a.dev");
}

/** Get custom-domain agents. */
export function getCustomAgents(agents: DashboardAgent[]): DashboardAgent[] {
  return agents.filter((a) => a.domain !== "agents.e2a.dev");
}

/** Check if user has any domains or agents set up. */
export function hasExistingSetup(
  domains: DomainInfo[],
  agents: DashboardAgent[],
): boolean {
  return domains.length > 0 || agents.length > 0;
}

// ── Resume / re-entry selectors ─────────────────────────

export type ResumeOption =
  | { type: "verify_domain"; domain: DomainInfo }
  | { type: "create_agent"; domain: DomainInfo }
  | { type: "has_agents"; count: number };

/**
 * Derive all possible resume options from current state.
 * Returns an empty array for fresh users (no domains, no agents).
 */
export function getResumeOptions(
  domains: DomainInfo[],
  agents: DashboardAgent[],
): ResumeOption[] {
  const options: ResumeOption[] = [];

  // Unverified domains → resume verification
  for (const d of domains) {
    if (!d.verified) {
      options.push({ type: "verify_domain", domain: d });
    }
  }

  // Verified domains with no agents → create agent
  const domainsReady = getDomainsReadyForAgents(domains, agents);
  for (const d of domainsReady) {
    options.push({ type: "create_agent", domain: d });
  }

  // Has existing agents
  if (agents.length > 0) {
    options.push({ type: "has_agents", count: agents.length });
  }

  return options;
}

/**
 * Pick the single best resume target, or null if state is fresh.
 * Only returns a definitive target when there is exactly one obvious action.
 * If ambiguous (multiple domains in flight), returns null — caller should
 * show a chooser instead.
 */
export function getBestResumeTarget(
  domains: DomainInfo[],
  agents: DashboardAgent[],
): ResumeOption | null {
  const options = getResumeOptions(domains, agents);
  if (options.length === 0) return null;

  // If there is only one actionable domain option, use it
  const domainOptions = options.filter((o) => o.type !== "has_agents");
  if (domainOptions.length === 1 && agents.length === 0) {
    return domainOptions[0];
  }

  // Multiple options → return null, let the UI show a chooser
  return null;
}

/** Check if any verified domain has no agents yet. */
export function hasVerifiedDomainWithoutAgents(
  domains: DomainInfo[],
  agents: DashboardAgent[],
): boolean {
  return getDomainsReadyForAgents(domains, agents).length > 0;
}

/** Get domains that still need verification. */
export function getDomainsNeedingVerification(
  domains: DomainInfo[],
): DomainInfo[] {
  return domains.filter((d) => !d.verified);
}
