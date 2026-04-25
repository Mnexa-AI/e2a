"use client";

import { useState, useEffect, useCallback } from "react";
import Link from "next/link";
import { useAuth } from "../../components/AuthProvider";
import { listAgents, deleteAgent } from "../../components/onboarding/api";
import type { DashboardAgent } from "../../components/types";
import { AgentsEmptyState } from "./_components/AgentsEmptyState";
import { AgentCard } from "./_components/AgentCard";

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
    <>
      <div className="flex items-start justify-between mb-8">
        <div>
          <h2 className="text-2xl font-bold tracking-tight mb-2">Agents</h2>
          <p className="text-muted">
            Manage your registered agents. Signed in as{" "}
            <span className="font-medium text-foreground">{user?.email}</span>.
          </p>
        </div>
        {agents.length > 0 && (
          <Link
            href="/get-started"
            className="text-sm px-3 py-1.5 bg-foreground text-background rounded-md hover:opacity-90 transition shrink-0"
          >
            Create agent
          </Link>
        )}
      </div>

      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
        </div>
      )}

      {loading ? (
        <div className="text-sm text-muted py-12 text-center">Loading...</div>
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
    </>
  );
}
