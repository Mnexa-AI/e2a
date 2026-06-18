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

// ForwardMessageRequest is the request body for forwarding a message.
// Body and html_body are the caller's optional comment to prepend; the
// server appends a quoted block with the original headers and body. A
// forward is treated as a new thread (no In-Reply-To/References) — pass
// conversation_id to bind it to an existing thread explicitly.
type ForwardMessageRequest struct {
	To             []string     `json:"to" example:"alice@example.com"`
	CC             []string     `json:"cc,omitempty" example:"bob@example.com"`
	BCC            []string     `json:"bcc,omitempty" example:"carol@example.com"`
	Body           string       `json:"body,omitempty" example:"FYI — see below"`
	HTMLBody       string       `json:"html_body,omitempty" example:"<p>FYI — see below</p>"`
	ConversationID string       `json:"conversation_id,omitempty"`
	Attachments    []Attachment `json:"attachments,omitempty"`
} // @name ForwardMessageRequest

// ListMessagesResponse wraps the message list with pagination.
type ListMessagesResponse struct {
	Messages  []MessageSummary `json:"messages"`
	NextToken string           `json:"next_token,omitempty"`
} // @name ListMessagesResponse

// ConversationSummary is one row in the conversations list. Aggregated
// counts + the "latest" preview fields render an inbox-style
// conversation list without a per-row drill-down.
type ConversationSummary struct {
	ConversationID string `json:"conversation_id" example:"conv_abc123"`
	LastMessageAt  string `json:"last_message_at" example:"2026-05-28T12:00:00Z"`
	FirstMessageAt string `json:"first_message_at" example:"2026-05-20T10:00:00Z"`
	MessageCount   int    `json:"message_count" example:"4"`
	InboundCount   int    `json:"inbound_count" example:"2"`
	OutboundCount  int    `json:"outbound_count" example:"2"`
	// HasUnread is true iff at least one INBOUND member of this
	// conversation is in inbox_status='unread'. Outbound rows do
	// NOT contribute — a thread containing only your sent messages
	// (or only HITL-pending outbound) returns false. This is the
	// agent's mailbox view, not the reviewer's HITL queue.
	HasUnread     bool   `json:"has_unread" example:"true"`
	LatestSubject string `json:"latest_subject" example:"Re: Quarterly report"`
	LatestSender  string `json:"latest_sender" example:"alice@example.com"`
} // @name ConversationSummary

// ConversationDetail extends the summary with computed aggregates
// (participants union, label union) and the member messages, ordered
// chronologically (oldest first).
type ConversationDetail struct {
	ConversationSummary
	Participants []string         `json:"participants"`
	Labels       []string         `json:"labels"`
	Messages     []MessageSummary `json:"messages"`
} // @name ConversationDetail

// --- Webhooks-as-a-resource (slice 2) ---

// WebhookFilters is the structured scope filter on a webhook. Empty /
// missing keys mean "no constraint of that type". A webhook with
// all-empty filters is a cross-cutting subscriber that matches every
// event of the right type for the owning user.
type WebhookFilters struct {
	AgentIDs        []string `json:"agent_ids,omitempty"`
	ConversationIDs []string `json:"conversation_ids,omitempty"`
	Labels          []string `json:"labels,omitempty"`
} // @name WebhookFilters

// CreateWebhookRequest is the POST /api/v1/webhooks body.
type CreateWebhookRequest struct {
	URL         string          `json:"url" example:"https://example.com/e2a/hook"`
	Events      []string        `json:"events" example:"email.received"`
	Filters     *WebhookFilters `json:"filters,omitempty"`
	Description string          `json:"description,omitempty" example:"main inbox handler"`
} // @name CreateWebhookRequest

// UpdateWebhookRequest is the PATCH body. All fields optional; only
// fields present in the request are touched. url / events / filters
// are full-replace when present (the sent value is canonical).
type UpdateWebhookRequest struct {
	URL         *string         `json:"url,omitempty"`
	Events      *[]string       `json:"events,omitempty"`
	Filters     *WebhookFilters `json:"filters,omitempty"`
	Description *string         `json:"description,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
} // @name UpdateWebhookRequest

// WebhookResponse is the GET / POST response shape. SigningSecret is
// populated only on POST /webhooks and POST /webhooks/{id}/rotate-secret
// — every other endpoint omits it so a stolen API key cannot exfiltrate
// secrets via list/get.
type WebhookResponse struct {
	ID                      string         `json:"id" example:"wh_abc123"`
	URL                     string         `json:"url"`
	Description             string         `json:"description"`
	Events                  []string       `json:"events"`
	Filters                 WebhookFilters `json:"filters"`
	SigningSecret           string         `json:"signing_secret,omitempty"`             // ONLY on create + rotate
	PreviousSecretExpiresAt string         `json:"previous_secret_expires_at,omitempty"` // ONLY on rotate
	Enabled                 bool           `json:"enabled"`
	AutoDisabledAt          string         `json:"auto_disabled_at,omitempty"`
	CreatedAt               string         `json:"created_at"`
	LastDeliveredAt         string         `json:"last_delivered_at,omitempty"`
} // @name WebhookResponse

// ListWebhooksResponse wraps the list endpoint.
type ListWebhooksResponse struct {
	Webhooks []WebhookResponse `json:"webhooks"`
} // @name ListWebhooksResponse

// RotateWebhookSecretResponse carries the new plaintext secret +
// the 24h expiry for the previous secret's grace window.
type RotateWebhookSecretResponse struct {
	SigningSecret           string `json:"signing_secret"`
	PreviousSecretExpiresAt string `json:"previous_secret_expires_at"`
} // @name RotateWebhookSecretResponse

// TestWebhookRequest fires a synthetic event for development.
type TestWebhookRequest struct {
	Event string                 `json:"event" example:"email.received"`
	Data  map[string]interface{} `json:"data,omitempty"`
} // @name TestWebhookRequest

// TestWebhookResponse echoes the delivery id of the synthetic event
// so the caller can correlate it in GET /webhooks/{id}/deliveries.
type TestWebhookResponse struct {
	DeliveryID string `json:"delivery_id"`
} // @name TestWebhookResponse

// WebhookDeliveryResponse is one row in GET /webhooks/{id}/deliveries.
type WebhookDeliveryResponse struct {
	ID             string `json:"id"`
	EventType      string `json:"event_type"`
	Status         string `json:"status"`
	Attempts       int    `json:"attempts"`
	LastError      string `json:"last_error,omitempty"`
	LastStatusCode *int   `json:"last_status_code,omitempty"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	NextRetryAt    string `json:"next_retry_at"`
	CreatedAt      string `json:"created_at"`
} // @name WebhookDeliveryResponse

// ListWebhookDeliveriesResponse wraps the deliveries endpoint.
type ListWebhookDeliveriesResponse struct {
	Deliveries []WebhookDeliveryResponse `json:"deliveries"`
} // @name ListWebhookDeliveriesResponse

// ListConversationsResponse wraps the conversations list. Pagination
// is intentionally deferred — the response is hard-capped at 100
// conversations server-side, which covers the inbox-style use case
// without a cursor.
type ListConversationsResponse struct {
	Conversations []ConversationSummary `json:"conversations"`
} // @name ListConversationsResponse

// MessageSummary is a lightweight message summary for the list endpoint.
// To and CC are the parsed To: / Cc: headers from the original message;
// Recipient is this delivery's per-agent target. ReplyTo is the parsed
// Reply-To: header — empty when the sender did not request a different
// reply mailbox (the server never falls back to From: silently).
//
// This shape covers both inbound and outbound rows since the dashboard
// inbox queries `?direction=all`. The non-`omitempty` fields are
// present on every row; the others are direction-specific:
//
//   - Status (inbound):       "unread" | "read"; empty string for outbound rows.
//   - HITLStatus (outbound):  the outbound delivery state ("pending_approval",
//     "sent", "rejected", "expired_*"); empty for inbound.
//   - WebhookStatus (outbound): delivery state of the most recent webhook
//     attempt ("delivered", "pending", "failed");
//     empty for inbound.
//   - WebhookError (outbound): last webhook delivery error text when
//     WebhookStatus is "failed"; empty otherwise.
//   - SizeBytes:               raw message length in bytes (best-effort,
//     0 when the row was migrated from a pre-sizing
//     build).
type MessageSummary struct {
	MessageID      string   `json:"message_id" example:"msg_abc123"`
	Direction      string   `json:"direction" example:"inbound" enums:"inbound,outbound"`
	From           string   `json:"from" example:"alice@example.com"`
	To             []string `json:"to" example:"my-bot@example.com"`
	CC             []string `json:"cc,omitempty"`
	ReplyTo        []string `json:"reply_to,omitempty"`
	Recipient      string   `json:"recipient" example:"my-bot@example.com"`
	Subject        string   `json:"subject" example:"Hello"`
	ConversationID string   `json:"conversation_id,omitempty"`
	// Status carries the inbound inbox_status value (`unread` | `read`).
	// Empty string for outbound rows — clients filtering on Status must
	// gate on `Direction == "inbound"` first. The enum was removed from
	// the swag annotation deliberately so SDK generators don't emit a
	// `Literal["unread", "read"]` that breaks at runtime.
	Status        string `json:"status"`
	HITLStatus    string `json:"hitl_status,omitempty" example:"sent" enums:"pending_approval,sent,rejected,expired_approved,expired_rejected"`
	WebhookStatus string `json:"webhook_status,omitempty" example:"delivered" enums:"pending,delivered,failed"`
	WebhookError  string `json:"webhook_error,omitempty"`
	SizeBytes     int    `json:"size_bytes,omitempty" example:"4231"`
	// Labels are caller-applied string tags. Always lowercase, charset
	// `[a-z0-9:_-]+`, ≤ 64 chars each, ≤ 100 per message. The `e2a:`
	// prefix is reserved for server-applied system labels. Empty array
	// when no labels are set — never null.
	Labels    []string `json:"labels"`
	CreatedAt string   `json:"created_at" example:"2025-01-15T10:30:00Z"`
} // @name MessageSummary

// MessageDetail is the full message content returned by GET /api/v1/agents/{email}/messages/{id},
// which marks unread messages as read when fetched. To and CC are the parsed
// To: / Cc: headers from the original message; Recipient is this delivery's
// per-agent target. ReplyTo carries the parsed Reply-To: header so consumers
// can identify the intended reply mailbox for forwarded / notification mail
// (e.g. From: notifications@..., Reply-To: <real-user>).
type MessageDetail struct {
	MessageID      string   `json:"message_id" example:"msg_abc123"`
	From           string   `json:"from" example:"alice@example.com"`
	To             []string `json:"to" example:"my-bot@example.com"`
	CC             []string `json:"cc,omitempty"`
	ReplyTo        []string `json:"reply_to,omitempty"`
	Recipient      string   `json:"recipient" example:"my-bot@example.com"`
	Subject        string   `json:"subject" example:"Hello"`
	ConversationID string   `json:"conversation_id,omitempty"`
	Status         string   `json:"status" example:"read"`
	// Labels are caller-applied string tags. See MessageSummary.Labels
	// for the validation rules. Empty array when no labels are set —
	// never null.
	Labels      []string          `json:"labels"`
	CreatedAt   string            `json:"created_at"`
	AuthHeaders map[string]string `json:"auth_headers"`
	RawMessage  string            `json:"raw_message"`
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
// DKIM is populated for domains created after migration 014 (per-domain
// keypairs). Pre-migration rows leave DKIM at the zero value — clients
// can detect by checking Host == "".
type DNSRecords struct {
	MX   DNSRecord `json:"mx"`
	TXT  DNSRecord `json:"txt"`
	DKIM DNSRecord `json:"dkim,omitempty"`
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
	// DKIM status: "found" iff the published TXT record at
	// "{selector}._domainkey.{domain}" matches the per-domain public
	// key stored at registration time. "missing" iff a keypair is
	// stored but the TXT record isn't published yet. "deferred" iff
	// no keypair is stored — pre-migration rows that haven't been
	// re-claimed since #5 shipped. A fresh-claimed domain always has
	// a keypair, so "deferred" only appears on legacy data.
	DKIM string `json:"dkim,omitempty" example:"found" enums:"found,missing,deferred"`
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

type UserExport = identity.UserExport                     // @name UserExport
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
	Type              string    `json:"type,omitempty" example:"send" enums:"send,reply,test,forward"`
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
	// InboundContext is attached when this is a reply — provides the
	// SPF/DKIM/DMARC provenance + sender/subject of the inbound message
	// being replied to so the review panel can render the context pane.
	InboundContext *PendingMessageInboundContext `json:"inbound,omitempty"`
	ReviewedAt     string                        `json:"reviewed_at,omitempty" example:"2025-01-15T10:35:00Z"`
	// ReviewedByUserID identifies the human reviewer for approved or
	// rejected messages. NULL on TTL-expired transitions (worker
	// auto-approve / auto-reject) where no human reviewed the message.
	ReviewedByUserID *string `json:"reviewed_by_user_id,omitempty" example:"usr_abc123"`
	// ReviewedByName is the JOIN'd display name from the reviewer's
	// users row. NULL when reviewed_by_user_id is null (worker) or when
	// the reviewer's user account has since been deleted (the FK has
	// ON DELETE SET NULL specifically so this doesn't poison the audit
	// trail).
	ReviewedByName    *string `json:"reviewed_by_name,omitempty" example:"Jamie"`
	RejectionReason   string  `json:"rejection_reason,omitempty"`
	ProviderMessageID string  `json:"provider_message_id,omitempty"`
	Method            string  `json:"method,omitempty" example:"smtp"`
} // @name PendingMessageDetail

// PendingMessageInboundContext is the inlined inbound-row preview
// attached to a reply's pending detail. Body is intentionally elided
// (the inbound's raw_message is RFC 5322 bytes; the review panel
// surfaces only the headers + auth_headers).
type PendingMessageInboundContext struct {
	Sender    string `json:"sender" example:"alice@gmail.com"`
	Subject   string `json:"subject" example:"contract details"`
	CreatedAt string `json:"created_at" example:"2025-01-15T10:25:00Z"`
	// AuthHeaders carries the SPF/DKIM/DMARC validation results captured
	// at inbound time. Keys are conventionally "spf", "dkim", "dmarc"
	// each with values "pass" | "fail" | "neutral" | etc. The dashboard
	// renders these as found/missing chips on the provenance pane.
	AuthHeaders map[string]string `json:"auth_headers,omitempty"`
} // @name PendingMessageInboundContext

// ListPendingMessagesResponse wraps the array returned by GET /api/v1/messages.
type ListPendingMessagesResponse struct {
	Messages []PendingMessageSummary `json:"messages"`
} // @name ListPendingMessagesResponse

// DeliveryStatus summarizes how many of the matched webhooks have
// received an event so far. Computed at read time by joining against
// webhook_subscriber_deliveries.
type DeliveryStatus struct {
	MatchedWebhooks int `json:"matched_webhooks"`
	Delivered       int `json:"delivered"`
	Pending         int `json:"pending"`
	Failed          int `json:"failed"`
} // @name DeliveryStatus

// WebhookEvent is the wire shape returned by GET /events and GET /events/{id}.
// Mirrors design §4.6.
type WebhookEvent struct {
	ID             string                 `json:"id"`
	Type           string                 `json:"type"`
	SchemaVersion  int                    `json:"schema_version"`
	CreatedAt      string                 `json:"created_at"`
	AgentID        *string                `json:"agent_id,omitempty"`
	ConversationID *string                `json:"conversation_id,omitempty"`
	MessageID      *string                `json:"message_id,omitempty"`
	Status         string                 `json:"status"`
	Data           map[string]interface{} `json:"data"`
	DeliveryStatus *DeliveryStatus        `json:"delivery_status,omitempty"`
} // @name WebhookEvent

// ListEventsResponse wraps the events list.
type ListEventsResponse struct {
	Events    []WebhookEvent `json:"events"`
	NextToken string         `json:"next_token,omitempty"`
} // @name ListEventsResponse

// RedeliverRequest is the body of POST /events/{id}/redeliver.
type RedeliverRequest struct {
	WebhookID string `json:"webhook_id,omitempty"`
} // @name RedeliverRequest

// RedeliverDeliveryResult is one element of a fan-out replay response.
type RedeliverDeliveryResult struct {
	WebhookID  string `json:"webhook_id"`
	DeliveryID string `json:"delivery_id,omitempty"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
} // @name RedeliverDeliveryResult

// RedeliverResponse wraps the result of a replay request.
type RedeliverResponse struct {
	DeliveryID string                    `json:"delivery_id,omitempty"`
	EventID    string                    `json:"event_id"`
	WebhookID  string                    `json:"webhook_id,omitempty"`
	Status     string                    `json:"status"`
	Deliveries []RedeliverDeliveryResult `json:"deliveries,omitempty"`
} // @name RedeliverResponse

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
