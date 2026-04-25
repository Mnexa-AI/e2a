"use client";

import { useState, useEffect, useCallback } from "react";
import { DomainList } from "./_components/DomainList";
import { AddDomainForm } from "./_components/AddDomainForm";
import { listDomains, listAgents } from "../../components/onboarding/api";
import type { DomainInfo } from "../../components/onboarding/types";
import type { DashboardAgent } from "../../components/types";

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
      <>
        <h2 className="text-2xl font-bold tracking-tight mb-2">Domains</h2>
        <p className="text-muted mb-8">
          Add a domain to create branded agent addresses.
        </p>

        <div className="border border-border rounded-lg p-8 text-center space-y-4">
          <p className="text-sm text-muted">
            You don&apos;t have any domains yet.
          </p>
          <div className="flex flex-col items-center gap-3">
            <button
              onClick={() => setShowAddForm(true)}
              className="px-4 py-2 bg-foreground text-background rounded-lg text-sm font-medium hover:opacity-90 transition"
            >
              Add domain
            </button>
            <a
              href="/get-started?mode=shared"
              className="text-sm text-accent hover:underline"
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
      </>
    );
  }

  return (
    <>
      <div className="flex items-center justify-between mb-2">
        <h2 className="text-2xl font-bold tracking-tight">Domains</h2>
        <button
          onClick={() => setShowAddForm(!showAddForm)}
          className="px-3 py-1.5 bg-foreground text-background rounded-lg text-sm font-medium hover:opacity-90 transition"
        >
          {showAddForm ? "Cancel" : "Add domain"}
        </button>
      </div>
      <p className="text-muted mb-8">
        Manage your domains. Verified domains can host agent email addresses.
      </p>

      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
          {error}
        </div>
      )}

      {showAddForm && (
        <div className="mb-6">
          <AddDomainForm onRegistered={handleDomainRegistered} />
        </div>
      )}

      {loading ? (
        <div className="text-sm text-muted py-12 text-center">Loading...</div>
      ) : (
        <DomainList
          domains={domains}
          agents={agents}
          onRefresh={fetchData}
        />
      )}
    </>
  );
}
