"use client";

import { useState, useEffect, useMemo } from "react";
import useSWR from "swr";
import Link from "next/link";
import { useAuth } from "../../components/AuthProvider";
import { listDomains } from "../../components/onboarding/api";
import { useAgents } from "../../components/hooks/useAgents";
import { domainsKey } from "../../../lib/swrKeys";
import type {
  DashboardAgent,
  DashboardStats,
} from "../../components/types";
import type { DomainInfo } from "../../components/onboarding/types";
import { PageShell } from "../../components/loft/PageShell";
import { AgentsEmptyState } from "./_components/AgentsEmptyState";
import { AgentCard } from "./_components/AgentCard";

// Formats a percent-change number as a short delta string. 0 → null so
// the caller can hide the row entirely (no baseline → no arrow).
function formatDelta(pct: number): string | null {
  if (pct === 0) return null;
  const sign = pct > 0 ? "+" : "";
  return `${sign}${pct}%`;
}

// formatRelativeSeconds renders the "oldest pending" age. Falls back to
// the empty string when count is 0 so the caller can hide the line.
function formatPendingOldest(seconds: number): string {
  if (seconds <= 0) return "";
  if (seconds < 60) return `oldest in <1m`;
  const min = Math.floor(seconds / 60);
  if (min < 60) return `oldest in ${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `oldest in ${hr}h`;
  return `oldest in ${Math.floor(hr / 24)}d`;
}

// Stats strip — populated from GET /api/dashboard/stats. Mock specifies
// sans-serif numerals at 28px/600 (NOT editorial italic); deltas tint by
// tone (positive=success, negative=neutral, pending uses accent).
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
        // Swallow — null state renders "—" below. Don't crash the
        // dashboard if the stats endpoint is down or tracking disabled.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  type Card = {
    label: string;
    value: string;
    sub: string | null;
    tone: "success" | "info" | "accent" | "neutral";
  };
  const cards: Card[] = [
    {
      label: "Inbound · today",
      value: stats ? String(stats.today.inbound) : "—",
      sub: stats ? formatDelta(stats.today.inbound_delta_pct) : null,
      tone: "success",
    },
    {
      label: "Outbound · today",
      value: stats ? String(stats.today.outbound) : "—",
      sub: stats ? formatDelta(stats.today.outbound_delta_pct) : null,
      tone: "info",
    },
    {
      label: "Pending review",
      value: stats ? String(stats.pending.count) : "—",
      sub: stats && stats.pending.count > 0
        ? formatPendingOldest(stats.pending.oldest_seconds)
        : null,
      tone: "accent",
    },
    {
      label: "Delivery success",
      value: stats
        ? stats.delivery_success_pct > 0
          ? `${stats.delivery_success_pct}%`
          : "—"
        : "—",
      sub: stats ? `last ${stats.sample_window_days}d` : null,
      tone: "neutral",
    },
  ];

  const subColor = (tone: Card["tone"]) =>
    tone === "accent"
      ? "var(--accent-strong)"
      : tone === "success"
        ? "var(--success)"
        : "var(--fg-muted)";

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
            className="font-mono text-[10px] font-semibold uppercase mb-2"
            style={{
              color: "var(--fg-subtle)",
              letterSpacing: "0.08em",
            }}
          >
            {s.label}
          </div>
          <div
            className="text-[28px] font-semibold"
            style={{
              color: "var(--fg)",
              letterSpacing: "-0.02em",
              lineHeight: 1,
              marginBottom: 6,
            }}
          >
            {s.value}
          </div>
          {s.sub && (
            <div className="text-[11px]" style={{ color: subColor(s.tone) }}>
              {s.sub}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

// Filter chips + sort dropdown. Counts are derived client-side from the
// agents list — the backend doesn't need to compute filter aggregates.
// "Sort: last activity" uses created_at descending as a proxy; the
// label is honest about what's available.
type Filter = "all" | "cloud" | "local" | "hitl" | "unverified";
type SortKey = "recent" | "name";

function FilterBar({
  agents,
  filter,
  setFilter,
  sort,
  setSort,
}: {
  agents: DashboardAgent[];
  filter: Filter;
  setFilter: (f: Filter) => void;
  sort: SortKey;
  setSort: (s: SortKey) => void;
}) {
  const counts = {
    all: agents.length,
    cloud: agents.filter((a) => a.agent_mode !== "local").length,
    local: agents.filter((a) => a.agent_mode === "local").length,
    hitl: agents.filter((a) => a.hitl_enabled).length,
    unverified: agents.filter((a) => !a.domain_verified).length,
  };
  const chips: { key: Filter; label: string; count: number }[] = [
    { key: "all", label: "All", count: counts.all },
    { key: "cloud", label: "Cloud", count: counts.cloud },
    { key: "local", label: "Local", count: counts.local },
    { key: "hitl", label: "HITL on", count: counts.hitl },
    { key: "unverified", label: "Unverified", count: counts.unverified },
  ];

  return (
    <div className="flex items-center gap-2 mb-3.5 flex-wrap">
      {chips.map((c) => {
        const active = filter === c.key;
        return (
          <button
            key={c.key}
            onClick={() => setFilter(c.key)}
            className="text-[12px] font-medium px-3 py-1 transition"
            style={{
              borderRadius: 999,
              background: active ? "var(--fg)" : "var(--bg-panel)",
              color: active ? "var(--bg)" : "var(--fg-muted)",
              border: active
                ? "1px solid var(--fg)"
                : "1px solid var(--border)",
            }}
          >
            {c.label} {c.count}
          </button>
        );
      })}
      <span className="flex-1" />
      <label
        className="font-mono text-[11px] flex items-center gap-1.5"
        style={{ color: "var(--fg-subtle)", letterSpacing: "0.02em" }}
      >
        Sort:
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value as SortKey)}
          className="font-mono text-[11px] bg-transparent border-none cursor-pointer"
          style={{ color: "var(--fg-muted)" }}
        >
          <option value="recent">last activity ▾</option>
          <option value="name">name ▾</option>
        </select>
      </label>
    </div>
  );
}

export default function DashboardPage() {
  const { user } = useAuth();
  // Both feeds are read through SWR so an agent edit on the Settings
  // page (which invalidates `agentsKey`) flows back into this view
  // without a manual refetch.
  const { agents, error: agentsError, isLoading: agentsLoading } = useAgents();
  const { data: domains = [] } = useSWR(domainsKey, () =>
    listDomains().catch(() => [] as DomainInfo[]),
  );
  const error = agentsError ? agentsError.message || "Failed to load agents" : "";
  const loading = agentsLoading;
  const [filter, setFilter] = useState<Filter>("all");
  const [sort, setSort] = useState<SortKey>("recent");

  // Delete moved to /dashboard/agents/settings → Danger zone. The
  // dashboard no longer needs a per-card delete handler; the settings
  // page calls deleteAgent + invalidateAgents() and routes back here,
  // which causes useAgents() to refetch and the new list to render.

  // Derived: filtered + sorted agent list.
  const visibleAgents = useMemo(() => {
    let out = agents;
    switch (filter) {
      case "cloud":
        out = out.filter((a) => a.agent_mode !== "local");
        break;
      case "local":
        out = out.filter((a) => a.agent_mode === "local");
        break;
      case "hitl":
        out = out.filter((a) => a.hitl_enabled);
        break;
      case "unverified":
        out = out.filter((a) => !a.domain_verified);
        break;
    }
    if (sort === "recent") {
      // created_at descending as a stand-in for last activity.
      out = [...out].sort(
        (a, b) =>
          new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
      );
    } else {
      out = [...out].sort((a, b) =>
        (a.name || a.email).localeCompare(b.name || b.email),
      );
    }
    return out;
  }, [agents, filter, sort]);

  // Meta line: "N agents · M verified domains · indexed <relative> ago"
  const [indexedAt, setIndexedAt] = useState<number>(Date.now());
  useEffect(() => {
    setIndexedAt(Date.now());
  }, [agents, domains]);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 5000);
    return () => clearInterval(id);
  }, []);
  const indexedAgo = useMemo(() => {
    const sec = Math.max(1, Math.floor((Date.now() - indexedAt) / 1000));
    if (sec < 60) return `${sec}s`;
    const min = Math.floor(sec / 60);
    if (min < 60) return `${min}m`;
    return `${Math.floor(min / 60)}h`;
  }, [indexedAt, tick]); // eslint-disable-line react-hooks/exhaustive-deps

  const verifiedDomains = domains.filter((d) => d.verified).length;
  const agentLabel = agents.length === 1 ? "agent" : "agents";
  const domainLabel = verifiedDomains === 1 ? "verified domain" : "verified domains";

  return (
    <PageShell
      crumbs={["Agents"]}
      eyebrow="Workspace"
      title={<>Agents</>}
      subtitle={
        <>
          {agents.length} {agentLabel} · {verifiedDomains} {domainLabel} ·
          indexed{" "}
          <span style={{ fontFamily: "var(--f-mono)" }}>
            {indexedAgo} ago
          </span>{" "}
          · signed in as{" "}
          <span style={{ color: "var(--fg)", fontWeight: 500 }}>
            {user?.email}
          </span>
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
            <span className="font-mono">+</span> Create agent
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
        <>
          <FilterBar
            agents={agents}
            filter={filter}
            setFilter={setFilter}
            sort={sort}
            setSort={setSort}
          />
          <div className="space-y-4">
            {visibleAgents.length === 0 ? (
              <p
                className="text-[13px] py-8 text-center"
                style={{ color: "var(--fg-muted)" }}
              >
                No agents match this filter.
              </p>
            ) : (
              visibleAgents.map((agent) => (
                <AgentCard key={agent.id} agent={agent} />
              ))
            )}
          </div>
        </>
      )}
    </PageShell>
  );
}
