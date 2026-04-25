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
          agentCount={agents.filter((a) => a.domain === d.domain).length}
          onVerified={onRefresh}
          onDeleted={onRefresh}
        />
      ))}
    </div>
  );
}
