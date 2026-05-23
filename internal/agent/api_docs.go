package agent

import (
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// --- Request/Response types for swag API documentation ---

// SendEmailRequest is the request body for POST /api/v1/send.
type SendEmailRequest struct {
	From           string       `json:"from,omitempty" example:"my-bot@example.com"`
	To             []string     `json:"to" example:"alice@example.com"`
	CC             []string     `json:"cc,omitempty" example:"bob@example.com"`
	BCC            []string     `json:"bcc,omitempty" example:"carol@example.com"`
	Subject        string       `json:"subject" example:"Hello from my agent"`
	Body           string       `json:"body" example:"Hi Alice, this is my agent reaching out."`
	HTMLBody       string       `json:"html_body,omitempty" example:"<p>Hi Alice</p>"`
	ConversationID string       `json:"conversation_id,omitempty" example:"conv_abc123"`
	Attachments    []Attachment `json:"attachments,omitempty"`
} // @name SendEmailRequest

// SendEmailResponse is the response for send and reply operations.
// When the owning agent has HITL enabled, the server responds with
// status = "pending_approval" and 202 Accepted; approval_expires_at
// is set in that case. Otherwise status = "sent" with 200.
type SendEmailResponse struct {
	Status            string `json:"status" example:"sent" enums:"sent,pending_approval"`
	MessageID         string `json:"message_id" example:"msg_abc123"`
	Method            string `json:"method,omitempty" example:"smtp"`
	ApprovalExpiresAt string `json:"approval_expires_at,omitempty" example:"2025-01-15T10:30:00Z"`
} // @name SendEmailResponse

// ListAgentsResponse wraps the agent list returned by GET /api/v1/agents.
type ListAgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
} // @name ListAgentsResponse

// AgentInfo is the public API representation of an agent.
type AgentInfo struct {
	ID                   string    `json:"id" example:"ag_abc123"`
	Domain               string    `json:"domain" example:"agents.example.com"`
	Email                string    `json:"email" example:"my-bot@example.com"`
	Name                 string    `json:"name" example:"My Bot"`
	WebhookURL           string    `json:"webhook_url"`
	AgentMode            string    `json:"agent_mode" example:"cloud" enums:"cloud,local"`
	DomainVerified       bool      `json:"domain_verified"`
	CreatedAt            time.Time `json:"created_at"`
	HITLEnabled          bool      `json:"hitl_enabled"`
	HITLTTLSeconds       int       `json:"hitl_ttl_seconds" example:"604800"`
	HITLExpirationAction string    `json:"hitl_expiration_action" example:"reject" enums:"approve,reject"`
} // @name Agent

// ReplyToMessageRequest is the request body for replying to a message.
type ReplyToMessageRequest struct {
	Body           string       `json:"body" example:"Thanks for your email!"`
	HTMLBody       string       `json:"html_body,omitempty" example:"<p>Thanks for your email!</p>"`
	ReplyAll       bool         `json:"reply_all,omitempty" example:"false"`
	CC             []string     `json:"cc,omitempty" example:"bob@example.com"`
	BCC            []string     `json:"bcc,omitempty" example:"carol@example.com"`
	ConversationID string       `json:"conversation_id,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`
} // @name ReplyToMessageRequest

// ListMessagesResponse wraps the message list with pagination.
type ListMessagesResponse struct {
	Messages  []MessageSummary `json:"messages"`
	NextToken string           `json:"next_token,omitempty"`
} // @name ListMessagesResponse

// MessageSummary is a lightweight message summary for the list endpoint.
// To and CC are the parsed To: / Cc: headers from the original message;
// Recipient is this delivery's per-agent target. ReplyTo is the parsed
// Reply-To: header — empty when the sender did not request a different
// reply mailbox (the server never falls back to From: silently).
type MessageSummary struct {
	MessageID      string   `json:"message_id" example:"msg_abc123"`
	From           string   `json:"from" example:"alice@example.com"`
	To             []string `json:"to" example:"my-bot@example.com"`
	CC             []string `json:"cc,omitempty"`
	ReplyTo        []string `json:"reply_to,omitempty"`
	Recipient      string   `json:"recipient" example:"my-bot@example.com"`
	Subject        string   `json:"subject" example:"Hello"`
	ConversationID string   `json:"conversation_id,omitempty"`
	Status         string   `json:"status" example:"unread" enums:"unread,read"`
	CreatedAt      string   `json:"created_at" example:"2025-01-15T10:30:00Z"`
} // @name MessageSummary

// MessageDetail is the full message content returned by GET /api/v1/agents/{email}/messages/{id},
// which marks unread messages as read when fetched. To and CC are the parsed
// To: / Cc: headers from the original message; Recipient is this delivery's
// per-agent target. ReplyTo carries the parsed Reply-To: header so consumers
// can identify the intended reply mailbox for forwarded / notification mail
// (e.g. From: notifications@..., Reply-To: <real-user>).
type MessageDetail struct {
	MessageID      string            `json:"message_id" example:"msg_abc123"`
	From           string            `json:"from" example:"alice@example.com"`
	To             []string          `json:"to" example:"my-bot@example.com"`
	CC             []string          `json:"cc,omitempty"`
	ReplyTo        []string          `json:"reply_to,omitempty"`
	Recipient      string            `json:"recipient" example:"my-bot@example.com"`
	Subject        string            `json:"subject" example:"Hello"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Status         string            `json:"status" example:"read"`
	CreatedAt      string            `json:"created_at"`
	AuthHeaders    map[string]string `json:"auth_headers"`
	RawMessage     string            `json:"raw_message"`
} // @name MessageDetail

// --- Domain types ---

// RegisterDomainRequest is the request body for POST /api/v1/domains.
type RegisterDomainRequest struct {
	Domain string `json:"domain" example:"yourdomain.com"`
} // @name RegisterDomainRequest

// DomainInfo is the public API representation of a domain.
type DomainInfo struct {
	Domain            string     `json:"domain" example:"yourdomain.com"`
	Verified          bool       `json:"verified"`
	VerificationToken string     `json:"verification_token" example:"e2a-verify=abc123"`
	DNSRecords        DNSRecords `json:"dns_records"`
	CreatedAt         time.Time  `json:"created_at"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	// IsPrimary marks the user's default domain — at most one per
	// user, enforced server-side via SetDomainPrimary. The redesign's
	// Domains list renders this as a "Primary" chip.
	IsPrimary bool `json:"is_primary"`
	// LastCheckedAt is the timestamp of the most recent
	// /api/v1/domains/{domain}/verify probe (success or failure).
	// Distinct from VerifiedAt, which only updates on success.
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	// AgentCount is populated by list endpoints. Single-domain endpoints
	// (register, verify) leave it zero — the count would require an
	// extra query that callers can derive from the agents list anyway.
	AgentCount int `json:"agent_count"`
} // @name Domain

// DNSRecords contains the DNS records needed for domain verification.
type DNSRecords struct {
	MX  DNSRecord `json:"mx"`
	TXT DNSRecord `json:"txt"`
} // @name DNSRecords

// DNSRecord is a single DNS record entry.
type DNSRecord struct {
	Host     string `json:"host" example:"@"`
	Value    string `json:"value" example:"mx.example.com"`
	Priority *int   `json:"priority,omitempty" example:"10"`
} // @name DNSRecord

// ListDomainsResponse wraps the domain list returned by GET /api/v1/domains.
type ListDomainsResponse struct {
	Domains []DomainInfo `json:"domains"`
} // @name ListDomainsResponse

// UpdateDomainRequest is the body for PATCH /api/v1/domains/{domain}.
// Only `is_primary=true` is meaningful — see handleUpdateDomain.
type UpdateDomainRequest struct {
	IsPrimary *bool `json:"is_primary,omitempty"`
} // @name UpdateDomainRequest

// RegisterDomainResponse is the response for POST /api/v1/domains.
type RegisterDomainResponse = DomainInfo // @name RegisterDomainResponse

// VerifyDomainResponse is the response for POST /api/v1/domains/{domain}/verify.
// Per-record diagnostic fields (MX, SPF, DKIM) report what the probe
// found in DNS independent of the verified bool — verified=true iff the
// TXT ownership token is present, while MX/SPF/DKIM are advisory.
type VerifyDomainResponse struct {
	Domain     string     `json:"domain" example:"yourdomain.com"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// MX status: "found" iff at least one MX record points at the
	// deployment's smtp domain. "missing" otherwise.
	MX string `json:"mx,omitempty" example:"found" enums:"found,missing"`
	// SPF status: "found" iff a v=spf1 TXT record includes the
	// deployment's send domain. "missing" otherwise.
	SPF string `json:"spf,omitempty" example:"found" enums:"found,missing"`
	// DKIM status: "deferred" until BACKEND_TODO #5 ships per-domain
	// DKIM key generation. Until then there's no per-domain DKIM TXT
	// record to verify against.
	DKIM string `json:"dkim,omitempty" example:"deferred" enums:"found,missing,deferred"`
} // @name VerifyDomainResponse

// --- Deployment info ---

// DeploymentInfo is the response shape of GET /api/v1/info. It's how
// CLI/SDK clients discover deployment-specific values (shared domain,
// public URL) without each user having to configure them by hand.
type DeploymentInfo struct {
	// SharedDomain is the mail domain backing slug-based agent registration
	// on this deployment (e.g. "agents.example.com"). Empty when the
	// operator hasn't configured one — in that case slug registration is
	// disabled and every agent must use a custom domain.
	SharedDomain string `json:"shared_domain" example:"agents.example.com"`

	// SlugRegistrationEnabled mirrors `shared_domain != ""` for clients
	// that prefer a boolean. Equivalent to checking SharedDomain directly.
	SlugRegistrationEnabled bool `json:"slug_registration_enabled" example:"true"`

	// PublicURL is the externally visible base URL of the API itself —
	// the same value the operator sets in `http.public_url`. Empty when
	// not configured.
	PublicURL string `json:"public_url,omitempty" example:"https://e2a.example.com"`
} // @name DeploymentInfo

// --- User data rights ---
//
// Aliases re-export the top-level identity-package types so swag's parser
// can resolve them in the @Success annotations on user_data_rights_api.go
// — swag binds @Success refs to types in the same package as the handler.
// Nested types (UserExportUser, APIKeyExportEntry, UsageEventEntry) are
// reached transitively and don't need aliases; their @name directives in
// the identity package keep them in the OpenAPI spec under clean names.

type UserExport = identity.UserExport                 // @name UserExport
type DeleteUserDataResult = identity.DeleteUserDataResult // @name DeleteUserDataResult

// --- Webhook types ---

// WebhookPayload is the payload delivered to your webhook URL when your agent receives an email.
// This schema is for documentation only — the actual delivery is handled by the webhook package.
// To and CC are the parsed To: / Cc: headers; Recipient is the per-agent target
// for this delivery.
type WebhookPayload struct {
	MessageID      string            `json:"message_id" example:"msg_abc123"`
	ConversationID string            `json:"conversation_id,omitempty"`
	From           string            `json:"from" example:"alice@example.com"`
	To             []string          `json:"to" example:"agent@yourdomain.com"`
	CC             []string          `json:"cc,omitempty"`
	Recipient      string            `json:"recipient" example:"agent@yourdomain.com"`
	RawMessage     []byte            `json:"raw_message"`
	AuthHeaders    map[string]string `json:"auth_headers"`
	ReceivedAt     time.Time         `json:"received_at"`
} // @name WebhookPayload

// AuthHeaders documents the signed authentication headers included in webhook deliveries.
// The server signs Verified, Sender, EntityType, DomainCheck, Delegation, and Timestamp
// into a canonical string and produces an HMAC-SHA256 Signature. SDKs can verify the
// signature to confirm the payload was not tampered with.
type AuthHeaders struct {
	Verified    string `json:"X-E2A-Auth-Verified" example:"true"`
	Sender      string `json:"X-E2A-Auth-Sender" example:"alice@example.com"`
	EntityType  string `json:"X-E2A-Auth-Entity-Type" example:"human" enums:"human,agent"`
	DomainCheck string `json:"X-E2A-Auth-Domain-Check" example:"spf=pass; dkim=none"`
	Delegation  string `json:"X-E2A-Auth-Delegation,omitempty" example:"agent=ag_abc123;human=usr_xyz789"`
	Signature   string `json:"X-E2A-Auth-Signature" example:"sha256=..."`
	Timestamp   string `json:"X-E2A-Auth-Timestamp" example:"2025-01-15T10:30:00Z"`
} // @name AuthHeaders

// --- WebSocket types ---

// WebSocketNotification is the lightweight notification sent over WebSocket when a new
// message arrives for an agent. It contains only metadata — the full message
// content (including the To/Cc lists) is fetched via GET /api/v1/agents/{email}/messages/{id}.
type WebSocketNotification struct {
	MessageID      string    `json:"message_id" example:"msg_abc123"`
	ConversationID string    `json:"conversation_id,omitempty"`
	From           string    `json:"from" example:"alice@example.com"`
	Recipient      string    `json:"recipient" example:"my-bot@example.com"`
	Subject        string    `json:"subject" example:"Hello"`
	ReceivedAt     time.Time `json:"received_at"`
} // @name WebSocketNotification

// Attachment is a base64-encoded file attachment.
type Attachment = outbound.Attachment

// --- HITL approval types ---

// UpdateAgentRequest is the request body for PUT /api/v1/agents/{email}.
// All fields are optional; only the fields you send are updated, so
// callers can PATCH a single setting without re-sending the rest.
type UpdateAgentRequest struct {
	WebhookURL           *string `json:"webhook_url,omitempty"`
	AgentMode            *string `json:"agent_mode,omitempty" enums:"cloud,local"`
	HITLEnabled          *bool   `json:"hitl_enabled,omitempty"`
	HITLTTLSeconds       *int    `json:"hitl_ttl_seconds,omitempty" example:"604800"`
	HITLExpirationAction *string `json:"hitl_expiration_action,omitempty" enums:"approve,reject"`
} // @name UpdateAgentRequest

// PendingMessageSummary is a row in GET /api/v1/messages?status=pending_approval.
// Body and attachments are intentionally omitted — fetch the full detail
// via GET /api/v1/messages/{id} when the reviewer drills in.
type PendingMessageSummary struct {
	ID                string    `json:"id" example:"msg_abc123"`
	AgentID           string    `json:"agent_id" example:"my-bot@example.com"`
	Direction         string    `json:"direction" example:"outbound"`
	Subject           string    `json:"subject" example:"Re: contract details"`
	Type              string    `json:"type,omitempty" example:"send" enums:"send,reply,test"`
	ConversationID    string    `json:"conversation_id,omitempty"`
	To                []string  `json:"to" example:"alice@example.com"`
	CC                []string  `json:"cc,omitempty"`
	BCC               []string  `json:"bcc,omitempty"`
	Status            string    `json:"status" example:"pending_approval" enums:"sent,pending_approval,rejected,expired_approved,expired_rejected"`
	ApprovalExpiresAt string    `json:"approval_expires_at,omitempty" example:"2025-01-15T10:30:00Z"`
	CreatedAt         time.Time `json:"created_at"`
} // @name PendingMessageSummary

// PendingMessageDetail extends the summary with the stored body,
// attachments, and review metadata. Body columns are populated only
// while the row is in pending_approval; terminal rows return empty
// bodies since the server scrubs them on transition.
type PendingMessageDetail struct {
	PendingMessageSummary
	EmailMessageID string       `json:"email_message_id,omitempty" example:"<orig@gmail.com>"`
	BodyText       string       `json:"body_text,omitempty"`
	BodyHTML       string       `json:"body_html,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`
	Edited         bool         `json:"edited,omitempty"`
	ReviewedAt     string       `json:"reviewed_at,omitempty" example:"2025-01-15T10:35:00Z"`
	// ReviewedByUserID identifies the human reviewer for approved or
	// rejected messages. NULL on TTL-expired transitions (worker
	// auto-approve / auto-reject) where no human reviewed the message.
	ReviewedByUserID  *string      `json:"reviewed_by_user_id,omitempty" example:"usr_abc123"`
	// ReviewedByName is the JOIN'd display name from the reviewer's
	// users row. NULL when reviewed_by_user_id is null (worker) or when
	// the reviewer's user account has since been deleted (the FK has
	// ON DELETE SET NULL specifically so this doesn't poison the audit
	// trail).
	ReviewedByName    *string      `json:"reviewed_by_name,omitempty" example:"Jamie"`
	RejectionReason   string       `json:"rejection_reason,omitempty"`
	ProviderMessageID string       `json:"provider_message_id,omitempty"`
	Method            string       `json:"method,omitempty" example:"smtp"`
} // @name PendingMessageDetail

// ListPendingMessagesResponse wraps the array returned by GET /api/v1/messages.
type ListPendingMessagesResponse struct {
	Messages []PendingMessageSummary `json:"messages"`
} // @name ListPendingMessagesResponse

// ApprovePendingMessageRequest is the optional body for
// POST /api/v1/messages/{id}/approve. Any field present overrides the
// stored value before the message is sent; missing fields are left as
// the original draft. An empty body means approve-as-is.
type ApprovePendingMessageRequest struct {
	Subject     *string      `json:"subject,omitempty"`
	BodyText    *string      `json:"body_text,omitempty"`
	BodyHTML    *string      `json:"body_html,omitempty"`
	To          []string     `json:"to,omitempty"`
	CC          []string     `json:"cc,omitempty"`
	BCC         []string     `json:"bcc,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
} // @name ApprovePendingMessageRequest

// ApprovePendingMessageResponse is the response when the reviewer
// approves a held message and the server successfully hands it to the
// upstream SMTP relay.
type ApprovePendingMessageResponse struct {
	Status            string `json:"status" example:"sent"`
	MessageID         string `json:"message_id"`
	ProviderMessageID string `json:"provider_message_id,omitempty"`
	Method            string `json:"method,omitempty" example:"smtp"`
	Edited            bool   `json:"edited,omitempty"`
} // @name ApprovePendingMessageResponse

// RejectPendingMessageRequest is the optional body for
// POST /api/v1/messages/{id}/reject.
type RejectPendingMessageRequest struct {
	Reason string `json:"reason,omitempty" example:"wrong recipient"`
} // @name RejectPendingMessageRequest

// RejectPendingMessageResponse is the response when the reviewer
// rejects a held message.
type RejectPendingMessageResponse struct {
	Status          string `json:"status" example:"rejected"`
	MessageID       string `json:"message_id"`
	RejectionReason string `json:"rejection_reason,omitempty"`
} // @name RejectPendingMessageResponse
