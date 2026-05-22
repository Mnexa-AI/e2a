"use client";

import { useState, useEffect, useCallback } from "react";
import Link from "next/link";
import { useAuth } from "../../components/AuthProvider";
import { listAgents, deleteAgent } from "../../components/onboarding/api";
import type { DashboardAgent } from "../../components/types";
import { PageShell } from "../../components/loft/PageShell";
import { AgentsEmptyState } from "./_components/AgentsEmptyState";
import { AgentCard } from "./_components/AgentCard";

// Stats strip — all four cards render `—` until GET /api/dashboard/stats ships
// (see BACKEND_TODO #1). Don't fabricate values.
function StatsStrip() {
  const stats = [
    { label: "Inbound today", value: "—", delta: null as string | null },
    { label: "Outbound today", value: "—", delta: null as string | null },
    { label: "Pending review", value: "—", delta: null as string | null },
    { label: "Delivery success", value: "—", delta: null as string | null },
  ];

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3 mb-6">
      {stats.map((s) => (
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
          {s.delta && (
            <div
              className="text-[11px] mt-1"
              style={{ color: "var(--fg-muted)" }}
            >
              {s.delta}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

export default function DashboardPage() {
  const { user } = useAuth();
  const [agents, setAgents] = useState<DashboardAgent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const fetchAgents = useCallback(async () => {
    try {
      const data = await listAgents();
      setAgents(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load agents");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchAgents();
  }, [fetchAgents]);

  const handleDelete = async (email: string) => {
    if (!confirm(`Delete agent ${email}? This cannot be undone.`)) return;
    try {
      await deleteAgent(email);
      fetchAgents();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete agent");
    }
  };

  return (
    <PageShell
      crumbs={["Agents"]}
      eyebrow="Workspace"
      title={<>Agents</>}
      subtitle={
        <>
          Manage your registered agents. Signed in as{" "}
          <span style={{ color: "var(--fg)", fontWeight: 500 }}>
            {user?.email}
          </span>
          .
        </>
      }
      actions={
        agents.length > 0 ? (
          <Link
            href="/get-started"
            className="inline-flex items-center gap-1.5 text-[13px] font-medium px-4 py-2"
            style={{
              background: "var(--accent-fill)",
              color: "var(--accent-fg)",
              borderRadius: "var(--r-md)",
            }}
          >
            Create agent
            <span className="font-mono">→</span>
          </Link>
        ) : null
      }
    >
      <StatsStrip />

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

      {loading ? (
        <div
          className="text-[13px] py-12 text-center"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading...
        </div>
      ) : agents.length === 0 ? (
        <AgentsEmptyState />
      ) : (
        <div className="space-y-4">
          {agents.map((agent) => (
            <AgentCard
              key={agent.id}
              agent={agent}
              onDelete={() => handleDelete(agent.email)}
              onUpdate={() => fetchAgents()}
            />
          ))}
        </div>
      )}
    </PageShell>
  );
}
