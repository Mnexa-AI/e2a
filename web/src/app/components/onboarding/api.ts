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
  PendingMessageSummary,
  PendingMessageDetail,
  PendingAttachment,
} from "../types";

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    credentials: "include",
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Request failed (${res.status})`);
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

// ── Agent activity ──────────────────────────────────────

export async function getAgentActivity(email: string): Promise<ActivityEntry[]> {
  return request<ActivityEntry[]>(
    "/api/dashboard/agents/" + encodeURIComponent(email) + "/activity",
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
