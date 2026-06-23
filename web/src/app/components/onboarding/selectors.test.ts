import {
  groupAgentsByDomain,
  getDomainProgress,
  getUnverifiedDomains,
  getDomainsReadyForAgents,
  getSharedAgents,
  getCustomAgents,
  hasExistingSetup,
  getResumeOptions,
  getBestResumeTarget,
  hasVerifiedDomainWithoutAgents,
  getDomainsNeedingVerification,
} from "./selectors";
import type { DomainInfo } from "./types";
import type { DashboardAgent } from "../types";

// ── Fixtures ─────────────────────────────────────────────

function makeDomain(overrides: Partial<DomainInfo> = {}): DomainInfo {
  return {
    domain: "mail.example.com",
    verified: false,
    verification_token: "e2a-verify=abc123",
    dns_records: {
      mx: { host: "mail.example.com", value: "mx.e2a.dev", priority: 10 },
      txt: { host: "mail.example.com", value: "e2a-verify=abc123" },
    },
    created_at: "2026-01-01T00:00:00Z",
    verified_at: null,
    ...overrides,
  };
}

function makeAgent(overrides: Partial<DashboardAgent> = {}): DashboardAgent {
  return {
    id: "ag_123",
    domain: "mail.example.com",
    email: "support@mail.example.com",
    name: "support",
    domain_verified: true,
    created_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

// ── groupAgentsByDomain ──────────────────────────────────

describe("groupAgentsByDomain", () => {
  it("returns empty map for no agents", () => {
    expect(groupAgentsByDomain([])).toEqual(new Map());
  });

  it("groups agents by domain", () => {
    const a1 = makeAgent({ domain: "a.com", email: "x@a.com" });
    const a2 = makeAgent({ domain: "a.com", email: "y@a.com" });
    const a3 = makeAgent({ domain: "b.com", email: "z@b.com" });

    const grouped = groupAgentsByDomain([a1, a2, a3]);
    expect(grouped.get("a.com")).toHaveLength(2);
    expect(grouped.get("b.com")).toHaveLength(1);
  });
});

// ── getDomainProgress ────────────────────────────────────

describe("getDomainProgress", () => {
  it("returns progress for unverified domain", () => {
    const domain = makeDomain({ verified: false });
    const progress = getDomainProgress(domain, []);
    expect(progress.step).toBe("domain_added");
    expect(progress.agentCount).toBe(0);
  });

  it("counts agents on the domain", () => {
    const domain = makeDomain({ verified: true, domain: "test.com" });
    const agents = [
      makeAgent({ domain: "test.com" }),
      makeAgent({ domain: "test.com", email: "other@test.com" }),
      makeAgent({ domain: "other.com" }),
    ];
    const progress = getDomainProgress(domain, agents);
    expect(progress.agentCount).toBe(2);
  });
});

// ── getUnverifiedDomains ─────────────────────────────────

describe("getUnverifiedDomains", () => {
  it("filters to unverified only", () => {
    const d1 = makeDomain({ domain: "a.com", verified: false });
    const d2 = makeDomain({ domain: "b.com", verified: true });
    const d3 = makeDomain({ domain: "c.com", verified: false });

    expect(getUnverifiedDomains([d1, d2, d3])).toEqual([d1, d3]);
  });
});

// ── getDomainsReadyForAgents ─────────────────────────────

describe("getDomainsReadyForAgents", () => {
  it("returns verified domains with no agents", () => {
    const d1 = makeDomain({ domain: "a.com", verified: true });
    const d2 = makeDomain({ domain: "b.com", verified: true });
    const d3 = makeDomain({ domain: "c.com", verified: false });
    const agent = makeAgent({ domain: "b.com" });

    const ready = getDomainsReadyForAgents([d1, d2, d3], [agent]);
    expect(ready).toEqual([d1]);
  });
});

// ── getSharedAgents / getCustomAgents ────────────────────

describe("getSharedAgents", () => {
  it("filters to shared domain agents", () => {
    const shared = makeAgent({ domain: "agents.e2a.dev", email: "bot@agents.e2a.dev" });
    const custom = makeAgent({ domain: "mail.co", email: "bot@mail.co" });

    expect(getSharedAgents([shared, custom])).toEqual([shared]);
  });
});

describe("getCustomAgents", () => {
  it("filters to custom domain agents", () => {
    const shared = makeAgent({ domain: "agents.e2a.dev", email: "bot@agents.e2a.dev" });
    const custom = makeAgent({ domain: "mail.co", email: "bot@mail.co" });

    expect(getCustomAgents([shared, custom])).toEqual([custom]);
  });
});

// ── hasExistingSetup ─────────────────────────────────────

describe("hasExistingSetup", () => {
  it("returns false when nothing exists", () => {
    expect(hasExistingSetup([], [])).toBe(false);
  });

  it("returns true when domains exist", () => {
    expect(hasExistingSetup([makeDomain()], [])).toBe(true);
  });

  it("returns true when agents exist", () => {
    expect(hasExistingSetup([], [makeAgent()])).toBe(true);
  });
});

// ── getResumeOptions ─────────────────────────────────────

describe("getResumeOptions", () => {
  it("returns empty array for fresh user", () => {
    expect(getResumeOptions([], [])).toEqual([]);
  });

  it("returns verify_domain for unverified domain", () => {
    const d = makeDomain({ domain: "a.com", verified: false });
    const options = getResumeOptions([d], []);
    expect(options).toHaveLength(1);
    expect(options[0]).toEqual({ type: "verify_domain", domain: d });
  });

  it("returns create_agent for verified domain with no agents", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    const options = getResumeOptions([d], []);
    expect(options).toHaveLength(1);
    expect(options[0]).toEqual({ type: "create_agent", domain: d });
  });

  it("returns has_agents when agents exist", () => {
    const options = getResumeOptions([], [makeAgent()]);
    expect(options).toHaveLength(1);
    expect(options[0]).toEqual({ type: "has_agents", count: 1 });
  });

  it("returns multiple options for mixed state", () => {
    const unverified = makeDomain({ domain: "a.com", verified: false });
    const verified = makeDomain({ domain: "b.com", verified: true });
    const agent = makeAgent({ domain: "c.com" });

    const options = getResumeOptions([unverified, verified], [agent]);
    expect(options).toHaveLength(3);
    expect(options.map((o) => o.type)).toEqual(["verify_domain", "create_agent", "has_agents"]);
  });

  it("does not return create_agent for verified domain that already has agents", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    const agent = makeAgent({ domain: "a.com" });
    const options = getResumeOptions([d], [agent]);
    // Only has_agents, no create_agent since domain already has an agent
    expect(options).toHaveLength(1);
    expect(options[0].type).toBe("has_agents");
  });
});

// ── getBestResumeTarget ──────────────────────────────────

describe("getBestResumeTarget", () => {
  it("returns null for fresh user", () => {
    expect(getBestResumeTarget([], [])).toBeNull();
  });

  it("returns single unverified domain as best target", () => {
    const d = makeDomain({ domain: "a.com", verified: false });
    const result = getBestResumeTarget([d], []);
    expect(result).toEqual({ type: "verify_domain", domain: d });
  });

  it("returns single verified-no-agents domain as best target", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    const result = getBestResumeTarget([d], []);
    expect(result).toEqual({ type: "create_agent", domain: d });
  });

  it("returns null when multiple domains are in flight", () => {
    const d1 = makeDomain({ domain: "a.com", verified: false });
    const d2 = makeDomain({ domain: "b.com", verified: true });
    expect(getBestResumeTarget([d1, d2], [])).toBeNull();
  });

  it("returns null when agents exist alongside domain options", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    const agent = makeAgent({ domain: "b.com" });
    expect(getBestResumeTarget([d], [agent])).toBeNull();
  });
});

// ── hasVerifiedDomainWithoutAgents ────────────────────────

describe("hasVerifiedDomainWithoutAgents", () => {
  it("returns false when no domains", () => {
    expect(hasVerifiedDomainWithoutAgents([], [])).toBe(false);
  });

  it("returns true for verified domain with no agents", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    expect(hasVerifiedDomainWithoutAgents([d], [])).toBe(true);
  });

  it("returns false when all verified domains have agents", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    const agent = makeAgent({ domain: "a.com" });
    expect(hasVerifiedDomainWithoutAgents([d], [agent])).toBe(false);
  });
});

// ── getDomainsNeedingVerification ────────────────────────

describe("getDomainsNeedingVerification", () => {
  it("returns only unverified domains", () => {
    const d1 = makeDomain({ domain: "a.com", verified: false });
    const d2 = makeDomain({ domain: "b.com", verified: true });
    expect(getDomainsNeedingVerification([d1, d2])).toEqual([d1]);
  });

  it("returns empty for all verified", () => {
    const d = makeDomain({ domain: "a.com", verified: true });
    expect(getDomainsNeedingVerification([d])).toEqual([]);
  });
});
