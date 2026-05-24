"use client";

import { DomainCard } from "./DomainCard";
import type { DomainInfo } from "../../../components/onboarding/types";
import type { DashboardAgent } from "../../../components/types";

export function DomainList({
  domains,
  agents,
  onRefresh,
}: {
  domains: DomainInfo[];
  agents: DashboardAgent[];
  onRefresh: () => void;
}) {
  return (
    <div className="space-y-4">
      {domains.map((d) => (
        <DomainCard
          key={d.domain}
          domain={d}
          // Prefer the server-computed count; fall back to the
          // client-side filter (defensive — older deployments without
          // the enrichment query still work).
          agentCount={
            d.agent_count ?? agents.filter((a) => a.domain === d.domain).length
          }
          onVerified={onRefresh}
          onDeleted={onRefresh}
        />
      ))}
    </div>
  );
}
