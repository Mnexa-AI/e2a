// Onboarding domain model types.
// These map to the backend API shapes but are owned by the UI layer.

/** Web-UI address path — shared e2a slug vs custom domain (under SetupMethod=web). */
export type AddressType = "shared" | "custom";

/** Top-level setup method — hand off to an agent over MCP, or use the web UI. */
export type SetupMethod = "agent" | "web";

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

/** Async SES sending-identity state (decision 4 / Slice 4), independent of
 *  inbound `verified`. Drives the "send as your own address" onboarding:
 *  none → not provisioned (feature off, or pre-verify); pending → registered,
 *  awaiting SES; verified → own-address From is live (no "via e2a"); failed →
 *  verification failed or timed out (see `sending_error`). Open set — tolerate
 *  unknown values. */
export type DomainSendingStatus = "none" | "pending" | "verified" | "failed";

/** A DNS record the customer publishes to enable own-domain sending. Mirrors
 *  the backend `sending_dns_records[]` (senderidentity.DNSRecord): the custom
 *  MAIL FROM subdomain's MX + SPF. The DKIM record is the existing per-domain
 *  one, reused via BYODKIM, so it is NOT repeated here. */
export type SendingDNSRecord = {
  type: string; // "MX" | "TXT"
  name: string;
  value: string;
};

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
  // Enrichment fields. last_checked_at moves on every verification probe
  // (success or failure); agent_count is computed at read time.
  last_checked_at?: string | null;
  agent_count?: number;
  // Sender identity (decision 4 / Slice 4), independent of `verified`.
  // Absent/none + empty sending_dns_records when the feature is off
  // (ses_region unset) — the UI renders no sending section in that case.
  sending_status?: DomainSendingStatus;
  sending_error?: string;
  sending_dns_records?: SendingDNSRecord[];
  sending_last_checked_at?: string | null;
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
export type SharedFlowStep = "address" | "details" | "connect";

/** Custom-domain flow steps (checklist, resumable). */
export type CustomFlowStep =
  | "choose_domain"
  | "dns"
  | "verify"
  | "create_agent"
  | "connect";

/** Top-level onboarding step before branching. */
export type OnboardingPath = "choose" | "shared" | "custom";

/** Parameters for creating an agent via the shared-domain flow. */
export type SharedAgentParams = {
  slug: string;
};

/** Parameters for creating an agent via the custom-domain flow. */
export type CustomAgentParams = {
  domain: string;
  localPart: string;
};

/** Normalized create-agent request for the API layer. */
export type CreateAgentRequest =
  | { type: "shared"; slug: string }
  | { type: "custom"; email: string };

/** Response from POST /v1/agents. */
export type AgentCreateResponse = {
  id: string;
  domain: string;
  email: string;
};

// ── Protection config (GET/PUT /v1/agents/{email}/protection) ──
// Mirrors ProtectionConfigView. Beta. The dashboard only edits the
// `holds` section; inbound/outbound are read + passed back unchanged on
// the wholesale PUT.

export type ProtectionGate = {
  policy?: "open" | "allowlist" | "domain";
  allowlist?: string[];
  action?: "flag" | "review" | "block";
};

export type ProtectionScan = {
  sensitivity?: "off" | "low" | "medium" | "high";
};

export type ProtectionDirection = {
  gate: ProtectionGate;
  scan: ProtectionScan;
};

export type ProtectionHolds = {
  ttl_seconds?: number;
  on_expiry?: "approve" | "reject";
};

export type ProtectionConfig = {
  inbound: ProtectionDirection;
  outbound: ProtectionDirection;
  holds: ProtectionHolds;
};
