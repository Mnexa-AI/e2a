"use client";

// Shared SWR hook for `GET /v1/agents` — the per-user inbox list
// (slim identity: id/domain/email/name/domain_verified/created_at).
//
// Called from three surfaces:
//   • /dashboard           — the agent grid
//   • /dashboard/agents/*  — the layout's AgentHeader lookup
//   • /dashboard/agents/settings — the per-agent editor section
//
// Without SWR each of those would fire its own GET on every mount.
// With SWR they share the cache (same `agentsKey`) and one
// invalidation (`invalidateAgents()` after create/update/delete) keeps
// all three in sync.

import useSWR from "swr";
import { listAgents } from "../onboarding/api";
import { agentsKey } from "../../../lib/swrKeys";
import type { DashboardAgent } from "../types";

export function useAgents(): {
  agents: DashboardAgent[];
  error: Error | undefined;
  isLoading: boolean;
} {
  const { data, error, isLoading } = useSWR(agentsKey, () => listAgents());
  return {
    agents: data ?? [],
    error,
    isLoading,
  };
}
