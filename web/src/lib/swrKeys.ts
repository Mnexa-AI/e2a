// Canonical SWR cache keys + invalidation helpers.
//
// SWR's mutate accepts a string, an array, or a predicate function.
// Centralizing key shapes here prevents drift between the consumer
// hooks (useSWR("agents", ...)) and the mutation sites that need to
// invalidate them (mutate("agents") after createAgent).
//
// Convention: single-resource lists use a bare string ("agents");
// parameterized resources use a tuple keyed by the params
// (["agent-messages", email, "all"]) so SWR caches separately by
// param combination.

import { mutate } from "swr";

// ── Keys ─────────────────────────────────────────────────

export const agentsKey = "agents";
export const domainsKey = "domains";
export const pendingMessagesKey = "pending-messages";

export const agentMessagesKey = (email: string, direction: string = "all", token?: string) =>
  ["agent-messages", email, direction, token ?? null] as const;

export const pendingMessageKey = (id: string) => ["pending-message", id] as const;
export const inboundMessageKey = (email: string, id: string) => ["inbound-message", email, id] as const;

// ── Invalidation helpers ─────────────────────────────────

// After any agent mutation (create/update/delete/test) the agents
// list — which carries the per-agent enrichment fields the dashboard
// renders — needs to refetch.
export function invalidateAgents() {
  return mutate(agentsKey);
}

// After approve / reject the user-wide pending list needs to drop
// the resolved row.
export function invalidatePendingList() {
  return mutate(pendingMessagesKey);
}

// After approve / reject of a specific message, the focus-page
// detail needs to refetch (status changes from pending_approval to
// sent/rejected, body may be scrubbed) AND the inbox view for that
// agent needs to drop the pending callout. Match every cached key
// that starts with "agent-messages" via the predicate form.
export function invalidateMessageDetail(id: string) {
  return mutate(pendingMessageKey(id));
}

export function invalidateAgentMessages(email: string) {
  return mutate(
    (key) =>
      Array.isArray(key) &&
      key[0] === "agent-messages" &&
      key[1] === email,
  );
}

export function invalidateDomains() {
  return mutate(domainsKey);
}
