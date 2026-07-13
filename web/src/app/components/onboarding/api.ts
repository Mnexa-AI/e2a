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

// DELETE /v1/domains/{domain}. The v1 surface guards destructive deletes
// behind an explicit `?confirm=DELETE` query param.
export async function deleteDomain(domain: string): Promise<void> {
  return request("/v1/domains/" + encodeURIComponent(domain) + "?confirm=DELETE", {
    method: "DELETE",
  });
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

// POST /v1/agents registers by full email only — a shared-domain agent is
// an email on the deployment's shared domain; the legacy `slug` field was
// dropped from the API and is rejected as an unexpected property.
export async function createAgent(params: {
  email: string;
  name?: string;
}): Promise<AgentCreateResponse> {
  return request<AgentCreateResponse>("/v1/agents", {
    method: "POST",
    body: JSON.stringify(params),
  });
}

// DELETE /v1/agents/{email}. The v1 surface guards destructive deletes
// behind an explicit `?confirm=DELETE` query param and returns 200 with a
// deletion receipt ({deleted:true, email, messages_deleted}); the dashboard
// doesn't consume it, so this stays Promise<void>.
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
  id: string;
  direction: "inbound" | "outbound";
  from: string;
  to?: string[] | null;
  cc?: string[] | null;
  reply_to?: string[] | null;
  delivered_to: string;
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
    id: w.id,
    direction: w.direction,
    from: w.from,
    to: w.to ?? [],
    cc: w.cc ?? undefined,
    reply_to: w.reply_to ?? undefined,
    recipient: w.delivered_to,
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

// Max unread we count before showing "N+" — keeps the per-card probe a
// small fixed page instead of walking the whole unread backlog.
export const UNREAD_BADGE_CAP = 99;

// Inbox-level unread rollup for the Inboxes list. Asks the messages
// endpoint for unread inbound rows (read_status=unread is the backend's
// inbound read-state filter — MSG-1) and reports how many there are. We
// pull one capped page (limit=CAP) and treat a returned cursor as "more
// than CAP" so the card can render "99+". One call per inbox card
// (Option A); a native per-agent unread count on GET /v1/agents would
// replace this later.
export async function getInboxUnread(
  email: string,
): Promise<{ count: number; more: boolean }> {
  const page = await request<PageMessageSummaryWire>(
    "/v1/agents/" +
      encodeURIComponent(email) +
      "/messages?direction=inbound&read_status=unread&limit=" +
      UNREAD_BADGE_CAP,
  );
  const count = (page.items ?? []).length;
  return { count, more: Boolean(page.next_cursor) };
}

// Wire shape of MessageView (GET /v1/agents/{address}/messages/{id}).
type MessageViewWire = {
  id: string;
  from: string;
  to?: string[] | null;
  cc?: string[] | null;
  reply_to?: string[] | null;
  delivered_to: string;
  subject: string;
  conversation_id?: string;
  direction?: "inbound" | "outbound";
  delivery_status?: string;
  review_status?: string;
  read_status?: string;
  created_at: string;
  auth_headers?: Record<string, string>;
  body?: { text?: string; html?: string };
  // Backend-derived body: `text` (injection-reduced) for any message with raw
  // MIME, plus `html` (decoded text/html part) for rich display when present.
  parsed?: { text?: string; html?: string };
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
    id: w.id,
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
    // Outbound drafts carry an editable `body`; sent outbound and inbound holds
    // carry the content as `parsed` (the draft columns are scrubbed at send).
    // Fall back so the body shows either way.
    body_text: w.body?.text ?? w.parsed?.text,
    body_html: w.body?.html ?? w.parsed?.html,
  };
}

// Held-message detail for the review queue: GET /v1/reviews/{id}
// (account-scoped; id is globally unique so no agent address is needed).
// `email` is retained only for the SWR cache key. Built from the
// MessageView the endpoint returns.
export async function getPendingMessage(
  email: string,
  id: string,
): Promise<PendingMessageDetail> {
  const w = await request<MessageViewWire>(
    "/v1/reviews/" + encodeURIComponent(id),
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
      id: w.id,
      from: w.from,
      to: w.to ?? [],
      cc: w.cc ?? [],
      reply_to: w.reply_to ?? [],
      recipient: w.delivered_to,
      subject: w.subject,
      conversation_id: w.conversation_id ?? "",
      status: w.delivery_status ?? "",
      created_at: w.created_at,
      auth_headers: w.auth_headers ?? {},
      parsed: w.parsed,
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

// Wire shape of a row in the review queue (GET /v1/reviews → { items }).
// Mirrors ReviewView in api/openapi.yaml. The review queue is the
// account-scoped operator surface for holds of BOTH directions (outbound
// drafts awaiting send + inbound messages held by a screening gate).
type ReviewWire = {
  id: string;
  agent: string;
  direction: "inbound" | "outbound";
  from: string;
  to?: string[] | null;
  subject: string;
  conversation_id?: string;
  review_status: string;
  created_at: string;
};

// The pending-review queue: one account-scoped call to GET /v1/reviews
// returns every hold (both directions) across the account's inboxes,
// newest-first. (Replaces the old per-agent fan-out over /messages — the
// dedicated /reviews resource is account-only, so agents can't see holds,
// and held inbound is surfaced here without leaking onto the agent inbox.)
export async function listPendingMessages(): Promise<PendingMessageSummary[]> {
  const page = await request<{ items?: ReviewWire[] | null }>("/v1/reviews");
  return (page.items ?? []).map<PendingMessageSummary>((r) => ({
    id: r.id,
    agent_email: r.agent,
    direction: r.direction,
    from: r.from,
    subject: r.subject,
    conversation_id: r.conversation_id,
    to: r.to ?? [],
    status: r.review_status,
    created_at: r.created_at,
  }));
}

export type ApprovePayload = {
  subject?: string;
  text?: string;
  html?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  attachments?: PendingAttachment[];
};

// approvePendingMessage / rejectPendingMessage hit the account-scoped review
// queue (POST /v1/reviews/{id}/approve|reject). A review's id IS the held
// message's id, so no inbox email is needed; agentEmail is kept in the
// signature only for call-site compatibility. (The deprecated agent-path
// approve/reject endpoints were removed in the pre-GA vocabulary freeze.)
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
  void agentEmail; // not needed by /reviews (id is globally unique); kept for call-site compat
  return request(
    "/v1/reviews/" + encodeURIComponent(id) + "/approve",
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
  void agentEmail;
  return request(
    "/v1/reviews/" + encodeURIComponent(id) + "/reject",
    {
      method: "POST",
      body: JSON.stringify({ reason }),
    },
  );
}
