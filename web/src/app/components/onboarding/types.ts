// Onboarding domain model types.
// These map to the backend API shapes but are owned by the UI layer.

/** Agent delivery mode — how the agent receives email. */
export type AgentMode = "local" | "cloud";

/** Address type — shared e2a slug vs custom domain. */
export type AddressType = "shared" | "custom";

/** Domain verification status for the checklist model. */
export type DomainStatus = "unverified" | "verified";

/** Custom-domain checklist steps — derivable from backend state only.
 *  domain_added:    domain registered but not yet verified
 *  domain_verified: domain verified, no agents on it yet
 *  agent_created:   at least one agent exists on this domain
 */
export type ChecklistStep =
  | "domain_added"
  | "domain_verified"
  | "agent_created";

/** Domain as returned by GET /v1/domains. */
export type DomainInfo = {
  domain: string;
  verified: boolean;
  verification_token: string;
  dns_records: {
    mx: { host: string; value: string; priority: number };
    txt: { host: string; value: string };
    // DKIM is populated for domains with a stored keypair (migration
    // 014). Pre-migration rows omit it.
    dkim?: { host: string; value: string };
  };
  created_at: string;
  verified_at: string | null;
  // Enrichment fields. is_primary is true on at most one domain per
  // user; last_checked_at moves on every verification probe (success
  // or failure); agent_count is computed at read time.
  is_primary?: boolean;
  last_checked_at?: string | null;
  agent_count?: number;
};

/** Response from POST /v1/domains/{domain}/verify — per-record
 * diagnostic. `dkim` reports "found" or "missing" against the
 * per-domain public key registered at claim time. "deferred" is
 * returned only for pre-migration domains that have no stored
 * keypair. */
export type VerifyDomainResponse = {
  domain: string;
  verified: boolean;
  verified_at?: string | null;
  mx?: "found" | "missing";
  spf?: "found" | "missing";
  dkim?: "found" | "missing" | "deferred";
};

/** The progress state for a domain through onboarding. */
export type DomainProgress = {
  domain: DomainInfo;
  step: ChecklistStep;
  agentCount: number;
};

/** Shared-domain flow steps (linear, fast). */
export type SharedFlowStep = "address" | "mode" | "details" | "connect";

/** Custom-domain flow steps (checklist, resumable). */
export type CustomFlowStep =
  | "choose_domain"
  | "dns"
  | "verify"
  | "mode"
  | "create_agent"
  | "connect";

/** Top-level onboarding step before branching. */
export type OnboardingPath = "choose" | "shared" | "custom";

/** Parameters for creating an agent via the shared-domain flow. */
export type SharedAgentParams = {
  slug: string;
  agentMode: AgentMode;
  webhookUrl?: string;
};

/** Parameters for creating an agent via the custom-domain flow. */
export type CustomAgentParams = {
  domain: string;
  localPart: string;
  agentMode: AgentMode;
  webhookUrl?: string;
};

/** Normalized create-agent request for the API layer. */
export type CreateAgentRequest =
  | { type: "shared"; slug: string; agent_mode: AgentMode; webhook_url?: string }
  | { type: "custom"; email: string; agent_mode: AgentMode; webhook_url?: string };

/** Response from POST /v1/agents. */
export type AgentCreateResponse = {
  id: string;
  domain: string;
  email: string;
};

/** Update agent mode request for PUT /api/dashboard/agents/{email}. */
export type UpdateAgentRequest = {
  agent_mode?: AgentMode;
  webhook_url?: string;
  hitl_enabled?: boolean;
  hitl_ttl_seconds?: number;
  hitl_expiration_action?: "approve" | "reject";
};
