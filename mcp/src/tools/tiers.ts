// Tool tier map — the single source of truth for §6a scope-gating.
//
// The MCP surface splits by persona, exposed per credential scope (§5/§6a):
//   - runtime (scope=agent): what a deployed agent uses every turn. An
//     `agent`-scoped credential sees ONLY these.
//   - admin   (scope=account): provisioning/setup. An `account`-scoped
//     credential sees runtime + admin (the full surface).
//
// This is a UX / decision-space optimization, NOT the security boundary — the
// backend enforces scope per-handler regardless (an agent-scoped credential is
// 403'd on account ops even if a tool were mis-listed). Keeping the classifica-
// tion here (one auditable place) mirrors the design's "the drift-gate map
// records each tool's tier next to its operationId".
//
// INVARIANT: every registered tool name MUST appear in exactly one set below.
// A new tool with no tier is a bug — `assertToolTiersComplete` (tested) guards it.

/** Runtime/inbox tools — visible to BOTH agent- and account-scoped credentials. */
export const RUNTIME_TOOLS: ReadonlySet<string> = new Set([
  "whoami",
  "list_agents",
  "get_agent",
  "list_messages",
  "get_message",
  "get_attachment",
  "update_message_labels",
  "list_conversations",
  "get_conversation",
  "send_message",
  "reply_to_message",
  "forward_message",
  "approve_message",
  "reject_message",
  "list_pending_messages",
  "get_pending_message",
]);

/** Admin/setup tools — visible ONLY to account-scoped credentials. */
export const ADMIN_TOOLS: ReadonlySet<string> = new Set([
  "create_agent",
  "update_agent",
  "delete_agent",
  "list_domains",
  "get_domain",
  "register_domain",
  "verify_domain",
  "delete_domain",
  "list_webhooks",
  "get_webhook",
  "create_webhook",
  "update_webhook",
  "delete_webhook",
  "rotate_webhook_secret",
  "test_webhook",
  "list_webhook_deliveries",
  "list_events",
  "get_event",
  "redeliver_event",
]);

export type Scope = "account" | "agent";

/**
 * The set of tool names a given credential scope may see.
 * - `agent`   → runtime only.
 * - `account` → runtime + admin (full surface).
 * Any unrecognized scope falls back to runtime (least privilege).
 */
export function toolNamesForScope(scope: Scope | string): ReadonlySet<string> {
  if (scope === "account") {
    return new Set([...RUNTIME_TOOLS, ...ADMIN_TOOLS]);
  }
  // agent, or anything unexpected → least privilege.
  return RUNTIME_TOOLS;
}

/** True if `name` is allowed for `scope`. */
export function toolAllowedForScope(name: string, scope: Scope | string): boolean {
  return toolNamesForScope(scope).has(name);
}
