export type AgentData = {
  id: string;
  domain: string;
  email: string;
};

export type UserInfo = {
  id: string;
  email: string;
  name: string;
  created_at: string;
};

export type DashboardAgent = {
  id: string;
  domain: string;
  email: string;
  name: string;
  domain_verified: boolean;
  created_at: string;
};

// Aggregated client-side from `GET /v1/agents/{address}/messages?
// direction=outbound` rows whose status === "pending_approval".
// `/v1` has no cross-account pending endpoint, so the pending page
// fans out over the account's agents and tags each row with the
// owning agent's address (`agent_email`) — needed to drive the
// agent-scoped approve/reject/detail endpoints.
export type PendingMessageSummary = {
  id: string;
  // Owning agent's email address. In `/v1` this is how detail/approve/
  // reject are addressed (the path's {address}). Displayed in the queue
  // row's "from" line.
  agent_email: string;
  direction: "outbound";
  subject: string;
  type?: string;
  conversation_id?: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  status: string;
  created_at: string;
};

export type PendingAttachment = {
  filename: string;
  content_type: string;
  data: string; // base64
};

export type PendingMessageDetail = PendingMessageSummary & {
  email_message_id?: string;
  body_text?: string;
  body_html?: string;
  attachments?: PendingAttachment[];
  edited?: boolean;
  reviewed_at?: string;
  // Set on approved/rejected rows. Null on worker-triggered transitions
  // (TTL auto-approve / auto-reject) — UI renders "expired" instead of
  // a reviewer name in that case. The two fields move together.
  reviewed_by_user_id?: string | null;
  reviewed_by_name?: string | null;
  rejection_reason?: string;
  provider_message_id?: string;
  method?: string;
  // Attached when this is a reply — the inbound message being replied
  // to. Drives the review panel's "In reply to" provenance pane
  // (SPF/DKIM/DMARC from auth_headers). Null on send/test messages.
  inbound?: PendingMessageInboundContext | null;
};

export type PendingMessageInboundContext = {
  sender: string;
  subject: string;
  created_at: string;
  auth_headers?: Record<string, string>;
};

// MessageSummaryView from `GET /v1/agents/{address}/messages`
// (PageMessageSummaryView.items). `status` carries the lifecycle value:
// for inbound rows "unread" | "read"; for outbound rows the HITL
// lifecycle (sent | pending_approval | rejected | expired_*). The
// dashboard inbox uses this projection directly.
export type MessageSummary = {
  message_id: string;
  direction: "inbound" | "outbound";
  from: string;
  to: string[];
  cc?: string[];
  reply_to?: string[];
  recipient: string;
  subject: string;
  conversation_id?: string;
  // Lifecycle status. Inbound: "unread" | "read". Outbound HITL:
  // sent | pending_approval | rejected | expired_*.
  status: string;
  // Outbound HITL lifecycle (mirrors `status` on outbound rows).
  hitl_status?: string;
  // Outbound webhook delivery state.
  webhook_status?: string;
  webhook_error?: string;
  // Byte length of the raw RFC-5322 message. 0 if not stored (older
  // outbound rows pre-dating the size projection).
  size_bytes?: number;
  created_at: string;
};

// PageMessageSummaryView — the cursor-paginated envelope returned by
// `GET /v1/agents/{address}/messages`.
export type ListMessagesResponse = {
  items: MessageSummary[];
  next_cursor?: string | null;
};

// MessageView from `GET /v1/agents/{address}/messages/{id}`. Used by the
// focus page's inbound branch. The `/v1` detail endpoint returns the
// same MessageView shape for inbound and outbound; inbound rows carry
// `auth_headers` + `raw_message`, and the parsed text/plain body comes
// through `body.text`.
export type InboundMessageDetail = {
  message_id: string;
  from: string;
  to: string[];
  cc: string[];
  reply_to: string[];
  recipient: string;
  subject: string;
  conversation_id: string;
  status: string;
  created_at: string;
  auth_headers: Record<string, string>;
  // Parsed body, when the backend could extract a text/plain part.
  body?: { text?: string; html?: string };
  // Raw RFC-5322 bytes, base64-encoded by the JSON layer. The focus page
  // decodes this as a fallback when `body.text` is absent.
  raw_message: string;
};

export type APIKeyData = {
  id: string;
  key?: string;        // one-time plaintext, only present on creation response
  key_prefix?: string; // non-secret prefix, shown in list view
  name: string;
  // Credential scope: "account" (workspace admin) or "agent" (bound to a
  // single inbox). `agent` is the bound inbox email, present only for agent
  // scope.
  scope?: string;
  agent?: string;
  created_at: string;
  // Updated on every successful authenticated request. Null until the
  // key is first used. Surfaces in the "Last used" column.
  last_used_at?: string | null;
  // Optional hard expiry — keys with null expires_at never expire.
  // AuthenticateRequest rejects expired keys at the auth gate.
  expires_at?: string | null;
};

// Domain enrichment fields — chips on the Domains page. is_primary is
// at most true on one row per user; last_checked_at moves on every
// verification probe (success or failure).
export type DomainInfo = {
  domain: string;
  verified: boolean;
  verification_token: string;
  dns_records: {
    mx: { host: string; value: string; priority?: number };
    txt: { host: string; value: string };
    // DKIM is populated for domains with a stored keypair (migration 014).
    // Legacy rows leave it absent; UI detects via `dkim?.host` being empty.
    dkim?: { host: string; value: string };
  };
  created_at: string;
  verified_at?: string | null;
  is_primary: boolean;
  last_checked_at?: string | null;
  agent_count: number;
};

// Request body for PATCH /v1/domains/{domain}. is_primary=true
// promotes the domain (atomically demoting any prior primary).
// is_primary=false is rejected — switch primary by promoting a
// different domain.
export type UpdateDomainRequest = {
  is_primary?: boolean;
};

// Request body for POST /api/keys. expires_at is an RFC 3339
// timestamp; omit or null to issue a never-expiring key.
export type CreateAPIKeyRequest = {
  name: string;
  expires_at?: string;
};

// Request body for PATCH /api/auth/me. Only `name` is updatable today;
// other identity fields come from the OAuth provider.
export type UpdateMeRequest = {
  name: string;
};
