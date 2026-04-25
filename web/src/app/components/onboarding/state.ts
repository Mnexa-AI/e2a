// Pure state helpers for the onboarding flow.
// No React, no fetch — just data transforms and derivations.

import type {
  AddressType,
  AgentMode,
  ChecklistStep,
  DomainInfo,
  DomainProgress,
  CustomFlowStep,
} from "./types";
import type { DashboardAgent } from "../types";

// ── Checklist derivation ─────────────────────────────────

/** Derive the checklist step for a domain given its agents. */
export function deriveChecklistStep(
  domain: DomainInfo,
  agents: DashboardAgent[],
): ChecklistStep {
  const domainAgents = agents.filter((a) => a.domain === domain.domain);

  if (!domain.verified) return "domain_added";
  if (domainAgents.length === 0) return "domain_verified";
  return "agent_created";
}

/** Build progress objects for all domains. */
export function buildDomainProgress(
  domains: DomainInfo[],
  agents: DashboardAgent[],
): DomainProgress[] {
  return domains.map((d) => ({
    domain: d,
    step: deriveChecklistStep(d, agents),
    agentCount: agents.filter((a) => a.domain === d.domain).length,
  }));
}

// ── Resume logic ─────────────────────────────────────────

/** Given a domain's checklist progress, determine the onboarding step to resume at.
 *  Returns null for domains that already have agents — those should send users
 *  to the Domains or Agents page, not back into onboarding (design line 528). */
export function getResumeTarget(progress: DomainProgress): CustomFlowStep | null {
  switch (progress.step) {
    case "domain_added":
      return "dns";
    case "domain_verified":
      return "create_agent";
    case "agent_created":
      return null;
  }
}

// ── Mode helpers ─────────────────────────────────────────

export function isCloudMode(mode: AgentMode): boolean {
  return mode === "cloud";
}

export function needsWebhookUrl(mode: AgentMode): boolean {
  return mode === "cloud";
}

/** Determine the address type from a domain string. */
export function getAddressType(domain: string): AddressType {
  return domain === "agents.e2a.dev" ? "shared" : "custom";
}

// ── Validation ───────────────────────────────────────────

// Must match backend: internal/agent/api.go slugPattern + validateSlug
const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$/;

// Must match backend: internal/agent/api.go reservedSlugs
const RESERVED_SLUGS = new Set([
  "admin", "postmaster", "abuse", "noreply", "no-reply",
  "mailer-daemon", "info", "help", "demo", "test",
  "www", "mail", "agent", "api", "system", "root",
]);

export function isValidSlug(slug: string): boolean {
  return slug.length >= 2 && slug.length <= 40 && SLUG_RE.test(slug) && !RESERVED_SLUGS.has(slug);
}

export function isValidDomain(domain: string): boolean {
  if (!domain || domain.length > 253) return false;
  const parts = domain.split(".");
  return parts.length >= 2 && parts.every((p) => /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(p));
}

export function isValidLocalPart(localPart: string): boolean {
  return localPart.length >= 1 && localPart.length <= 64 && /^[a-z0-9][a-z0-9._-]*$/.test(localPart);
}

export function isValidWebhookUrl(url: string): boolean {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "https:";
  } catch {
    return false;
  }
}
