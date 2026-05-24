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
  webhook_url: string;
  agent_mode: string;
  domain_verified: boolean;
  public: boolean;
  created_at: string;
  hitl_enabled: boolean;
  hitl_ttl_seconds: number;
  hitl_expiration_action: "approve" | "reject";
  // Enriched stats — only populated by GET /api/dashboard/agents;
  // other agent endpoints leave them at zero values. Fields are
  // optional in the type so older deployments (no enrichment) still
  // parse correctly.
  inbound_7d?: number;
  outbound_7d?: number;
  pending_count?: number;
  last_delivery_at?: string | null;
  webhook_healthy?: boolean;
};

export type PendingMessageSummary = {
  id: string;
  agent_id: string;
  direction: "outbound";
  subject: string;
  type?: string;
  conversation_id?: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  status: string;
  approval_expires_at?: string;
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

export type ActivityEntry = {
  id: string;
  direction: "inbound" | "outbound";
  sender: string;
  recipient: string;
  subject: string;
  method?: string;
  type?: string;
  created_at: string;
  webhook_status?: string;
  webhook_error?: string;
  webhook_attempts?: number;
  // Outbound-only multi-recipient fields
  to_recipients?: string[];
  cc?: string[];
  bcc?: string[];
  // Set by ListActivityByAgent for the per-agent activity feed. Older
  // load paths leave these unset — UI renders "—" in that case.
  conversation_id?: string;
  size_bytes?: number;
};

// Response shape from `GET /api/v1/agents/{email}/messages`. The wire
// `status` field carries the inbox_status value for back-compat with
// the SDK polling contract; `hitl_status` is the outbound HITL
// lifecycle (sent | pending_approval | rejected | expired_*) and is
// empty on inbound rows. The dashboard inbox uses this projection
// directly; SDK consumers continue to read the inbound-only fields.
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
  // Inbox status for inbound rows: "unread" | "read". Empty for outbound.
  status: string;
  // Outbound HITL lifecycle. Empty for inbound.
  hitl_status?: string;
  // Outbound webhook delivery state.
  webhook_status?: string;
  webhook_error?: string;
  // Byte length of the raw RFC-5322 message. 0 if not stored (older
  // outbound rows pre-dating the size projection).
  size_bytes?: number;
  created_at: string;
};

export type ListMessagesResponse = {
  messages: MessageSummary[];
  next_token?: string;
};

// Response shape from `GET /api/v1/agents/{email}/messages/{id}` for an
// inbound row. Hand-rolled on the backend (see api.go's handleGetMessage)
// — kept here as the wire type for the focus page's inbound branch.
export type InboundMessageDetail = {
  message_id: string;
  from: string;
  to: string[];
  cc: string[];
  reply_to: string[];
  recipient: string;
  subject: string;
  conversation_id: string;
  status: string; // inbox_status
  created_at: string;
  auth_headers: Record<string, string>;
  // Raw RFC-5322 bytes, base64-encoded by the JSON layer. The focus page
  // renders a parsed text/plain part when present; otherwise falls back
  // to a "View raw" link. Backend body_text projection for inbound is a
  // tracked follow-up.
  raw_message: string;
};

export type APIKeyData = {
  id: string;
  key?: string;        // one-time plaintext, only present on creation response
  key_prefix?: string; // non-secret prefix, shown in list view
  name: string;
  created_at: string;
  // Updated on every successful authenticated request. Null until the
  // key is first used. Surfaces in the "Last used" column.
  last_used_at?: string | null;
  // Optional hard expiry — keys with null expires_at never expire.
  // AuthenticateRequest rejects expired keys at the auth gate.
  expires_at?: string | null;
};

// GET /api/dashboard/stats — workspace-level aggregates. The same
// endpoint powers two surfaces:
//   - Dashboard at-a-glance strip: uses `today` (default window=7)
//   - Settings usage card: passes ?window=30 and uses the
//     inbound_window / outbound_window / delivery_success_pct fields
// sample_window_days echoes the window in effect for the response.
export type DashboardStats = {
  today: {
    inbound: number;
    outbound: number;
    inbound_delta_pct: number;
    outbound_delta_pct: number;
  };
  pending: {
    count: number;
    oldest_seconds: number;
  };
  delivery_success_pct: number;
  sample_window_days: number;
  inbound_window: number;
  outbound_window: number;
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

// Request body for PATCH /api/v1/domains/{domain}. is_primary=true
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
