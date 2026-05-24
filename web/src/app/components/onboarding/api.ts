// Thin fetch helpers for all onboarding-related API calls.
// Every function returns the parsed JSON or throws with the server error message.

import type {
  DomainInfo,
  AgentCreateResponse,
  AgentMode,
  UpdateAgentRequest,
} from "./types";
import type {
  DashboardAgent,
  ActivityEntry,
  InboundMessageDetail,
  ListMessagesResponse,
  PendingMessageSummary,
  PendingMessageDetail,
  PendingAttachment,
} from "../types";

/** Thrown by `request` on any non-2xx HTTP response. Carries the raw
 *  status code so callers can branch on 404 vs 500 vs 401 (the
 *  messages focus page uses this to distinguish "fall back to inbound
 *  endpoint" from "surface the real server error"). */
export class ApiError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    credentials: "include",
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(text || `Request failed (${res.status})`, res.status);
  }
  // Successful mutation endpoints may return no body.
  if (res.status === 204) return undefined as T;

  const textFn = (res as Response & { text?: () => Promise<string> }).text;
  if (typeof textFn === "function") {
    const text = await textFn.call(res);
    if (!text) return undefined as T;
    return JSON.parse(text) as T;
  }

  return res.json();
}

// ── Domains ──────────────────────────────────────────────

export async function listDomains(): Promise<DomainInfo[]> {
  const data = await request<{ domains: DomainInfo[] }>("/api/v1/domains");
  return data.domains ?? [];
}

export async function registerDomain(domain: string): Promise<DomainInfo> {
  return request<DomainInfo>("/api/v1/domains", {
    method: "POST",
    body: JSON.stringify({ domain }),
  });
}

export async function verifyDomain(
  domain: string,
): Promise<import("./types").VerifyDomainResponse> {
  return request("/api/v1/domains/" + encodeURIComponent(domain) + "/verify", {
    method: "POST",
  });
}

export async function deleteDomain(domain: string): Promise<void> {
  return request("/api/v1/domains/" + encodeURIComponent(domain), {
    method: "DELETE",
  });
}

// updateDomain hits PATCH /api/v1/domains/{domain}. Currently the
// only mutable field is is_primary — passing true promotes the domain
// (and atomically demotes any prior primary on the server side). The
// server rejects {is_primary: false} (to switch primary you promote
// a different domain instead) so we don't expose that case here.
export async function setDomainPrimary(domain: string): Promise<DomainInfo> {
  return request<DomainInfo>(
    "/api/v1/domains/" + encodeURIComponent(domain),
    {
      method: "PATCH",
      body: JSON.stringify({ is_primary: true }),
    },
  );
}

// ── Agents ───────────────────────────────────────────────

export async function listAgents(): Promise<DashboardAgent[]> {
  const data = await request<{ agents: DashboardAgent[] }>("/api/dashboard/agents");
  return data.agents ?? [];
}

export async function createAgent(params: {
  slug?: string;
  email?: string;
  name?: string;
  agent_mode: AgentMode;
  webhook_url?: string;
}): Promise<AgentCreateResponse> {
  return request<AgentCreateResponse>("/api/v1/agents", {
    method: "POST",
    body: JSON.stringify(params),
  });
}

export async function deleteAgent(email: string): Promise<void> {
  return request("/api/dashboard/agents/" + encodeURIComponent(email), {
    method: "DELETE",
  });
}

// Fires a synthetic "Test email from e2a" send to the agent's own
// address — exercises outbound SMTP + inbound delivery + webhook (or
// WebSocket for local agents). Used by the dashboard card's Test
// button. Routes through `request<T>` so 4xx/5xx surface as `ApiError`
// with the server's body text, consistent with every other mutation.
export async function sendAgentTestEmail(
  email: string,
): Promise<{ status: string; message_id: string }> {
  return request(
    "/api/v1/agents/" + encodeURIComponent(email) + "/test",
    { method: "POST" },
  );
}

// ── Agent activity (deprecated) ─────────────────────────

/**
 * @deprecated No web client reads this anymore. The dashboard agent
 * card used to expand an inline ActivityPanel that called this helper;
 * that panel was removed in favor of the threaded inbox at
 * `/dashboard/agents/messages`. The backend endpoint
 * `/api/dashboard/agents/{email}/activity` is also ready for deletion
 * — keep this helper around for one release in case external tooling
 * imported it via tree-shaken builds, then remove the helper + the
 * endpoint together in a follow-up.
 */
export async function getAgentActivity(email: string): Promise<ActivityEntry[]> {
  return request<ActivityEntry[]>(
    "/api/dashboard/agents/" + encodeURIComponent(email) + "/activity",
  );
}

// Dashboard inbox + SDK polling share this endpoint. SDK callers pass
// `direction=inbound` (the default); the dashboard inbox passes
// `direction=all` to fetch mixed inbound+outbound newest-first.
export async function listAgentMessages(
  email: string,
  opts: {
    direction?: "all" | "inbound" | "outbound";
    status?: "all" | "unread" | "read";
    pageSize?: number;
    token?: string;
  } = {},
): Promise<ListMessagesResponse> {
  const params = new URLSearchParams();
  if (opts.direction) params.set("direction", opts.direction);
  if (opts.status) params.set("status", opts.status);
  if (opts.pageSize) params.set("page_size", String(opts.pageSize));
  if (opts.token) params.set("token", opts.token);
  const qs = params.toString();
  return request<ListMessagesResponse>(
    "/api/v1/agents/" + encodeURIComponent(email) + "/messages" + (qs ? "?" + qs : ""),
  );
}

// Inbound focus-page payload. Note: the server flips inbox_status from
// "unread" to "read" as a side effect of this GET. The focus page
// always wants this; if a future consumer needs to preview without
// marking, add `?mark_read=false` to the backend.
export async function getInboundMessage(
  email: string,
  id: string,
): Promise<InboundMessageDetail> {
  return request<InboundMessageDetail>(
    "/api/v1/agents/" +
      encodeURIComponent(email) +
      "/messages/" +
      encodeURIComponent(id),
  );
}

// ── Agent update (general) ──────────────────────────────

export async function updateAgent(
  email: string,
  update: UpdateAgentRequest,
): Promise<void> {
  return request("/api/dashboard/agents/" + encodeURIComponent(email), {
    method: "PUT",
    body: JSON.stringify(update),
  });
}

// ── HITL pending messages ───────────────────────────────

export async function listPendingMessages(): Promise<PendingMessageSummary[]> {
  const data = await request<{ messages: PendingMessageSummary[] }>(
    "/api/v1/messages?status=pending_approval",
  );
  return data.messages ?? [];
}

export async function getPendingMessage(id: string): Promise<PendingMessageDetail> {
  return request<PendingMessageDetail>(
    "/api/v1/messages/" + encodeURIComponent(id),
  );
}

export type ApprovePayload = {
  subject?: string;
  body_text?: string;
  body_html?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  attachments?: PendingAttachment[];
};

export async function approvePendingMessage(
  id: string,
  overrides: ApprovePayload = {},
): Promise<{
  status: string;
  message_id: string;
  provider_message_id?: string;
  method?: string;
  edited?: boolean;
}> {
  return request(
    "/api/v1/messages/" + encodeURIComponent(id) + "/approve",
    {
      method: "POST",
      body: JSON.stringify(overrides),
    },
  );
}

export async function rejectPendingMessage(
  id: string,
  reason: string,
): Promise<{ status: string; message_id: string; rejection_reason?: string }> {
  return request(
    "/api/v1/messages/" + encodeURIComponent(id) + "/reject",
    {
      method: "POST",
      body: JSON.stringify({ reason }),
    },
  );
}
