// Thin fetch helpers for all onboarding-related API calls.
// Every function returns the parsed JSON or throws with the server error message.

import type {
  DomainInfo,
  AgentCreateResponse,
  ProtectionConfig,
} from "./types";
import type {
  DashboardAgent,
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
  const data = await request<{ items: DomainInfo[] }>("/v1/domains");
  return data.items ?? [];
}

export async function registerDomain(domain: string): Promise<DomainInfo> {
  return request<DomainInfo>("/v1/domains", {
    method: "POST",
    body: JSON.stringify({ domain }),
  });
}

export async function verifyDomain(
  domain: string,
): Promise<import("./types").VerifyDomainResponse> {
  return request("/v1/domains/" + encodeURIComponent(domain) + "/verify", {
    method: "POST",
  });
}

export async function deleteDomain(domain: string): Promise<void> {
  return request("/v1/domains/" + encodeURIComponent(domain), {
    method: "DELETE",
  });
}

// updateDomain hits PATCH /v1/domains/{domain}. Currently the
// only mutable field is is_primary — passing true promotes the domain
// (and atomically demotes any prior primary on the server side). The
// server rejects {is_primary: false} (to switch primary you promote
// a different domain instead) so we don't expose that case here.
export async function setDomainPrimary(domain: string): Promise<DomainInfo> {
  return request<DomainInfo>(
    "/v1/domains/" + encodeURIComponent(domain),
    {
      method: "PATCH",
      body: JSON.stringify({ is_primary: true }),
    },
  );
}

// ── Agents ───────────────────────────────────────────────

// GET /v1/agents → PageAgentView. AgentView carries exactly the slim
// identity fields the dashboard list needs (id/domain/email/name/
// domain_verified/created_at), so the wire rows map straight onto
// DashboardAgent. (Per-agent config moved to the protection sub-resource.)
export async function listAgents(): Promise<DashboardAgent[]> {
  const data = await request<{ items?: DashboardAgent[] | null }>("/v1/agents");
  return data.items ?? [];
}

export async function createAgent(params: {
  slug?: string;
  email?: string;
  name?: string;
}): Promise<AgentCreateResponse> {
  return request<AgentCreateResponse>("/v1/agents", {
    method: "POST",
    body: JSON.stringify(params),
  });
}

// DELETE /v1/agents/{email}. The v1 surface guards destructive deletes
// behind an explicit `?confirm=DELETE` query param and returns 204 (no
// body) on success — `request<T>` maps that to undefined.
export async function deleteAgent(email: string): Promise<void> {
  return request(
    "/v1/agents/" + encodeURIComponent(email) + "?confirm=DELETE",
    { method: "DELETE" },
  );
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
    "/v1/agents/" + encodeURIComponent(email) + "/test",
    { method: "POST" },
  );
}

// Wire shape of a row in PageMessageSummaryView.items
// (GET /v1/agents/{address}/messages). Mirrors MessageSummaryView in
// api/openapi.yaml. Kept local; the dashboard projects it into the
// MessageSummary type the UI reads.
type MessageSummaryWire = {
  message_id: string;
  direction: "inbound" | "outbound";
  from: string;
  to?: string[] | null;
  cc?: string[] | null;
  reply_to?: string[] | null;
  recipient: string;
  subject: string;
  conversation_id?: string;
  // v1 splits message state into delivery rollup (delivery_status) and the
  // review/HITL lifecycle (review_status). Both optional on the wire.
  delivery_status?: string;
  review_status?: string;
  read_status?: string;
  webhook_status?: string;
  webhook_error?: string;
  size_bytes?: number;
  created_at: string;
};

type PageMessageSummaryWire = {
  items?: MessageSummaryWire[] | null;
  next_cursor?: string | null;
};

function projectSummary(w: MessageSummaryWire): import("../types").MessageSummary {
  return {
    message_id: w.message_id,
    direction: w.direction,
    from: w.from,
    to: w.to ?? [],
    cc: w.cc ?? undefined,
    reply_to: w.reply_to ?? undefined,
    recipient: w.recipient,
    subject: w.subject,
    conversation_id: w.conversation_id,
    // App keeps `status` (delivery rollup) + `review_status` (review
    // lifecycle) field names; v1 sources them from delivery_status /
    // review_status.
    status: w.delivery_status ?? "",
    review_status: w.review_status,
    // Inbound unread state lives in read_status on v1 (delivery_status is
    // outbound-only); the inbox's unread affordance reads this.
    read_status: w.read_status,
    webhook_status: w.webhook_status,
    webhook_error: w.webhook_error,
    size_bytes: w.size_bytes,
    created_at: w.created_at,
  };
}

// Dashboard inbox + SDK polling share this endpoint
// (GET /v1/agents/{address}/messages). Cursor pagination: pass `cursor`
// (the prior page's next_cursor) to fetch the next page. `direction=all`
// fetches mixed inbound+outbound newest-first.
export async function listAgentMessages(
  email: string,
  opts: {
    direction?: "all" | "inbound" | "outbound";
    status?: "all" | "unread" | "read";
    pageSize?: number;
    cursor?: string;
  } = {},
): Promise<ListMessagesResponse> {
  const params = new URLSearchParams();
  if (opts.direction) params.set("direction", opts.direction);
  // `status` is inbound-only in /v1; sending it on direction=all/outbound
  // is harmless but we only forward it when meaningful.
  if (opts.status && opts.direction !== "outbound") params.set("status", opts.status);
  if (opts.pageSize) params.set("limit", String(opts.pageSize));
  if (opts.cursor) params.set("cursor", opts.cursor);
  const qs = params.toString();
  const page = await request<PageMessageSummaryWire>(
    "/v1/agents/" + encodeURIComponent(email) + "/messages" + (qs ? "?" + qs : ""),
  );
  return {
    items: (page.items ?? []).map(projectSummary),
    next_cursor: page.next_cursor ?? null,
  };
}

// Wire shape of MessageView (GET /v1/agents/{address}/messages/{id}).
type MessageViewWire = {
  message_id: string;
  from: string;
  to?: string[] | null;
  cc?: string[] | null;
  reply_to?: string[] | null;
  recipient: string;
  subject: string;
  conversation_id?: string;
  direction?: "inbound" | "outbound";
  delivery_status?: string;
  review_status?: string;
  read_status?: string;
  created_at: string;
  auth_headers?: Record<string, string>;
  body?: { text?: string; html?: string };
  raw_message?: string;
};

// Projects a MessageView into the PendingMessageDetail shape the review
// surfaces read. Fields the `/v1` MessageView doesn't expose
// (attachments, the parent inbound context, the reviewer identity) come
// through undefined — the UI degrades gracefully, hiding those
// affordances.
function projectPending(
  email: string,
  w: MessageViewWire,
): PendingMessageDetail {
  return {
    id: w.message_id,
    agent_email: email,
    direction: "outbound",
    subject: w.subject,
    conversation_id: w.conversation_id,
    to: w.to ?? [],
    cc: w.cc ?? undefined,
    // A held draft's lifecycle state is the review_status (pending_review);
    // the delivery rollup is empty until it's approved + sent.
    status: w.review_status ?? "",
    created_at: w.created_at,
    body_text: w.body?.text,
    body_html: w.body?.html,
  };
}

// Pending-draft detail. `/v1` has no bare-id endpoint, so callers must
// thread the owning agent's address. Built from the agent-scoped
// MessageView.
export async function getPendingMessage(
  email: string,
  id: string,
): Promise<PendingMessageDetail> {
  const w = await request<MessageViewWire>(
    "/v1/agents/" +
      encodeURIComponent(email) +
      "/messages/" +
      encodeURIComponent(id),
  );
  return projectPending(email, w);
}

// Combined detail fetch for the focus page. `/v1` returns one MessageView
// for both directions, and that detail shape has NO `direction` field —
// it also drops `from`/`status` to empty strings on outbound rows, so the
// direction CANNOT be recovered from the detail payload. The authoritative
// direction lives on the MessageSummaryView list row, so the focus page
// threads it in (via the `?direction=` query param) and passes it here.
// When the caller can't supply a direction (a deep link with no param),
// we fall back to inbound — the safe default that never offers
// approve/reject on a message we can't prove is a held outbound draft.
// Returns both projections under a discriminated `direction` so the focus
// page can keep its existing inbound/outbound branches.
export async function getMessageDetail(
  email: string,
  id: string,
  direction: "inbound" | "outbound" = "inbound",
):
  | Promise<
      | { direction: "outbound"; data: PendingMessageDetail }
      | { direction: "inbound"; data: InboundMessageDetail }
    > {
  const w = await request<MessageViewWire>(
    "/v1/agents/" +
      encodeURIComponent(email) +
      "/messages/" +
      encodeURIComponent(id),
  );
  if (direction === "outbound") {
    return { direction: "outbound", data: projectPending(email, w) };
  }
  return {
    direction: "inbound",
    data: {
      message_id: w.message_id,
      from: w.from,
      to: w.to ?? [],
      cc: w.cc ?? [],
      reply_to: w.reply_to ?? [],
      recipient: w.recipient,
      subject: w.subject,
      conversation_id: w.conversation_id ?? "",
      status: w.delivery_status ?? "",
      created_at: w.created_at,
      auth_headers: w.auth_headers ?? {},
      body: w.body,
      raw_message: w.raw_message ?? "",
    },
  };
}

// ── Protection config (review-queue holds) ──────────────

// GET /v1/agents/{address}/protection — the agent's protection posture
// (inbound/outbound trust gate + content scan + the review-hold queue).
// Account-scope only; the dashboard's session cookie qualifies. Beta:
// the shape may change before it's declared stable.
export async function getProtection(email: string): Promise<ProtectionConfig> {
  return request<ProtectionConfig>(
    "/v1/agents/" + encodeURIComponent(email) + "/protection",
  );
}

// Replace the agent's full protection posture. The PUT is a wholesale
// replace (inbound/outbound/holds all required), so callers send the
// complete config — the ProtectionEditor edits every section in one form
// and submits the whole thing, which matches this contract exactly.
export async function setProtection(
  email: string,
  config: ProtectionConfig,
): Promise<void> {
  return request(
    "/v1/agents/" + encodeURIComponent(email) + "/protection",
    { method: "PUT", body: JSON.stringify(config) },
  );
}

// ── HITL pending messages ───────────────────────────────

// Wire shape of an agent row in PageAgentView.items (GET /v1/agents).
type AgentViewWire = {
  email: string;
};

// `/v1` has no cross-account pending endpoint. Pending drafts are
// outbound messages whose review lifecycle is "pending_review", scoped
// per agent. NOTE: on MessageSummaryView the review state lives in
// `review_status` (projected to the app's `review_status`), NOT
// `delivery_status` — the delivery rollup on a held draft is empty. We
// fan out over the account's agents, list each agent's outbound
// messages, keep the rows whose review status is pending, and tag each
// with the owning agent's address so the detail/approve/reject calls can
// be addressed. Aggregated newest-first. (Per-agent review config lives
// on the /protection sub-resource now — there is no agent-level flag to
// pre-filter on, so we query every agent.)
export async function listPendingMessages(): Promise<PendingMessageSummary[]> {
  const agentsResp = await request<{ items?: AgentViewWire[] | null }>("/v1/agents");
  const agents = agentsResp.items ?? [];
  const perAgent = await Promise.all(
    agents.map(async (a) => {
      try {
        const page = await listAgentMessages(a.email, {
          direction: "outbound",
          pageSize: 100,
        });
        return page.items
          .filter((m) => m.review_status === "pending_review")
          .map<PendingMessageSummary>((m) => ({
            id: m.message_id,
            agent_email: a.email,
            direction: "outbound",
            subject: m.subject,
            conversation_id: m.conversation_id,
            to: m.to ?? [],
            cc: m.cc,
            // Surface the HITL lifecycle value as the row's `status` —
            // the wire `status` is the (empty) delivery rollup for a
            // held draft, so the pending UI keys off review_status here.
            status: m.review_status ?? "",
            created_at: m.created_at,
          }));
      } catch {
        // One agent failing (e.g. transient 5xx) shouldn't blank the
        // whole queue — drop its rows and surface the rest.
        return [] as PendingMessageSummary[];
      }
    }),
  );
  return perAgent
    .flat()
    .sort(
      (a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    );
}

export type ApprovePayload = {
  subject?: string;
  body?: string;
  html_body?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  attachments?: PendingAttachment[];
};

// approvePendingMessage / rejectPendingMessage hit the agent-scoped
// HITL endpoints. The backend validates that {agentEmail} matches the
// message's owning agent — pass the message's agent_id directly (no
// pre-flight lookup needed; the focus page and pending detail panel
// already have the loaded message in scope).
export async function approvePendingMessage(
  agentEmail: string,
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
    "/v1/agents/" +
      encodeURIComponent(agentEmail) +
      "/messages/" +
      encodeURIComponent(id) +
      "/approve",
    {
      method: "POST",
      body: JSON.stringify(overrides),
    },
  );
}

export async function rejectPendingMessage(
  agentEmail: string,
  id: string,
  reason: string,
): Promise<{ status: string; message_id: string; rejection_reason?: string }> {
  return request(
    "/v1/agents/" +
      encodeURIComponent(agentEmail) +
      "/messages/" +
      encodeURIComponent(id) +
      "/reject",
    {
      method: "POST",
      body: JSON.stringify({ reason }),
    },
  );
}
