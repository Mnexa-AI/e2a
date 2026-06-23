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

// Per-agent protection config (GET /v1/agents/{address}/protection),
// keyed by the owning agent's email. The inbox-settings Review-queue
// editor reads + writes the `holds` section through this key.
export const protectionKey = (email: string) =>
  ["protection", email] as const;

// Key tuple includes every filter that affects the response so two
// surfaces fetching the same email under different filters don't
// poison each other's cache. Pre-fix the key omitted `status`, so a
// future surface filtering by status would collide with the existing
// all-status inbox view.
export const agentMessagesKey = (
  email: string,
  direction: string = "all",
  status: string = "all",
  token?: string,
) =>
  ["agent-messages", email, direction, status, token ?? null] as const;

// Agent-scoped in /v1: the detail fetch is GET /v1/agents/{address}/
// messages/{id}, so the cache key carries the owning agent's email.
export const pendingMessageKey = (email: string, id: string) =>
  ["pending-message", email, id] as const;

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
// sent/rejected, body may be scrubbed).
//
// Caveat: this only mutates the outbound (`pending-message`) key
// because every mutation we ship today goes against a pending-
// approval outbound row. If we ever start mutating inbound rows
// (e.g. mark-as-unread), this helper needs a second mutate for
// `inbound-message`.
//
// Matches by message id regardless of which agent's email keyed the
// fetch, so callers that don't have the agent address in scope (the
// pending page's blanket refresh) can still drop the right entry.
export function invalidateMessageDetail(id: string) {
  return mutate(
    (key) =>
      Array.isArray(key) && key[0] === "pending-message" && key[2] === id,
  );
}

export function invalidateAgentMessages(email: string) {
  return mutate(
    (key) =>
      Array.isArray(key) &&
      key[0] === "agent-messages" &&
      key[1] === email,
  );
}

// Variant for invalidating EVERY cached inbox query at once. Used by
// the user-wide pending page, which doesn't know which specific
// agent's inbox is open elsewhere in the dashboard. A small
// over-invalidation: every per-agent inbox view refetches on next
// render. Cheap relative to the alternative ("Sidebar / inbox stale
// until the user navigates").
export function invalidateAllAgentMessages() {
  return mutate(
    (key) => Array.isArray(key) && key[0] === "agent-messages",
  );
}

export function invalidateDomains() {
  return mutate(domainsKey);
}

// After the Review-queue editor saves a new TTL / on-expiry, the per-
// agent protection fetch needs to refetch so the collapsed summary
// reflects the change.
export function invalidateProtection(email: string) {
  return mutate(protectionKey(email));
}
