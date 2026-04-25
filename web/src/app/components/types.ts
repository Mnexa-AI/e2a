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
  rejection_reason?: string;
  provider_message_id?: string;
  method?: string;
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
};

export type APIKeyData = {
  id: string;
  key?: string;        // one-time plaintext, only present on creation response
  key_prefix?: string; // non-secret prefix, shown in list view
  name: string;
  created_at: string;
};
