"use client";

import { useState, useEffect, useCallback } from "react";
import Link from "next/link";
import { useAuth } from "../../components/AuthProvider";
import { listAgents, deleteAgent } from "../../components/onboarding/api";
import type { DashboardAgent, DashboardStats } from "../../components/types";
import { PageShell } from "../../components/loft/PageShell";
import { AgentsEmptyState } from "./_components/AgentsEmptyState";
import { AgentCard } from "./_components/AgentCard";

// Formats a percent-change number as a short delta string. 0 → null so
// the caller can hide the row entirely (no baseline → no arrow).
function formatDelta(pct: number): string | null {
  if (pct === 0) return null;
  const sign = pct > 0 ? "+" : "";
  return `${sign}${pct}% vs yesterday`;
}

// Stats strip — populated from GET /api/dashboard/stats. Zero counts and
// missing baselines are rendered as bare numbers (no delta arrow) so the
// cards stay sensible on deployments without usage tracking enabled.
function StatsStrip() {
  const [stats, setStats] = useState<DashboardStats | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/dashboard/stats")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (!cancelled) setStats(data);
      })
      .catch(() => {
        // Swallow — leaves stats=null, which renders "—" below. The
        // dashboard load shouldn't fail because the stats endpoint is
        // down or the user has tracking disabled.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const cards = [
    {
      label: "Inbound today",
      value: stats ? String(stats.today.inbound) : "—",
      delta: stats ? formatDelta(stats.today.inbound_delta_pct) : null,
    },
    {
      label: "Outbound today",
      value: stats ? String(stats.today.outbound) : "—",
      delta: stats ? formatDelta(stats.today.outbound_delta_pct) : null,
    },
    {
      label: "Pending review",
      value: stats ? String(stats.pending.count) : "—",
      delta: null as string | null,
    },
    {
      label: "Delivery success",
      value: stats
        ? stats.delivery_success_pct > 0
          ? `${stats.delivery_success_pct}%`
          : "—"
        : "—",
      delta: stats ? `last ${stats.sample_window_days}d` : null,
    },
  ];

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3 mb-6">
      {cards.map((s) => (
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
