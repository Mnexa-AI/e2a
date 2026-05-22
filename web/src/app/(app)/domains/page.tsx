"use client";

import { useState, useEffect, useCallback } from "react";
import { DomainList } from "./_components/DomainList";
import { AddDomainForm } from "./_components/AddDomainForm";
import { listDomains, listAgents } from "../../components/onboarding/api";
import type { DomainInfo } from "../../components/onboarding/types";
import type { DashboardAgent } from "../../components/types";
import { PageShell } from "../../components/loft/PageShell";

// Domains stats strip — same degradation as dashboard: renders `—` until
// the workspace-level aggregate endpoint ships (BACKEND_TODO #1 / #7).
function DomainsStatsStrip({ domains }: { domains: DomainInfo[] }) {
  const verified = domains.filter((d) => d.verified).length;
  const unverified = domains.length - verified;
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-6">
      {[
        { label: "Total domains", value: String(domains.length) },
        { label: "Verified", value: String(verified) },
        { label: "Pending", value: String(unverified) },
        { label: "Agents · 7d", value: "—" },
      ].map((s) => (
        <div
          key={s.label}
          className="px-4 py-3.5"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <div
            className="font-mono text-[11px] font-semibold uppercase mb-1.5"
            style={{
              color: "var(--fg-subtle)",
              letterSpacing: "0.08em",
            }}
          >
            {s.label}
          </div>
          <div
            className="text-[24px]"
            style={{
              fontFamily: "var(--f-editorial)",
              color: "var(--fg)",
              letterSpacing: "-0.01em",
              lineHeight: 1.1,
            }}
          >
            {s.value}
          </div>
        </div>
      ))}
    </div>
  );
}

export default function DomainsPage() {
  const [domains, setDomains] = useState<DomainInfo[]>([]);
  const [agents, setAgents] = useState<DashboardAgent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showAddForm, setShowAddForm] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      const [domainsData, agentsData] = await Promise.all([
        listDomains(),
        listAgents(),
      ]);
      setDomains(domainsData);
      setAgents(agentsData);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load data");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const handleDomainRegistered = () => {
    setShowAddForm(false);
    fetchData();
  };

  // Empty state
  if (!loading && domains.length === 0 && !error) {
    return (
      <PageShell
        crumbs={["Domains"]}
        eyebrow="Workspace"
        title={<>Domains</>}
        subtitle="Add a domain to create branded agent addresses."
      >
        <div
          className="p-8 text-center space-y-4"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-lg)",
          }}
        >
          <p className="text-[14px]" style={{ color: "var(--fg-muted)" }}>
            You don&apos;t have any domains yet.
          </p>
          <div className="flex flex-col items-center gap-3">
            <button
              onClick={() => setShowAddForm(true)}
              className="px-4 py-2 text-[13px] font-medium transition"
              style={{
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                borderRadius: "var(--r-md)",
              }}
            >
              Add domain
            </button>
            <a
              href="/get-started?mode=shared"
              className="text-[13px] underline"
              style={{ color: "var(--accent-strong)" }}
            >
              Use shared e2a domain instead
            </a>
          </div>
        </div>

        {showAddForm && (
          <div className="mt-6">
            <AddDomainForm onRegistered={handleDomainRegistered} />
          </div>
        )}
      </PageShell>
    );
  }

  return (
    <PageShell
      crumbs={["Domains"]}
      eyebrow="Workspace"
      title={<>Domains</>}
      subtitle="Manage your domains. Verified domains can host agent email addresses."
      actions={
        <button
          onClick={() => setShowAddForm(!showAddForm)}
          className="px-4 py-2 text-[13px] font-medium transition"
          style={{
            background: showAddForm ? "var(--bg-panel)" : "var(--accent-fill)",
            color: showAddForm ? "var(--fg)" : "var(--accent-fg)",
            border: showAddForm ? "1px solid var(--border)" : "none",
            borderRadius: "var(--r-md)",
          }}
        >
          {showAddForm ? "Cancel" : "Add domain"}
        </button>
      }
    >
      <DomainsStatsStrip domains={domains} />

      {error && (
        <div
          className="mb-6 p-3 text-[13px]"
          style={{
            background: "var(--danger-bg)",
            color: "var(--danger-strong)",
            border: "1px solid var(--danger-bg)",
            borderRadius: "var(--r-md)",
          }}
        >
          {error}
        </div>
      )}

      {showAddForm && (
        <div className="mb-6">
          <AddDomainForm onRegistered={handleDomainRegistered} />
        </div>
      )}

      {loading ? (
        <div
          className="text-[13px] py-12 text-center"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading...
        </div>
      ) : (
        <DomainList domains={domains} agents={agents} onRefresh={fetchData} />
      )}
    </PageShell>
  );
}
