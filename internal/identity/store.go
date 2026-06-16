package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/dkim"
	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/inboundpolicy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/idna"
)

// normalizeDomain lowercases and IDNA-normalizes a domain string.
// Internationalized domains are converted to their ASCII (punycode) form.
func normalizeDomain(domain string) string {
	domain = strings.ToLower(domain)
	if ascii, err := idna.Lookup.ToASCII(domain); err == nil {
		return ascii
	}
	return domain
}

// Domain represents a verified or unverified domain registered by a user.
type Domain struct {
	Domain            string     `json:"domain"`
	UserID            *string    `json:"user_id,omitempty"`
	Verified          bool       `json:"verified"`
	VerificationToken string     `json:"verification_token"`
	CreatedAt         time.Time  `json:"created_at"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	// IsPrimary marks the user's default domain. At most one TRUE per
	// user (enforced by a partial unique index in migration 013).
	IsPrimary bool `json:"is_primary"`
	// LastCheckedAt is updated whenever the verification probe runs,
	// successful or not. NULL until the first probe — distinct from
	// "probed and failed" which is captured by `verified=false` + a
	// non-null LastCheckedAt.
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	// AgentCount is computed at read time by ListDomainsByUser and is
	// not a persisted column. Single-domain LookupDomain leaves it at
	// the zero value — callers that need the count call the list path
	// (this column-versus-aggregate split avoids changing every store
	// signature to thread an agent-counter through).
	AgentCount int `json:"agent_count"`
	// DKIM keypair fields. The selector + public key
	// are user-facing — the dashboard shows them so users can copy the
	// DNS TXT record. The private key is intentionally NOT in the JSON
	// shape; it's only read by the outbound signer via
	// GetDKIMKey(domain). Domains created before migration 014 ran
	// keep all three NULL until the next ClaimOrCreate or backfill.
	DKIMSelector  string `json:"dkim_selector,omitempty"`
	DKIMPublicKey string `json:"dkim_public_key,omitempty"`
	// Sender identity (decision 4 / Slice 4). Independent of `Verified`
	// (inbound ownership): SendingStatus tracks the async SES sending
	// identity that lets outbound use the agent's own address as From.
	// SendingStatus ∈ {none,pending,verified,failed}; own-address From is
	// used ONLY when "verified" (fail-closed). SendingDNSRecordsJSON is the
	// raw JSONB (nil when unset) — the API layer unmarshals it for display.
	SendingStatus         string     `json:"sending_status"`
	SendingError          string     `json:"sending_error,omitempty"`
	SendingDNSRecordsJSON []byte     `json:"-"`
	SendingLastCheckedAt  *time.Time `json:"sending_last_checked_at,omitempty"`
}

type AgentIdentity struct {
	ID                   string    `json:"id"`
	Domain               string    `json:"domain"`
	Email                string    `json:"email"`
	Name                 string    `json:"name"`
	DomainVerified       bool      `json:"domain_verified"`
	Public               bool      `json:"public"`
	CreatedAt            time.Time `json:"created_at"`
	UserID               string    `json:"user_id"`
	HITLEnabled          bool      `json:"hitl_enabled"`
	HITLTTLSeconds       int       `json:"hitl_ttl_seconds"`
	HITLExpirationAction string    `json:"hitl_expiration_action"`
	// Dashboard enrichment fields. Computed at read
	// time by ListAgentsByUser via correlated subqueries — other load
	// paths (GetAgentByID / GetAgentByEmail) leave them at zero values,
	// same pattern as Domain.AgentCount. Switch to denormalized columns
	// if the read cost ever bites.
	Inbound7d      int        `json:"inbound_7d"`
	Outbound7d     int        `json:"outbound_7d"`
	PendingCount   int        `json:"pending_count"`
	LastDeliveryAt *time.Time `json:"last_delivery_at,omitempty"`
	// WebhookHealthy is false iff there's been a failed webhook delivery
	// in the last 24h. Defaults to true for agents with no deliveries
	// yet — avoids painting fresh agents red.
	WebhookHealthy bool `json:"webhook_healthy"`
	// InboundPolicy is the per-agent inbound ingestion gate (migration 033 /
	// Slice 7): one of inboundpolicy.{Open,Allowlist,Domain,VerifiedOnly}.
	// Defaults to 'open' (the column default). InboundAllowlist holds the
	// exact addresses (allowlist policy) or domains (domain policy) the gate
	// trusts; empty for open/verified_only.
	InboundPolicy    string   `json:"inbound_policy"`
	InboundAllowlist []string `json:"inbound_allowlist,omitempty"`
	// AssertionVersion is the auth.md kill-switch counter (migration 035 /
	// Slice 5b-2): stamped into minted identity_assertion/access_token JWTs and
	// re-checked at the token endpoint; a bump invalidates prior tokens.
	AssertionVersion int `json:"-"`
}

// HITL constants mirror the CHECK constraints in migration 003_hitl.sql.
const (
	HITLMaxTTLSeconds        = 604800 // 7 days
	HITLDefaultTTLSeconds    = 604800
	HITLExpirationApprove    = "approve"
	HITLExpirationReject     = "reject"
	HITLDefaultExpirationAct = HITLExpirationReject
)

// ValidateHITLConfig returns an error if the TTL or expiration action is invalid.
// The DB CHECK constraints are the final guard; this mirrors them for a
// clean, pre-query error path.
func ValidateHITLConfig(ttlSeconds int, expirationAction string) error {
	if ttlSeconds <= 0 || ttlSeconds > HITLMaxTTLSeconds {
		return fmt.Errorf("hitl_ttl_seconds must be between 1 and %d", HITLMaxTTLSeconds)
	}
	if expirationAction != HITLExpirationApprove && expirationAction != HITLExpirationReject {
		return fmt.Errorf("hitl_expiration_action must be 'approve' or 'reject'")
	}
	return nil
}

// populateEmail sets the Email field from the agent ID (which is the full email).
func (a *AgentIdentity) populateEmail() {
	a.Email = a.ID
}

// IsSharedDomain returns true if the agent's domain matches the configured
// shared domain (the host that backs slug-based registration). When
// sharedDomain is empty, the deployment has slug registration disabled
// and no agent can be on the shared domain.
func (a *AgentIdentity) IsSharedDomain(sharedDomain string) bool {
	return sharedDomain != "" && a.Domain == sharedDomain
}

// ActualDomain returns the DNS domain for the agent.
func (a *AgentIdentity) ActualDomain() string {
	return a.Domain
}

// EmailAddress returns the agent's email address (always the ID).
func (a *AgentIdentity) EmailAddress() string {
	return a.ID
}

type User struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Name          string    `json:"name"`
	GoogleSubject string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
}

type Message struct {
	ID                string            `json:"id"`
	AgentID           string            `json:"agent_id"`
	Direction         string            `json:"direction"`
	Sender            string            `json:"sender"`
	Recipient         string            `json:"recipient"`
	Subject           string            `json:"subject"`
	EmailMessageID    string            `json:"email_message_id,omitempty"`
	ProviderMessageID string            `json:"provider_message_id,omitempty"`
	Method            string            `json:"method,omitempty"`
	Type              string            `json:"type,omitempty"`
	RawMessage        []byte            `json:"raw_message,omitempty"`
	AuthHeaders       map[string]string `json:"auth_headers,omitempty"`
	// Auth carries the parsed inbound authentication verdict
	// (messages.auth_verdict from migration 032): SPF/DKIM/DMARC each with
	// a status and detail. Populated on inbound read paths when the column
	// is non-null; nil on outbound rows (which never have a verdict).
	Auth           *emailauth.Result `json:"auth,omitempty"`
	ConversationID string            `json:"conversation_id,omitempty"`
	// DeliveryStatus is overloaded by direction. On inbound rows it carries
	// the inbox read/unread status (messages.inbox_status) under this legacy
	// JSON key. On outbound rows it carries the outbound delivery rollup
	// (messages.delivery_status from migration 031: 'sent', 'delivered',
	// 'bounced', …) — the worst recipient status by precedence. A message is
	// either inbound or outbound, so the two sources never collide per-row.
	DeliveryStatus string `json:"delivery_status,omitempty"`
	// DeliveryDetail is the human-readable diagnostic for the outbound
	// delivery rollup (e.g. an SES bounce sub-type / SMTP response).
	// Outbound-only; empty on inbound rows. Source: messages.delivery_detail.
	DeliveryDetail string `json:"delivery_detail,omitempty"`
	// SentAs is the From identity actually used when the outbound message was
	// accepted by the relay. Outbound-only; empty on inbound rows. Source:
	// messages.sent_as.
	SentAs          string    `json:"sent_as,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	WebhookStatus   string    `json:"webhook_status,omitempty"`
	WebhookError    string    `json:"webhook_error,omitempty"`
	WebhookAttempts int       `json:"webhook_attempts,omitempty"`
	// SizeBytes is the byte length of raw_message. Populated by load paths
	// that compute it (e.g. GetMessagesByAgent for the dashboard inbox).
	// Zero on load paths that don't — the inbox renders "—" in that case.
	SizeBytes int `json:"size_bytes,omitempty"`
	// InboxStatus mirrors messages.inbox_status ('unread' | 'read') for
	// inbound rows. Kept separate from DeliveryStatus (which currently
	// carries the same value under a confusing JSON key — see line 161)
	// so the dashboard's inbox can read it under a non-overloaded key.
	// Empty on outbound rows. Populated by GetMessagesByAgent.
	InboxStatus string `json:"inbox_status,omitempty"`

	// Multi-recipient fields. For outbound, these are the addressed
	// To/Cc/Bcc recipients of the send. For inbound, ToRecipients and CC
	// are the parsed To: and Cc: headers of the original message (the
	// per-delivery target for this row is in Recipient). BCC is
	// outbound-only.
	ToRecipients []string `json:"to_recipients,omitempty"`
	CC           []string `json:"cc,omitempty"`
	BCC          []string `json:"bcc,omitempty"`

	// ReplyTo is the parsed Reply-To: header on inbound messages — empty
	// when the header was absent. Distinct from Sender so consumers can
	// recover the original From: of forwarded / notification mail whose
	// Reply-To points at a different mailbox. Outbound-irrelevant.
	ReplyTo []string `json:"reply_to,omitempty"`

	// Labels are user-applied string tags (`urgent`, `follow-up`, …).
	// Always lowercase, charset `[a-z0-9:_-]+`, ≤ 64 chars per label,
	// capped at 100 per message. Empty slice means no labels — the DB
	// default is `'{}'` so this is never null on read. Labels with the
	// `e2a:` prefix are reserved for server-applied system labels;
	// caller writes that try to set them are rejected at the API layer.
	Labels []string `json:"labels,omitempty"`

	// HITL approval fields. Status defaults to 'sent'; body and attachments
	// are populated only while a message is in 'pending_approval', and are
	// scrubbed on any terminal transition.
	Status            string     `json:"status,omitempty"`
	ApprovalExpiresAt *time.Time `json:"approval_expires_at,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	// ReviewedByUserID identifies the human reviewer who approved or
	// rejected this message. NULL on worker-triggered transitions
	// (TTL auto-approve / auto-reject) — operator-visible signal "no
	// human looked at this." Set by ApproveAndSend and RejectPending,
	// left null by ExpireApproveAndSend / ExpireReject.
	ReviewedByUserID *string `json:"reviewed_by_user_id,omitempty"`
	// ReviewedByName is the JOIN'd display name from the reviewer's
	// users row, populated only by GetOutboundMessageForUser. List
	// endpoints leave this empty to avoid a join-per-row cost — the
	// pending-detail page is where reviewer attribution matters.
	ReviewedByName  *string         `json:"reviewed_by_name,omitempty"`
	RejectionReason string          `json:"rejection_reason,omitempty"`
	Edited          bool            `json:"edited,omitempty"`
	BodyText        string          `json:"body_text,omitempty"`
	BodyHTML        string          `json:"body_html,omitempty"`
	AttachmentsJSON json.RawMessage `json:"attachments,omitempty"`

	// Flagged + FlagReason carry the inbound ingestion verdict (migration 033 /
	// Slice 7): true when the agent's inbound_policy gate flagged this message
	// on arrival (still delivered, never dropped). FlagReason is the
	// human-readable reason. Inbound-relevant; outbound rows read false/''.
	Flagged    bool   `json:"flagged,omitempty"`
	FlagReason string `json:"flag_reason,omitempty"`
}

// Message status values mirror the CHECK constraint in migration 003_hitl.sql.
const (
	MessageStatusSent            = "sent"
	MessageStatusPendingApproval = "pending_approval"
	MessageStatusRejected        = "rejected"
	MessageStatusExpiredApproved = "expired_approved"
	MessageStatusExpiredRejected = "expired_rejected"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// --- Domain CRUD ---

// EnsureSharedDomain inserts a system row for the configured shared
// mail domain so slug-based agent registration can satisfy the
// agent_identities.domain → domains.domain foreign key. The row is
// owned by no user (user_id = NULL) and pre-verified — it represents
// infrastructure the operator runs, not user-claimed identity.
//
// Called once at server startup. Idempotent via ON CONFLICT DO NOTHING,
// and a no-op when the operator has not configured a shared domain.
// Without this, any deployment whose shared_domain differs from the
// hardcoded migration seed (`agents.e2a.dev`) gets an FK violation the
// first time a user tries to register a slug-based agent.
func (s *Store) EnsureSharedDomain(ctx context.Context, domain string) error {
	if domain == "" {
		return nil
	}
	domain = normalizeDomain(domain)
	_, err := s.pool.Exec(ctx,
		`INSERT INTO domains (domain, user_id, verified, verified_at)
		 VALUES ($1, NULL, true, now())
		 ON CONFLICT (domain) DO NOTHING`,
		domain,
	)
	if err != nil {
		return fmt.Errorf("ensure shared domain %q: %w", domain, err)
	}
	return nil
}

// ClaimOrCreateDomain implements the atomic create/claim logic from the design doc.
// Creates if new, returns the existing row when the same user already owns
// it (verified or not), and errors if a different user owns it. The
// verification_token and DKIM keypair are minted on first INSERT and remain
// stable across re-claims — a caller that has already published the TXT
// record on DNS (or has mail in flight signed with the DKIM key) isn't
// silently invalidated by a second call. A different user cannot take
// over an unverified row; that closes a squatting window where the new
// owner could verify against a TXT record the original owner already
// published. Callers are responsible for rejecting the configured shared
// domain before invoking this — the store has no concept of a reserved
// domain.
func (s *Store) ClaimOrCreateDomain(ctx context.Context, domain, userID string) (*Domain, error) {
	domain = normalizeDomain(domain)

	verificationToken := "e2a-verify=" + generateID()

	// Generate a DKIM keypair for this domain. Failures here are
	// non-fatal — the columns are nullable and the outbound signer
	// treats a missing key as "skip DKIM". We still log because key gen
	// failing is a hard signal (entropy exhaustion or an OS-level
	// CSPRNG bug) that ops should see.
	var dkimSelector string
	var dkimPubKey string
	var dkimPrivKey []byte
	if kp, kerr := dkim.GenerateKeypair(); kerr == nil {
		dkimSelector = kp.Selector
		dkimPubKey = kp.PublicKeyDNS
		dkimPrivKey = kp.PrivateKeyDER
	} else {
		log.Printf("[identity] dkim keygen failed for %s: %v", domain, kerr)
	}

	// Atomic upsert. The conflict branch only fires for a same-user
	// re-claim of an unverified row, and runs as a no-op SET so
	// RETURNING surfaces the existing row. DKIM columns and the
	// verification_token are only written on a true INSERT, so they
	// stay stable across re-claims — DKIM stability avoids
	// invalidating signatures on mail in flight, and token stability
	// means a caller who already published the TXT record on DNS
	// isn't silently invalidated. A different-user conflict falls
	// through to the SELECT below and returns "domain not available",
	// preventing squatting on an unverified row whose TXT record the
	// original owner may have already published.
	d := &Domain{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO domains (domain, user_id, verified, verification_token, dkim_selector, dkim_public_key, dkim_private_key)
		 VALUES ($1, $2, false, $3, $4, $5, $6)
		 ON CONFLICT (domain) DO UPDATE
		 SET user_id = domains.user_id
		 WHERE domains.verified = false AND domains.user_id = $2
		 RETURNING domain, user_id, verified, verification_token, created_at, verified_at, is_primary, last_checked_at, COALESCE(dkim_selector, ''), COALESCE(dkim_public_key, ''), sending_status, COALESCE(sending_error, ''), sending_dns_records, sending_last_checked_at`,
		domain, userID, verificationToken, nullIfEmpty(dkimSelector), nullIfEmpty(dkimPubKey), nullIfEmptyBytes(dkimPrivKey),
	).Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt, &d.IsPrimary, &d.LastCheckedAt, &d.DKIMSelector, &d.DKIMPublicKey, &d.SendingStatus, &d.SendingError, &d.SendingDNSRecordsJSON, &d.SendingLastCheckedAt)

	if err == nil {
		return d, nil
	}

	// No row returned — the row exists but the conflict UPDATE was
	// skipped because either it's already verified or a different user
	// owns it. Re-read to decide between "verified + same user → return
	// it" and "different user → not available".
	existing := &Domain{}
	err = s.pool.QueryRow(ctx,
		`SELECT domain, user_id, verified, verification_token, created_at, verified_at, is_primary, last_checked_at, COALESCE(dkim_selector, ''), COALESCE(dkim_public_key, ''), sending_status, COALESCE(sending_error, ''), sending_dns_records, sending_last_checked_at
		 FROM domains WHERE domain = $1`, domain,
	).Scan(&existing.Domain, &existing.UserID, &existing.Verified, &existing.VerificationToken, &existing.CreatedAt, &existing.VerifiedAt, &existing.IsPrimary, &existing.LastCheckedAt, &existing.DKIMSelector, &existing.DKIMPublicKey, &existing.SendingStatus, &existing.SendingError, &existing.SendingDNSRecordsJSON, &existing.SendingLastCheckedAt)
	if err != nil {
		return nil, fmt.Errorf("domain lookup failed: %w", err)
	}

	if existing.UserID != nil && *existing.UserID == userID {
		return existing, nil // verified + same user
	}

	return nil, fmt.Errorf("domain not available")
}

// nullIfEmpty returns nil for empty strings so we can write SQL NULL
// (rather than empty-string) for nullable text columns. Pgx treats an
// untyped nil interface{} as NULL.
func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nullIfEmptyBytes is the BYTEA counterpart of nullIfEmpty.
func nullIfEmptyBytes(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return b
}

// GetDKIMKeyInternal returns the stored selector + private key bytes
// for a domain. The "Internal" suffix is load-bearing: this function
// does NOT scope by user — it takes a domain name and returns whoever
// owns that domain's signing key. ONLY call from server-internal
// codepaths where the domain has already been resolved from a
// trusted source (e.g. an outbound message's sender field, after the
// agent layer has authenticated the owner). A handler that ever
// takes a user-supplied domain string and feeds it to this function
// becomes a "sign as anyone" primitive: don't.
//
// Returns ("", nil, nil) when the domain has no key — callers MUST
// treat this as "skip signing" and fall back to whatever the
// relay-level fallback does.
func (s *Store) GetDKIMKeyInternal(ctx context.Context, domain string) (string, []byte, error) {
	var selector string
	var privKey []byte
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(dkim_selector, ''), dkim_private_key FROM domains WHERE domain = $1`,
		normalizeDomain(domain),
	).Scan(&selector, &privKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("dkim key lookup: %w", err)
	}
	return selector, privKey, nil
}

// --- Sender identity (decision 4 / Slice 4) ---
//
// These primitive accessors back the senderidentity.RawStore interface
// (string status, JSON dns records) so the core store stays decoupled from
// the senderidentity package (and its River + AWS SDK deps). The adapter in
// senderidentity converts to its typed Status/DNSRecord.

// SendingProvisionInputs returns the per-domain DKIM selector + private key
// for BYODKIM provisioning. ok=false means no usable key material. Like
// GetDKIMKeyInternal this is unscoped — call only with a server-resolved
// domain.
func (s *Store) SendingProvisionInputs(ctx context.Context, domain string) (selector string, privateKeyDER []byte, ok bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT COALESCE(dkim_selector, ''), dkim_private_key FROM domains WHERE domain = $1`,
		normalizeDomain(domain),
	).Scan(&selector, &privateKeyDER)
	if err != nil {
		return "", nil, false, err // includes pgx.ErrNoRows (domain gone)
	}
	if selector == "" || len(privateKeyDER) == 0 {
		return "", nil, false, nil
	}
	return selector, privateKeyDER, true, nil
}

// SetSendingStatus writes the sending lifecycle state for a domain and stamps
// sending_last_checked_at. recordsJSON may be nil (cleared).
func (s *Store) SetSendingStatus(ctx context.Context, domain, status, errMsg string, recordsJSON []byte) error {
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE domains
		    SET sending_status = $2,
		        sending_error = $3,
		        sending_dns_records = $4,
		        sending_last_checked_at = now()
		  WHERE domain = $1`,
		normalizeDomain(domain), status, errPtr, recordsJSON,
	)
	return err
}

// TouchSendingChecked stamps sending_last_checked_at without changing status
// (a still-pending poll).
func (s *Store) TouchSendingChecked(ctx context.Context, domain string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE domains SET sending_last_checked_at = now() WHERE domain = $1`,
		normalizeDomain(domain),
	)
	return err
}

// GetSendingStatus returns the domain's sending_status. Propagates
// pgx.ErrNoRows when the domain row is gone.
func (s *Store) GetSendingStatus(ctx context.Context, domain string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT sending_status FROM domains WHERE domain = $1`,
		normalizeDomain(domain),
	).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

// DomainOwner returns the user_id owning a domain, or "" for an unowned
// (system) domain. pgx.ErrNoRows → ("", nil) so the caller treats a missing
// domain as "no owner, no event".
func (s *Store) DomainOwner(ctx context.Context, domain string) (string, error) {
	var owner *string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id FROM domains WHERE domain = $1`,
		normalizeDomain(domain),
	).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if owner == nil {
		return "", nil
	}
	return *owner, nil
}

// DomainExists reports whether a live domain row exists (orphan reaper).
func (s *Store) DomainExists(ctx context.Context, domain string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM domains WHERE domain = $1)`,
		normalizeDomain(domain),
	).Scan(&exists)
	return exists, err
}

// LookupDomain returns a domain if it exists and is owned by the given user.
func (s *Store) LookupDomain(ctx context.Context, domain, userID string) (*Domain, error) {
	d := &Domain{}
	err := s.pool.QueryRow(ctx,
		`SELECT domain, user_id, verified, verification_token, created_at, verified_at, is_primary, last_checked_at, COALESCE(dkim_selector, ''), COALESCE(dkim_public_key, ''), sending_status, COALESCE(sending_error, ''), sending_dns_records, sending_last_checked_at
		 FROM domains WHERE domain = $1 AND user_id = $2`,
		normalizeDomain(domain), userID,
	).Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt, &d.IsPrimary, &d.LastCheckedAt, &d.DKIMSelector, &d.DKIMPublicKey, &d.SendingStatus, &d.SendingError, &d.SendingDNSRecordsJSON, &d.SendingLastCheckedAt)
	if err != nil {
		return nil, fmt.Errorf("domain not found")
	}
	return d, nil
}

// VerifyDomain marks a domain as verified, only if owned by the given user.
func (s *Store) VerifyDomain(ctx context.Context, domain, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE domains SET verified = true, verified_at = now()
		 WHERE domain = $1 AND user_id = $2 AND verified = false`,
		normalizeDomain(domain), userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("domain not found, not owned by user, or already verified")
	}
	return nil
}

// ListDomainsByUser returns all domains owned by the user (excludes system rows).
// AgentCount is computed inline via a correlated subquery — one round-trip
// regardless of how many domains the user has, and the per-row count is
// cheap because (agent_identities.user_id, agent_identities.domain) is
// indexed via the existing idx_agent_identities_user.
func (s *Store) ListDomainsByUser(ctx context.Context, userID string) ([]Domain, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT d.domain, d.user_id, d.verified, d.verification_token, d.created_at, d.verified_at,
		        d.is_primary, d.last_checked_at,
		        COALESCE(d.dkim_selector, ''), COALESCE(d.dkim_public_key, ''),
		        d.sending_status, COALESCE(d.sending_error, ''), d.sending_dns_records, d.sending_last_checked_at,
		        (SELECT count(*) FROM agent_identities a WHERE a.domain = d.domain AND a.user_id = d.user_id) AS agent_count
		 FROM domains d
		 WHERE d.user_id = $1
		 ORDER BY d.created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var domains []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt, &d.IsPrimary, &d.LastCheckedAt, &d.DKIMSelector, &d.DKIMPublicKey, &d.SendingStatus, &d.SendingError, &d.SendingDNSRecordsJSON, &d.SendingLastCheckedAt, &d.AgentCount); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

// SetDomainPrimary marks a domain as the user's primary in a single
// transaction: first clear any other primary belonging to the user, then
// set the requested domain. The partial unique index in migration 013
// makes the clear-first step necessary — otherwise the two writes would
// race and one would fail with a unique violation.
//
// Returns ErrDomainNotFound when the domain doesn't exist or isn't owned
// by the user.
func (s *Store) SetDomainPrimary(ctx context.Context, domain, userID string) error {
	domain = normalizeDomain(domain)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx,
		`UPDATE domains SET is_primary = false WHERE user_id = $1 AND is_primary = true`,
		userID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE domains SET is_primary = true WHERE domain = $1 AND user_id = $2`,
		domain, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDomainNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}

// TouchDomainLastChecked records that the verification probe ran. Call
// this from POST /api/v1/domains/{domain}/verify whether the probe
// succeeded or not — the LastCheckedAt column is "when did we last try",
// not "when did we last succeed" (the latter is verified_at).
func (s *Store) TouchDomainLastChecked(ctx context.Context, domain, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE domains SET last_checked_at = now() WHERE domain = $1 AND user_id = $2`,
		normalizeDomain(domain), userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDomainNotFound
	}
	return nil
}

// HasAgentsOnDomain checks whether the owned domain still has agents.
func (s *Store) HasAgentsOnDomain(ctx context.Context, domain, userID string) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_identities WHERE domain = $1 AND user_id = $2`,
		normalizeDomain(domain), userID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ErrDomainHasAgents is returned when a domain delete is blocked by existing agents.
var ErrDomainHasAgents = fmt.Errorf("cannot delete domain: agents still exist")

// ErrDomainNotFound is returned when a domain is not found or not owned by the user.
var ErrDomainNotFound = fmt.Errorf("domain not found or not owned by user")

// DeleteDomain deletes a domain only if owned by the user.
// The handler should check for existing agents first.
func (s *Store) DeleteDomain(ctx context.Context, domain, userID string) error {
	return s.DeleteDomainTx(ctx, domain, userID, nil)
}

// DeleteDomainTx deletes a domain and, before committing, runs inTx within
// the SAME transaction. The hook is how sender-identity teardown is enqueued
// transactionally (decision 4): the River deprovision job is committed
// atomically with the domain-row delete, so it can never be lost if SES is
// unreachable at delete time. A nil hook is a plain delete (dev / no SES).
//
// inTx runs only after the DELETE affected a row (the domain existed and was
// owned by userID); it never runs for a not-found / FK-blocked delete.
func (s *Store) DeleteDomainTx(ctx context.Context, domain, userID string, inTx func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`DELETE FROM domains WHERE domain = $1 AND user_id = $2`,
		normalizeDomain(domain), userID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "violates foreign key") {
			return ErrDomainHasAgents
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDomainNotFound
	}
	if inTx != nil {
		if err := inTx(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// --- Agent CRUD ---

// GetAgentByID looks up an agent by its ID (full email) with domain verification status.
func (s *Store) GetAgentByID(ctx context.Context, id string) (*AgentIdentity, error) {
	a := &AgentIdentity{}
	err := s.pool.QueryRow(ctx,
		`SELECT a.id, a.domain, a.user_id, a.name, a.public, a.created_at,
		        a.hitl_enabled, a.hitl_ttl_seconds, a.hitl_expiration_action,
		        COALESCE(a.inbound_policy, 'open'), a.inbound_allowlist,
		        COALESCE(a.assertion_version, 1),
		        d.verified as domain_verified
		 FROM agent_identities a
		 JOIN domains d ON a.domain = d.domain
		 WHERE a.id = $1`, id,
	).Scan(&a.ID, &a.Domain, &a.UserID, &a.Name, &a.Public, &a.CreatedAt,
		&a.HITLEnabled, &a.HITLTTLSeconds, &a.HITLExpirationAction,
		&a.InboundPolicy, &a.InboundAllowlist,
		&a.AssertionVersion,
		&a.DomainVerified)
	if err != nil {
		return nil, err
	}
	a.populateEmail()
	return a, nil
}

// GetAgentByEmail looks up an agent by email address (same as GetAgentByID since ID = email).
func (s *Store) GetAgentByEmail(ctx context.Context, email string) (*AgentIdentity, error) {
	return s.GetAgentByID(ctx, email)
}

// CreateAgent inserts an agent with a domain FK. Does NOT check domain ownership —
// that's the API handler's responsibility (shared domain skips the check).
//
// webhookURL and agentMode are accepted for signature compatibility but are
// now IGNORED: the legacy per-agent webhook_url + agent_mode columns were
// dropped (migration 029). Push is delivered solely via the /v1/webhooks
// subscriber resource and WebSocket is open to all agents. The params are
// retained to avoid churning the ~80 call-sites that still pass them; the
// internal-signature cleanup is a separate follow-up.
func (s *Store) CreateAgent(ctx context.Context, agentEmail, domain, name, webhookURL, agentMode, userID string) (*AgentIdentity, error) {
	return createAgent(ctx, s.pool, agentEmail, domain, name, userID)
}

// CreateAgentTx inserts an agent inside a caller-owned transaction.
// Used by the OAuth consent flow so the slug auto-create row and the
// authorization-code insert (in oauth_auth_codes) commit together —
// without this, a code-issue failure after the agent commit would
// leave a phantom inbox the user never authorized.
// webhookURL and agentMode are accepted but IGNORED — see CreateAgent.
func (s *Store) CreateAgentTx(ctx context.Context, tx pgx.Tx, agentEmail, domain, name, webhookURL, agentMode, userID string) (*AgentIdentity, error) {
	return createAgent(ctx, tx, agentEmail, domain, name, userID)
}

// agentExecutor is the subset of pgxpool.Pool + pgx.Tx that
// createAgent needs. Lets the same body serve both stand-alone and
// in-transaction callers without duplicating the SQL.
type agentExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func createAgent(ctx context.Context, exec agentExecutor, agentEmail, domain, name, userID string) (*AgentIdentity, error) {
	a := &AgentIdentity{
		ID:                   agentEmail,
		Domain:               normalizeDomain(domain),
		Name:                 name,
		Public:               true,
		CreatedAt:            time.Now(),
		UserID:               userID,
		HITLEnabled:          false,
		HITLTTLSeconds:       HITLDefaultTTLSeconds,
		HITLExpirationAction: HITLDefaultExpirationAct,
	}
	_, err := exec.Exec(ctx,
		`INSERT INTO agent_identities (id, domain, user_id, name, public, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		a.ID, a.Domain, a.UserID, a.Name, a.Public, a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	a.populateEmail()
	return a, nil
}

// UpdateAgentHITL updates all three HITL settings on an agent owned by userID.
// The TTL and expiration action are validated against the same rules as the
// DB CHECK constraints so callers get a clean error rather than a raw SQL error.
func (s *Store) UpdateAgentHITL(ctx context.Context, agentID, userID string, enabled bool, ttlSeconds int, expirationAction string) error {
	if err := ValidateHITLConfig(ttlSeconds, expirationAction); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_identities
		    SET hitl_enabled = $1,
		        hitl_ttl_seconds = $2,
		        hitl_expiration_action = $3
		  WHERE id = $4 AND user_id = $5`,
		enabled, ttlSeconds, expirationAction, agentID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found or not owned by user")
	}
	return nil
}

// UpdateAgentInboundPolicy sets the inbound ingestion gate (migration 033 /
// Slice 7) on an agent owned by userID. The policy is validated against
// inboundpolicy.Valid so callers get a clean error rather than a raw CHECK
// violation. allowlist may be empty (the gate then flags everything for the
// gating postures — fail-closed). Returns an error if the agent isn't found
// or isn't owned by the user.
// maxInboundAllowlist bounds the per-agent inbound_allowlist. The relay scans
// it linearly on every inbound message, so an unbounded list is an owner-scoped
// DoS vector; 1000 entries is far beyond any real allow/deny need.
const maxInboundAllowlist = 1000

func (s *Store) UpdateAgentInboundPolicy(ctx context.Context, agentID, userID, policy string, allowlist []string) error {
	if !inboundpolicy.Valid(policy) {
		return fmt.Errorf("invalid inbound_policy %q", policy)
	}
	if len(allowlist) > maxInboundAllowlist {
		return fmt.Errorf("inbound_allowlist has %d entries, max %d", len(allowlist), maxInboundAllowlist)
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_identities
		    SET inbound_policy = $3,
		        inbound_allowlist = $4
		  WHERE id = $1 AND user_id = $2`,
		agentID, userID, policy, allowlist,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found or not owned by user")
	}
	return nil
}

// ListAgentsByUser returns all agents owned by the user, joined with
// domain verification AND enriched with per-agent stats for the
// dashboard. Five correlated subqueries compute
// inbound/outbound 7-day counts, pending approvals, last delivery, and
// webhook health in a single round-trip. Other load paths
// (GetAgentByID, GetAgentByEmail) intentionally don't compute these —
// only the dashboard needs them.
func (s *Store) ListAgentsByUser(ctx context.Context, userID string) ([]AgentIdentity, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT a.id, a.domain, a.user_id, a.name, a.public, a.created_at,
		        a.hitl_enabled, a.hitl_ttl_seconds, a.hitl_expiration_action,
		        COALESCE(a.inbound_policy, 'open'), a.inbound_allowlist,
		        d.verified as domain_verified,
		        (SELECT count(*) FROM messages m
		           WHERE m.agent_id = a.id AND m.direction = 'inbound'
		             AND m.created_at > now() - interval '7 days') AS inbound_7d,
		        (SELECT count(*) FROM messages m
		           WHERE m.agent_id = a.id AND m.direction = 'outbound'
		             AND m.created_at > now() - interval '7 days') AS outbound_7d,
		        (SELECT count(*) FROM messages m
		           WHERE m.agent_id = a.id AND m.status = 'pending_approval') AS pending_count,
		        (SELECT max(m.created_at) FROM messages m
		           WHERE m.agent_id = a.id AND m.direction = 'outbound'
		             AND m.status = 'sent') AS last_delivery_at,
		        NOT EXISTS (
		           SELECT 1 FROM webhook_deliveries wd
		           JOIN messages m ON m.id = wd.message_id
		           WHERE m.agent_id = a.id
		             AND wd.status = 'failed'
		             AND wd.last_attempt_at > now() - interval '24 hours'
		        ) AS webhook_healthy
		 FROM agent_identities a
		 JOIN domains d ON a.domain = d.domain
		 WHERE a.user_id = $1
		 ORDER BY a.created_at DESC`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []AgentIdentity
	for rows.Next() {
		var a AgentIdentity
		var lastDeliveryAt *time.Time
		if err := rows.Scan(&a.ID, &a.Domain, &a.UserID, &a.Name, &a.Public, &a.CreatedAt,
			&a.HITLEnabled, &a.HITLTTLSeconds, &a.HITLExpirationAction,
			&a.InboundPolicy, &a.InboundAllowlist,
			&a.DomainVerified,
			&a.Inbound7d, &a.Outbound7d, &a.PendingCount,
			&lastDeliveryAt, &a.WebhookHealthy); err != nil {
			return nil, err
		}
		a.LastDeliveryAt = lastDeliveryAt
		a.populateEmail()
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) DeleteAgent(ctx context.Context, agentID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM agent_identities WHERE id = $1 AND user_id = $2`, agentID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found or not owned by user")
	}
	return nil
}

// --- Messages ---

// MessageTTL is the per-row lifetime for `messages`. The janitor at
// DeleteExpiredMessages drops rows whose expires_at has passed.
//
// 10 days is chosen to strictly exceed HITLMaxTTLSeconds (7 days) with
// a 3-day buffer. The buffer guarantees:
//   - the HITL worker (60s cadence) always wins the race against the
//     messages janitor (hourly cadence) on max-HITL pending rows;
//   - terminal HITL rows retain ≥3 days of post-resolution audit
//     visibility before the metadata row is dropped;
//   - reply-composition can load a parent inbound up to 10 days old
//     for quoting context.
//
// If HITLMaxTTLSeconds is ever raised, raise this too — keep
// MessageTTL > HITLMaxTTLSeconds by at least 1 day.
const MessageTTL = 10 * 24 * time.Hour // 10 days

// NewMessageID returns a fresh internal message ID. Callers can use this
// to generate the ID up-front when they need it before storing — for
// example, the SMTP relay generates the ID before signing auth headers
// so the ID is part of the canonical string fed to HMAC.
func NewMessageID() string {
	return "msg_" + generateID()
}

// CreateInboundMessage stores an inbound message. If id is empty a new
// one is generated; otherwise the caller's pre-generated ID is used so
// the upstream signer can bind auth headers to the same ID that gets
// stored. toRecipients and cc are the parsed To: and Cc: headers from
// the original RFC 2822 message; recipient is the per-delivery target
// for this row (may be one of the To: addresses, or absent from the
// header list when the agent was Bcc'd). replyTo is the parsed Reply-To:
// header (empty when absent — never silently falls back to sender).
func (s *Store) CreateInboundMessage(ctx context.Context, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus string, rawMessage []byte, authHeaders map[string]string, authVerdict []byte, flagged bool, flagReason string, toRecipients, cc, replyTo []string) (*Message, error) {
	return createInboundMessage(ctx, s.pool, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus, rawMessage, authHeaders, authVerdict, flagged, flagReason, toRecipients, cc, replyTo)
}

// WithTx opens a transaction, runs fn inside it, and commits if fn
// returns nil (or rolls back if fn returns an error). Used by the
// slice-3 relay refactor so the messages INSERT and the
// webhook_events outbox INSERT commit together, closing the
// at-least-once publish-loss window.
//
// The relay handler is the primary v1 caller; future trigger sites
// (slice 4 outbound + HITL) reuse the same helper. Keeps callers from
// having to import pgxpool directly.
func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateInboundMessageInTx writes the messages row inside the caller's
// transaction. Used by the slice-3 relay refactor (per design §4.2) so
// the messages INSERT and the webhook_events outbox INSERT commit
// together, closing the at-least-once publish-loss window.
//
// Mirrors the CreateAgentTx pattern at store.go:596-607 — same SQL
// body, executed against either *pgxpool.Pool or pgx.Tx via the
// messageExecutor interface below.
func (s *Store) CreateInboundMessageInTx(ctx context.Context, tx pgx.Tx, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus string, rawMessage []byte, authHeaders map[string]string, authVerdict []byte, flagged bool, flagReason string, toRecipients, cc, replyTo []string) (*Message, error) {
	return createInboundMessage(ctx, tx, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus, rawMessage, authHeaders, authVerdict, flagged, flagReason, toRecipients, cc, replyTo)
}

// messageExecutor is the subset of *pgxpool.Pool and pgx.Tx that
// createInboundMessage needs. Parallel to agentExecutor (which already
// lives in this file for createAgent) — same shape, different scope.
type messageExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func createInboundMessage(ctx context.Context, exec messageExecutor, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus string, rawMessage []byte, authHeaders map[string]string, authVerdict []byte, flagged bool, flagReason string, toRecipients, cc, replyTo []string) (*Message, error) {
	if id == "" {
		id = NewMessageID()
	}
	now := time.Now()

	var authHeadersJSON []byte
	if authHeaders != nil {
		var err error
		authHeadersJSON, err = json.Marshal(authHeaders)
		if err != nil {
			return nil, fmt.Errorf("marshal auth headers: %w", err)
		}
	}

	m := &Message{
		ID:             id,
		AgentID:        agentID,
		Direction:      "inbound",
		Sender:         senderEmail,
		Recipient:      recipient,
		ToRecipients:   toRecipients,
		CC:             cc,
		ReplyTo:        replyTo,
		Subject:        subject,
		EmailMessageID: emailMessageID,
		RawMessage:     rawMessage,
		AuthHeaders:    authHeaders,
		ConversationID: conversationID,
		DeliveryStatus: deliveryStatus,
		Flagged:        flagged,
		FlagReason:     flagReason,
		CreatedAt:      now,
		ExpiresAt:      now.Add(MessageTTL),
	}
	// inbox_status column has CHECK constraint: must be 'unread', 'read', or NULL
	var inboxStatus *string
	if m.DeliveryStatus == "unread" || m.DeliveryStatus == "read" {
		inboxStatus = &m.DeliveryStatus
	}
	_, err := exec.Exec(ctx,
		`INSERT INTO messages (id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, raw_message, auth_headers, auth_verdict, flagged, flag_reason, conversation_id, inbox_status, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		m.ID, m.AgentID, m.Direction, m.Sender, m.Recipient, m.ToRecipients, m.CC, m.ReplyTo, m.Subject, m.EmailMessageID, m.RawMessage, authHeadersJSON, nullIfEmptyBytes(authVerdict), m.Flagged, nullIfEmptyString(m.FlagReason), m.ConversationID, inboxStatus, m.CreatedAt, m.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) GetInboundMessage(ctx context.Context, id string) (*Message, error) {
	m := &Message{}
	var authVerdict []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, raw_message, auth_verdict, COALESCE(flagged, false), COALESCE(flag_reason, ''), created_at, expires_at
		 FROM messages WHERE id = $1 AND direction = 'inbound' AND expires_at > now()`, id,
	).Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo, &m.Subject, &m.EmailMessageID, &m.RawMessage, &authVerdict, &m.Flagged, &m.FlagReason, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if err := unmarshalAuthVerdict(authVerdict, m); err != nil {
		return nil, err
	}
	return m, nil
}

// unmarshalAuthVerdict parses the messages.auth_verdict JSONB column into
// m.Auth. A NULL/empty column (every outbound row, and inbound rows written
// before migration 032) leaves m.Auth nil.
func unmarshalAuthVerdict(b []byte, m *Message) error {
	if len(b) == 0 {
		return nil
	}
	var r emailauth.Result
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("unmarshal auth verdict: %w", err)
	}
	m.Auth = &r
	return nil
}

// GetInboundByEmailMessageID looks up an inbound message by its RFC 5322
// Message-ID for the given agent. Used by HITL flows to reach the parent
// inbound at approval time so the References chain can be rebuilt — the
// pending-outbound row only stores the parent's Message-ID, not its raw
// message. Scoped to agent_id to prevent any cross-agent reach across
// shared infra. Returns sql.ErrNoRows when the inbound has expired or
// was never persisted; callers must tolerate that and fall back to
// legacy single-id threading.
//
// auth_headers is included in the SELECT so HITL review handlers can
// surface SPF/DKIM/DMARC provenance on the reply-context pane.
func (s *Store) GetInboundByEmailMessageID(ctx context.Context, agentID, emailMessageID string) (*Message, error) {
	if emailMessageID == "" {
		return nil, fmt.Errorf("empty email_message_id")
	}
	m := &Message{}
	var authHeaders map[string]string
	err := s.pool.QueryRow(ctx,
		`SELECT id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, raw_message, auth_headers, created_at, expires_at
		 FROM messages
		 WHERE agent_id = $1
		   AND direction = 'inbound'
		   AND email_message_id = $2
		   AND expires_at > now()
		 ORDER BY created_at DESC LIMIT 1`,
		agentID, emailMessageID,
	).Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo, &m.Subject, &m.EmailMessageID, &m.RawMessage, &authHeaders, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	m.AuthHeaders = authHeaders
	return m, nil
}

// CreateOutboundMessage stores an outbound message with multi-recipient support.
// The recipient param is kept for backward compat with the singular recipient column;
// toRecipients, cc, and bcc are the canonical outbound-only multi-recipient fields.
func (s *Store) CreateOutboundMessage(ctx context.Context, agentID string, toRecipients []string, cc []string, bcc []string, subject, msgType, method, providerMessageID, conversationID string) (*Message, error) {
	id := "msg_" + generateID()
	now := time.Now()

	// Use first To recipient as the singular recipient column for backward compat
	var recipient string
	if len(toRecipients) > 0 {
		recipient = toRecipients[0]
	}

	m := &Message{
		ID:                id,
		AgentID:           agentID,
		Direction:         "outbound",
		Recipient:         recipient,
		Subject:           subject,
		Type:              msgType,
		Method:            method,
		ProviderMessageID: providerMessageID,
		ConversationID:    conversationID,
		CreatedAt:         now,
		ExpiresAt:         now.Add(MessageTTL),
		ToRecipients:      toRecipients,
		CC:                cc,
		BCC:               bcc,
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, agent_id, direction, recipient, subject, message_type, method, provider_message_id, conversation_id, created_at, expires_at, to_recipients, cc, bcc, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		m.ID, m.AgentID, m.Direction, m.Recipient, m.Subject, m.Type, m.Method, m.ProviderMessageID, m.ConversationID, m.CreatedAt, m.ExpiresAt, m.ToRecipients, m.CC, m.BCC, MessageStatusSent,
	)
	if err != nil {
		return nil, err
	}
	m.Status = MessageStatusSent
	return m, nil
}

// CreatePendingOutboundMessage stores a fully composed outbound email in
// pending_approval status, including body_text, body_html, and attachments so
// that approval can reconstruct the original SendRequest (or accept edits)
// without the caller needing to retain it. ttlSeconds sets how long the
// message remains pending before the expiration worker resolves it.
//
// replyToEmailMessageID is the RFC 5322 Message-ID of the inbound being
// replied to (e.g. "<abc@gmail.com>"), or "" for fresh sends and test emails.
// It reuses the email_message_id column, which is unused for outbound rows
// in every other path — the column semantically carries "the Message-ID this
// row references" in both directions.
//
// attachmentsJSON must be a JSON array matching the public Attachment shape
// ([{filename, content_type, data}, ...]) or nil. Callers that already have
// an []outbound.Attachment slice should json.Marshal it before passing in.
func (s *Store) CreatePendingOutboundMessage(ctx context.Context, agentID string, toRecipients, cc, bcc []string, subject, bodyText, bodyHTML string, attachmentsJSON []byte, msgType, conversationID, replyToEmailMessageID string, ttlSeconds int) (*Message, error) {
	if ttlSeconds <= 0 || ttlSeconds > HITLMaxTTLSeconds {
		return nil, fmt.Errorf("ttl_seconds must be between 1 and %d", HITLMaxTTLSeconds)
	}

	id := "msg_" + generateID()
	now := time.Now()
	approvalExpiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)

	var recipient string
	if len(toRecipients) > 0 {
		recipient = toRecipients[0]
	}

	var attachmentsArg interface{}
	if len(attachmentsJSON) > 0 {
		attachmentsArg = attachmentsJSON
	}

	m := &Message{
		ID:                id,
		AgentID:           agentID,
		Direction:         "outbound",
		Recipient:         recipient,
		Subject:           subject,
		EmailMessageID:    replyToEmailMessageID,
		Type:              msgType,
		ConversationID:    conversationID,
		CreatedAt:         now,
		ExpiresAt:         now.Add(MessageTTL),
		ToRecipients:      toRecipients,
		CC:                cc,
		BCC:               bcc,
		Status:            MessageStatusPendingApproval,
		ApprovalExpiresAt: &approvalExpiresAt,
		BodyText:          bodyText,
		BodyHTML:          bodyHTML,
		AttachmentsJSON:   json.RawMessage(attachmentsJSON),
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (
		    id, agent_id, direction, recipient, subject, email_message_id, message_type,
		    conversation_id, created_at, expires_at,
		    to_recipients, cc, bcc,
		    status, approval_expires_at,
		    body_text, body_html, attachments_json)
		 VALUES ($1, $2, $3, $4, $5, $6, $7,
		         $8, $9, $10,
		         $11, $12, $13,
		         $14, $15,
		         $16, $17, $18)`,
		m.ID, m.AgentID, m.Direction, m.Recipient, m.Subject, m.EmailMessageID, m.Type,
		m.ConversationID, m.CreatedAt, m.ExpiresAt,
		m.ToRecipients, m.CC, m.BCC,
		m.Status, m.ApprovalExpiresAt,
		nullIfEmptyString(m.BodyText), nullIfEmptyString(m.BodyHTML), attachmentsArg,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// nullIfEmptyString returns nil interface when s is empty so the column is
// inserted as SQL NULL rather than ”. Keeps body columns distinguishable
// between "scrubbed" (NULL) and "empty body" once scrubbing is wired up.
func nullIfEmptyString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// --- HITL approval store helpers ---

// ErrNotPendingApproval is returned when an approve or reject operation
// targets a message that is not (or is no longer) in pending_approval status.
// Handlers map this to HTTP 409 Conflict.
var ErrNotPendingApproval = fmt.Errorf("message is not pending approval")

// approvalTxTimeout caps how long a single approve-and-send transaction may
// hold its row-level lock. Chosen to sit just above SMTPRelay's worst-case
// retry envelope: 30s dial + 2min deadline per attempt × 3 attempts, plus
// 21s of backoff sleeps, rounded up. If the underlying send ever ignores
// its own deadlines, this safeguard cancels the tx and releases the lock.
const approvalTxTimeout = 7 * time.Minute

// ErrMessageNotFound is returned when a message is not found for the given
// user (either the ID doesn't exist or the message belongs to another user's
// agent). Handlers map this to HTTP 404.
var ErrMessageNotFound = fmt.Errorf("message not found")

// PendingApprovalEdit holds optional overrides a reviewer can apply when
// approving a pending message. Pointer-typed strings distinguish "not
// provided" (nil) from "explicitly empty" (pointer to ""). Slice fields
// distinguish "unset" (nil) from "empty list" (non-nil zero-length slice).
type PendingApprovalEdit struct {
	Subject         *string
	BodyText        *string
	BodyHTML        *string
	To              []string
	CC              []string
	BCC             []string
	AttachmentsJSON []byte
	// AttachmentsSet must be true when the caller intends to override
	// AttachmentsJSON, since nil and empty [] are both valid overrides
	// (empty [] clears attachments; nil preserves).
	AttachmentsSet bool
}

// Apply mutates msg to reflect any fields the reviewer changed. Returns true
// if any field was actually different from what msg already held (signals
// the edited flag should be set).
func (e PendingApprovalEdit) Apply(msg *Message) bool {
	edited := false
	if e.Subject != nil && *e.Subject != msg.Subject {
		msg.Subject = *e.Subject
		edited = true
	}
	if e.BodyText != nil && *e.BodyText != msg.BodyText {
		msg.BodyText = *e.BodyText
		edited = true
	}
	if e.BodyHTML != nil && *e.BodyHTML != msg.BodyHTML {
		msg.BodyHTML = *e.BodyHTML
		edited = true
	}
	if e.To != nil && !stringSlicesEqual(e.To, msg.ToRecipients) {
		msg.ToRecipients = e.To
		edited = true
	}
	if e.CC != nil && !stringSlicesEqual(e.CC, msg.CC) {
		msg.CC = e.CC
		edited = true
	}
	if e.BCC != nil && !stringSlicesEqual(e.BCC, msg.BCC) {
		msg.BCC = e.BCC
		edited = true
	}
	if e.AttachmentsSet {
		msg.AttachmentsJSON = json.RawMessage(e.AttachmentsJSON)
		edited = true
	}
	return edited
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GetOutboundMessageForUser returns a full message row (including body, HITL
// fields, and attachments) if it exists and is owned by userID (via the
// agent the row belongs to). Inbound messages and cross-user access both
// return ErrMessageNotFound — the caller should not be able to distinguish
// "does not exist" from "belongs to someone else".
func (s *Store) GetOutboundMessageForUser(ctx context.Context, messageID, userID string) (*Message, error) {
	m := &Message{}
	var (
		bodyText, bodyHTML *string
		attachments        []byte
		method, msgType    *string
		approvalExpires    *time.Time
		reviewedAt         *time.Time
		rejectionReason    *string
		reviewedByID       *string
		reviewedByName     *string
	)
	err := s.pool.QueryRow(ctx,
		`SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient, m.subject,
		        m.email_message_id, COALESCE(m.provider_message_id, ''),
		        m.method, m.message_type,
		        m.conversation_id, m.created_at, m.expires_at,
		        m.to_recipients, m.cc, m.bcc,
		        m.status, m.approval_expires_at, m.reviewed_at,
		        m.rejection_reason, m.edited,
		        m.body_text, m.body_html, m.attachments_json,
		        m.reviewed_by_user_id, r.name,
		        COALESCE(m.delivery_status, ''), COALESCE(m.delivery_detail, ''), COALESCE(m.sent_as, '')
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 LEFT JOIN users r ON r.id = m.reviewed_by_user_id
		 WHERE m.id = $1 AND a.user_id = $2 AND m.direction = 'outbound'`,
		messageID, userID,
	).Scan(
		&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.Subject,
		&m.EmailMessageID, &m.ProviderMessageID,
		&method, &msgType,
		&m.ConversationID, &m.CreatedAt, &m.ExpiresAt,
		&m.ToRecipients, &m.CC, &m.BCC,
		&m.Status, &approvalExpires, &reviewedAt,
		&rejectionReason, &m.Edited,
		&bodyText, &bodyHTML, &attachments,
		&reviewedByID, &reviewedByName,
		&m.DeliveryStatus, &m.DeliveryDetail, &m.SentAs,
	)
	if err != nil {
		return nil, ErrMessageNotFound
	}
	if method != nil {
		m.Method = *method
	}
	if msgType != nil {
		m.Type = *msgType
	}
	if approvalExpires != nil {
		m.ApprovalExpiresAt = approvalExpires
	}
	if reviewedAt != nil {
		m.ReviewedAt = reviewedAt
	}
	if rejectionReason != nil {
		m.RejectionReason = *rejectionReason
	}
	if bodyText != nil {
		m.BodyText = *bodyText
	}
	if bodyHTML != nil {
		m.BodyHTML = *bodyHTML
	}
	if len(attachments) > 0 {
		m.AttachmentsJSON = json.RawMessage(attachments)
	}
	m.ReviewedByUserID = reviewedByID
	m.ReviewedByName = reviewedByName
	return m, nil
}

// ListPendingOutboundForUser returns pending-approval messages across all of
// the user's agents, sorted by approval_expires_at ASC (expiring-soonest
// first). Body columns are not returned from this path — callers should use
// GetOutboundMessageForUser for detail.
func (s *Store) ListPendingOutboundForUser(ctx context.Context, userID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.agent_id, m.subject, m.email_message_id,
		        COALESCE(m.message_type, ''),
		        m.conversation_id, m.created_at,
		        m.to_recipients, m.cc, m.bcc,
		        m.status, m.approval_expires_at,
		        COALESCE(m.delivery_status, ''), COALESCE(m.delivery_detail, ''), COALESCE(m.sent_as, '')
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 WHERE a.user_id = $1 AND m.status = 'pending_approval'
		 ORDER BY m.approval_expires_at ASC
		 LIMIT $2`, userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var approvalExpires *time.Time
		if err := rows.Scan(
			&m.ID, &m.AgentID, &m.Subject, &m.EmailMessageID,
			&m.Type,
			&m.ConversationID, &m.CreatedAt,
			&m.ToRecipients, &m.CC, &m.BCC,
			&m.Status, &approvalExpires,
			&m.DeliveryStatus, &m.DeliveryDetail, &m.SentAs,
		); err != nil {
			return nil, err
		}
		m.Direction = "outbound"
		m.ApprovalExpiresAt = approvalExpires
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// SendResult carries the outcome of a sender.Send invocation back to the
// store for final persistence. Handlers wrap their sender.Send call in a
// closure that returns this type.
type SendResult struct {
	ProviderMessageID string
	Method            string
	To                []string
	CC                []string
	BCC               []string
}

// ApproveAndSend finalizes a pending_approval message by running it through
// a caller-supplied send function inside a transaction that holds a row lock
// on the pending row. If send returns an error the transaction rolls back
// and the message remains pending. On success the row is updated to
// 'sent' with the provider-assigned Message-ID and the body/attachments
// columns are scrubbed.
//
// edits, if any fields are populated, are applied to the in-memory message
// before send is called and the 'edited' column is set to true when any
// field differs from what was stored. Approval-via-magic-link callers
// pass the zero edits value.
//
// Ownership is enforced by the agent -> user join. Messages owned by
// another user return ErrMessageNotFound. Messages whose status is not
// 'pending_approval' return ErrNotPendingApproval. If another worker is
// already mid-send for this message (rare; only possible after the
// approval row lock was released without status changing — e.g. a
// pool drop mid-send), this returns ErrSendInProgress.
//
// Concurrency / failure mode notes:
//
//   - The row-level FOR NO KEY UPDATE lock is held on the messages row
//     for the duration of the send callback. In practice that is
//     bounded by outbound.SMTPRelay's per-attempt deadline (2min) plus
//     its internal retry backoff (1s/5s/15s) — worst case ~6.5min of
//     lock on this single row. Other rows are unaffected; deadlock is
//     not possible because only one row is ever locked per call.
//
//     Why NO KEY UPDATE rather than the stricter FOR UPDATE: the
//     send_attempts INSERT below runs on a SEPARATE pool connection
//     and needs a KEY SHARE lock on this messages row for FK
//     enforcement. FOR UPDATE blocks KEY SHARE; FOR NO KEY UPDATE
//     allows it. The downgrade is safe because nothing in this
//     codebase mutates messages.id (the only key column) after
//     creation — all UPDATEs touch non-key columns, which NO KEY
//     UPDATE serializes against itself exactly like FOR UPDATE.
//
//   - The old crash window where send() succeeded at SES but the
//     subsequent UPDATE/Commit failed (DB blip, pool exhaustion) is now
//     closed by the send_attempts table. Around send() we run two small
//     auxiliary transactions that outlive the surrounding approval
//     transaction: ClaimSendAttempt before send(), MarkSendSucceeded
//     (or MarkSendFailed) after. If the approval tx rolls back AFTER
//     send() succeeded, the next retry of ApproveAndSend reads
//     send_attempts.status='sent', reuses the recorded SendResult, and
//     skips the upstream send entirely.
func (s *Store) ApproveAndSend(
	ctx context.Context,
	messageID, userID string,
	edits PendingApprovalEdit,
	send func(msg *Message) (SendResult, error),
) (*Message, error) {
	// Bound the transaction's lifetime at just above SMTPRelay's worst-case
	// retry envelope (~6.5min). This is a defensive cap: if the relay ever
	// ignores its own deadlines or a send stalls indefinitely, the tx gets
	// cancelled and the row lock releases rather than held forever.
	txCtx, cancel := context.WithTimeout(ctx, approvalTxTimeout)
	defer cancel()

	tx, err := s.pool.Begin(txCtx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(txCtx)
		}
	}()

	// Lock the pending row and verify ownership in one query.
	var (
		m                  Message
		ownerUserID        string
		bodyText, bodyHTML *string
		attachments        []byte
		method, msgType    *string
		approvalExpires    *time.Time
	)
	err = tx.QueryRow(txCtx,
		`SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient, m.subject,
		        m.email_message_id,
		        m.method, m.message_type,
		        m.conversation_id, m.created_at, m.expires_at,
		        m.to_recipients, m.cc, m.bcc,
		        m.status, m.approval_expires_at, m.edited,
		        m.body_text, m.body_html, m.attachments_json,
		        a.user_id
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 WHERE m.id = $1 AND m.direction = 'outbound'
		 FOR NO KEY UPDATE OF m`,
		messageID,
	).Scan(
		&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.Subject,
		&m.EmailMessageID,
		&method, &msgType,
		&m.ConversationID, &m.CreatedAt, &m.ExpiresAt,
		&m.ToRecipients, &m.CC, &m.BCC,
		&m.Status, &approvalExpires, &m.Edited,
		&bodyText, &bodyHTML, &attachments,
		&ownerUserID,
	)
	if err != nil {
		return nil, ErrMessageNotFound
	}
	if ownerUserID != userID {
		return nil, ErrMessageNotFound
	}
	if m.Status != MessageStatusPendingApproval {
		return nil, ErrNotPendingApproval
	}
	if method != nil {
		m.Method = *method
	}
	if msgType != nil {
		m.Type = *msgType
	}
	if approvalExpires != nil {
		m.ApprovalExpiresAt = approvalExpires
	}
	if bodyText != nil {
		m.BodyText = *bodyText
	}
	if bodyHTML != nil {
		m.BodyHTML = *bodyHTML
	}
	if len(attachments) > 0 {
		m.AttachmentsJSON = json.RawMessage(attachments)
	}

	editedByReviewer := edits.Apply(&m)

	// Exactly-once gate around the upstream send. Runs OUTSIDE the
	// approval transaction so its outcome survives an approval-tx
	// rollback — that's the whole point of send_attempts.
	claim, err := s.ClaimSendAttempt(ctx, messageID)
	if err != nil {
		return nil, err
	}

	var result SendResult
	switch claim.Outcome {
	case SendAttemptAcquired:
		result, err = send(&m)
		if err != nil {
			// Mark failed in a separate tx so the next retry can
			// take over. Best-effort: log if the mark itself fails,
			// don't shadow the original send error.
			if markErr := s.MarkSendFailed(ctx, messageID, err.Error()); markErr != nil {
				log.Printf("[approve] MarkSendFailed for %s: %v", messageID, markErr)
			}
			return nil, err
		}
		if markErr := s.MarkSendSucceededWithRetry(messageID, result); markErr != nil {
			// The upstream send DID succeed but we exhausted the
			// retry budget recording that fact. Log loudly so ops
			// can reconcile against the SES Configuration Set
			// events log; the approval tx below still finalizes
			// the message row from this attempt so the customer
			// sees a successful approve. Residual risk: the 10-min
			// stale takeover could re-invoke send() if the row
			// stays `attempting` until the worker fires.
			log.Printf("[approve] MarkSendSucceeded exhausted retries for %s: %v (manual reconciliation may be needed)", messageID, markErr)
		}
	case SendAttemptAlreadySent:
		// A prior approval-tx attempt succeeded at SES but its
		// surrounding tx rolled back. Reuse the recorded result and
		// skip the upstream send.
		result = claim.Sent
	case SendAttemptInFlight:
		return nil, ErrSendInProgress
	}

	_, err = tx.Exec(txCtx,
		`UPDATE messages
		    SET status            = $2,
		        provider_message_id = $3,
		        method            = $4,
		        to_recipients     = $5,
		        cc                = $6,
		        bcc               = $7,
		        recipient         = $8,
		        subject           = $9,
		        edited            = $10,
		        reviewed_at       = now(),
		        reviewed_by_user_id = $11,
		        body_text         = NULL,
		        body_html         = NULL,
		        attachments_json  = NULL
		  WHERE id = $1`,
		messageID,
		MessageStatusSent,
		result.ProviderMessageID,
		result.Method,
		result.To,
		result.CC,
		result.BCC,
		firstOr(result.To, ""),
		m.Subject,
		editedByReviewer || m.Edited,
		userID,
	)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(txCtx); err != nil {
		return nil, err
	}
	committed = true

	// Reflect post-commit state on the returned message.
	m.Status = MessageStatusSent
	m.ProviderMessageID = result.ProviderMessageID
	m.Method = result.Method
	m.ToRecipients = result.To
	m.CC = result.CC
	m.BCC = result.BCC
	if len(result.To) > 0 {
		m.Recipient = result.To[0]
	}
	m.Edited = editedByReviewer || m.Edited
	now := time.Now()
	m.ReviewedAt = &now
	reviewerID := userID
	m.ReviewedByUserID = &reviewerID
	m.BodyText = ""
	m.BodyHTML = ""
	m.AttachmentsJSON = nil
	return &m, nil
}

func firstOr(s []string, fallback string) string {
	if len(s) > 0 {
		return s[0]
	}
	return fallback
}

// ResolveOutboundOwner looks up the user_id and agent_id for an outbound
// message without requiring the caller to know the user_id up-front. It
// exists for token-authenticated paths (magic-link approve/reject) where
// the HMAC token itself is the authorization and the handler just needs
// enough context to dispatch into the existing user-scoped store methods.
//
// Returns ErrMessageNotFound if the message doesn't exist or isn't
// outbound. The returned user_id is guaranteed to own the message's
// agent (via the agent_identities.user_id join).
func (s *Store) ResolveOutboundOwner(ctx context.Context, messageID string) (userID, agentID string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT a.user_id, m.agent_id
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 WHERE m.id = $1 AND m.direction = 'outbound'`,
		messageID,
	).Scan(&userID, &agentID)
	if err != nil {
		return "", "", ErrMessageNotFound
	}
	return userID, agentID, nil
}

// ExpirationCandidate is the minimal row the expiration worker needs to
// decide how to finalize an expired pending message.
type ExpirationCandidate struct {
	MessageID        string
	AgentID          string
	ExpirationAction string // 'approve' or 'reject'
}

// ListExpiredPending returns pending_approval messages whose
// approval_expires_at is in the past, joined with their agent's
// hitl_expiration_action. Ordered by approval_expires_at ASC so
// earliest-expired are handled first.
func (s *Store) ListExpiredPending(ctx context.Context, limit int) ([]ExpirationCandidate, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.agent_id, a.hitl_expiration_action
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 WHERE m.status = 'pending_approval'
		   AND m.approval_expires_at < now()
		 ORDER BY m.approval_expires_at ASC
		 LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpirationCandidate
	for rows.Next() {
		var c ExpirationCandidate
		if err := rows.Scan(&c.MessageID, &c.AgentID, &c.ExpirationAction); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ExpireApproveAndSend is the worker-side counterpart to ApproveAndSend:
// no user ownership check (the caller is the expiration worker, which is
// system-scoped), SELECT ... FOR NO KEY UPDATE SKIP LOCKED so concurrent
// workers don't race for the same row, and the terminal status is
// 'expired_approved' instead of 'sent'. On send failure the transaction
// rolls back; the worker should then call ExpireReject to move the row
// to a final state so the row doesn't get picked up on every sweep.
//
// Exactly-once guarantee: like ApproveAndSend, this method runs the
// send() callback under a send_attempts gate so a crash between SES
// acceptance and the surrounding tx commit does NOT cause the next
// worker poll to re-send. ClaimSendAttempt / MarkSendSucceeded /
// MarkSendFailed run in separate small transactions that outlive the
// approval tx; on retry, an AlreadySent verdict reuses the cached
// SendResult and skips the upstream send entirely. Without this, the
// polling-loop nature of the worker would guarantee a re-send on any
// commit failure — strictly worse than the human-approval path,
// where a re-send needs an explicit click.
//
// SKIP LOCKED means multiple app instances can run the worker without
// contending on the same row. The row-level FOR NO KEY UPDATE lock on
// messages is held for the duration of the send callback (bounded by
// SMTPRelay timeouts); FOR NO KEY UPDATE rather than FOR UPDATE so
// the send_attempts INSERT in a separate connection can acquire its
// KEY SHARE lock for FK enforcement — see ApproveAndSend's docstring
// for the full rationale.
//
// If a concurrent worker is mid-send for the same row (the
// send_attempts row is 'attempting' and not yet stale), returns
// ErrSendInProgress. The worker loop should treat this like
// ErrNotPendingApproval — skip silently and let the next poll handle
// it.
func (s *Store) ExpireApproveAndSend(
	ctx context.Context,
	messageID string,
	send func(msg *Message) (SendResult, error),
) (*Message, error) {
	txCtx, cancel := context.WithTimeout(ctx, approvalTxTimeout)
	defer cancel()

	tx, err := s.pool.Begin(txCtx)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(txCtx)
		}
	}()

	var (
		m                  Message
		bodyText, bodyHTML *string
		attachments        []byte
		method, msgType    *string
		approvalExpires    *time.Time
	)
	err = tx.QueryRow(txCtx,
		`SELECT id, agent_id, direction, sender, recipient, subject,
		        email_message_id,
		        method, message_type,
		        conversation_id, created_at, expires_at,
		        to_recipients, cc, bcc,
		        status, approval_expires_at, edited,
		        body_text, body_html, attachments_json
		 FROM messages
		 WHERE id = $1
		   AND direction = 'outbound'
		   AND status = 'pending_approval'
		   AND approval_expires_at < now()
		 FOR NO KEY UPDATE SKIP LOCKED`,
		messageID,
	).Scan(
		&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.Subject,
		&m.EmailMessageID,
		&method, &msgType,
		&m.ConversationID, &m.CreatedAt, &m.ExpiresAt,
		&m.ToRecipients, &m.CC, &m.BCC,
		&m.Status, &approvalExpires, &m.Edited,
		&bodyText, &bodyHTML, &attachments,
	)
	if err != nil {
		// Row is either gone, no longer pending, not yet expired, or is
		// currently locked by another worker. Any of those means "someone
		// else will handle it, or nothing to do" — don't bubble as an error.
		return nil, ErrNotPendingApproval
	}
	if method != nil {
		m.Method = *method
	}
	if msgType != nil {
		m.Type = *msgType
	}
	if approvalExpires != nil {
		m.ApprovalExpiresAt = approvalExpires
	}
	if bodyText != nil {
		m.BodyText = *bodyText
	}
	if bodyHTML != nil {
		m.BodyHTML = *bodyHTML
	}
	if len(attachments) > 0 {
		m.AttachmentsJSON = json.RawMessage(attachments)
	}

	// Exactly-once gate, identical to ApproveAndSend's bracket. Runs
	// OUTSIDE this approval tx so the SES outcome survives an approval
	// tx rollback. See ApproveAndSend's docstring for the full
	// rationale.
	claim, err := s.ClaimSendAttempt(ctx, messageID)
	if err != nil {
		return nil, err
	}

	var result SendResult
	switch claim.Outcome {
	case SendAttemptAcquired:
		result, err = send(&m)
		if err != nil {
			if markErr := s.MarkSendFailed(ctx, messageID, err.Error()); markErr != nil {
				log.Printf("[expire] MarkSendFailed for %s: %v", messageID, markErr)
			}
			return nil, err
		}
		if markErr := s.MarkSendSucceededWithRetry(messageID, result); markErr != nil {
			log.Printf("[expire] MarkSendSucceeded exhausted retries for %s: %v (manual reconciliation may be needed)", messageID, markErr)
		}
	case SendAttemptAlreadySent:
		// A prior auto-approve attempt succeeded at SES but its
		// approval tx rolled back. Reuse the recorded result and
		// skip the upstream send.
		result = claim.Sent
	case SendAttemptInFlight:
		return nil, ErrSendInProgress
	}

	_, err = tx.Exec(txCtx,
		`UPDATE messages
		    SET status            = $2,
		        provider_message_id = $3,
		        method            = $4,
		        to_recipients     = $5,
		        cc                = $6,
		        bcc               = $7,
		        recipient         = $8,
		        reviewed_at       = now(),
		        body_text         = NULL,
		        body_html         = NULL,
		        attachments_json  = NULL
		  WHERE id = $1`,
		messageID,
		MessageStatusExpiredApproved,
		result.ProviderMessageID,
		result.Method,
		result.To,
		result.CC,
		result.BCC,
		firstOr(result.To, ""),
	)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(txCtx); err != nil {
		return nil, err
	}
	committed = true

	m.Status = MessageStatusExpiredApproved
	m.ProviderMessageID = result.ProviderMessageID
	m.Method = result.Method
	m.ToRecipients = result.To
	m.CC = result.CC
	m.BCC = result.BCC
	if len(result.To) > 0 {
		m.Recipient = result.To[0]
	}
	now := time.Now()
	m.ReviewedAt = &now
	m.BodyText = ""
	m.BodyHTML = ""
	m.AttachmentsJSON = nil
	return &m, nil
}

// ExpireReject transitions a pending_approval message to expired_rejected
// and scrubs body columns. No user ownership check — this is the worker
// path. If the row is no longer pending (racing worker, already handled)
// returns ErrNotPendingApproval; caller can treat as a no-op.
func (s *Store) ExpireReject(ctx context.Context, messageID, reason string) (*Message, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE messages
		    SET status = $2,
		        rejection_reason = $3,
		        reviewed_at = now(),
		        body_text = NULL,
		        body_html = NULL,
		        attachments_json = NULL
		  WHERE id = $1 AND status = 'pending_approval'`,
		messageID, MessageStatusExpiredRejected, reason,
	)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotPendingApproval
	}
	// Read back with ownership skipped — the worker doesn't have a userID.
	m := &Message{}
	var (
		method, msgType   *string
		approvalExpiresAt *time.Time
		reviewedAt        *time.Time
		rejectionReason   *string
	)
	err = s.pool.QueryRow(ctx,
		`SELECT id, agent_id, direction, subject, email_message_id,
		        method, message_type,
		        conversation_id, created_at, expires_at,
		        to_recipients, cc, bcc,
		        status, approval_expires_at, reviewed_at,
		        rejection_reason, edited
		 FROM messages WHERE id = $1`, messageID,
	).Scan(
		&m.ID, &m.AgentID, &m.Direction, &m.Subject, &m.EmailMessageID,
		&method, &msgType,
		&m.ConversationID, &m.CreatedAt, &m.ExpiresAt,
		&m.ToRecipients, &m.CC, &m.BCC,
		&m.Status, &approvalExpiresAt, &reviewedAt,
		&rejectionReason, &m.Edited,
	)
	if err != nil {
		return nil, err
	}
	if method != nil {
		m.Method = *method
	}
	if msgType != nil {
		m.Type = *msgType
	}
	m.ApprovalExpiresAt = approvalExpiresAt
	m.ReviewedAt = reviewedAt
	if rejectionReason != nil {
		m.RejectionReason = *rejectionReason
	}
	return m, nil
}

// RejectPending transitions a pending_approval message to rejected,
// records the reviewer's reason (empty string allowed), and scrubs
// body_text / body_html / attachments_json. Ownership checked; missing
// rows return ErrMessageNotFound. Non-pending rows return ErrNotPendingApproval.
func (s *Store) RejectPending(ctx context.Context, messageID, userID, reason string) (*Message, error) {
	// Single atomic UPDATE with status guard. We distinguish "not found" from
	// "not pending" with a follow-up existence check only when rows-affected
	// is 0.
	tag, err := s.pool.Exec(ctx,
		`UPDATE messages
		    SET status = $3,
		        rejection_reason = $4,
		        reviewed_at = now(),
		        reviewed_by_user_id = $2,
		        body_text = NULL,
		        body_html = NULL,
		        attachments_json = NULL
		  WHERE id = $1
		    AND status = 'pending_approval'
		    AND agent_id IN (SELECT id FROM agent_identities WHERE user_id = $2)`,
		messageID, userID, MessageStatusRejected, reason,
	)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// Figure out why: missing, not owned, or not pending.
		var status string
		err := s.pool.QueryRow(ctx,
			`SELECT m.status
			 FROM messages m
			 JOIN agent_identities a ON a.id = m.agent_id
			 WHERE m.id = $1 AND a.user_id = $2`,
			messageID, userID,
		).Scan(&status)
		if err != nil {
			return nil, ErrMessageNotFound
		}
		return nil, ErrNotPendingApproval
	}
	return s.GetOutboundMessageForUser(ctx, messageID, userID)
}

func (s *Store) ListActivityByAgent(ctx context.Context, agentID string, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient, m.subject, m.email_message_id, COALESCE(m.method, ''), COALESCE(m.message_type, ''), COALESCE(m.inbox_status, ''), m.created_at, m.expires_at,
		        COALESCE(wd.status, ''), COALESCE(wd.last_error, ''), COALESCE(wd.attempts, 0),
		        m.to_recipients, m.cc, m.bcc,
		        COALESCE(m.conversation_id, ''), COALESCE(octet_length(m.raw_message), 0),
		        m.labels,
		        COALESCE(m.delivery_status, ''), COALESCE(m.delivery_detail, ''), COALESCE(m.sent_as, ''), m.auth_verdict,
		        COALESCE(m.flagged, false), COALESCE(m.flag_reason, '')
		 FROM messages m
		 LEFT JOIN webhook_deliveries wd ON wd.message_id = m.id
		 WHERE m.agent_id = $1 AND m.expires_at > now()
		 ORDER BY m.created_at DESC
		 LIMIT $2`, agentID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var inboxStatus, outboundDeliveryStatus string
		var authVerdict []byte
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.Subject, &m.EmailMessageID, &m.Method, &m.Type, &inboxStatus, &m.CreatedAt, &m.ExpiresAt, &m.WebhookStatus, &m.WebhookError, &m.WebhookAttempts, &m.ToRecipients, &m.CC, &m.BCC, &m.ConversationID, &m.SizeBytes, &m.Labels, &outboundDeliveryStatus, &m.DeliveryDetail, &m.SentAs, &authVerdict, &m.Flagged, &m.FlagReason); err != nil {
			return nil, err
		}
		if err := unmarshalAuthVerdict(authVerdict, &m); err != nil {
			return nil, err
		}
		// DeliveryStatus is overloaded by direction (see Message.DeliveryStatus):
		// inbound carries inbox_status, outbound carries the delivery rollup.
		m.InboxStatus = inboxStatus
		if m.Direction == "outbound" {
			m.DeliveryStatus = outboundDeliveryStatus
		} else {
			m.DeliveryStatus = inboxStatus
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// escapeLikePattern escapes the three SQL LIKE/ILIKE metacharacters
// (%, _, \) by prefixing them with backslash. Callers pair the
// returned pattern with `ESCAPE '\'` in the SQL fragment so the
// driver treats backslash as the escape char.
//
// This is NOT for SQL injection protection — pgx parameter binding
// already handles that — it's for "user-typed substring search,
// not glob". Without this, `?from=foo_bar` would match `fooXbar`,
// and `?from=%@acme.com` would match every row in the table.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLikePattern(s string) string {
	return likeEscaper.Replace(s)
}

// MessageListFilter bundles the params for GetMessagesByAgent. Zero
// values on the optional substring / time / ID filters mean "no
// constraint" — callers omit what they don't want to filter on.
type MessageListFilter struct {
	AgentID    string
	Status     string // "unread" | "read" | "all"
	Direction  string // "inbound" | "outbound" | "all"
	Descending bool
	Limit      int
	AfterTime  time.Time
	AfterID    string
	// Optional search filters. Empty / zero means "no constraint".
	// From / SubjectContains are case-insensitive substring matches
	// (Postgres ILIKE) and bound to 200 chars at the handler layer.
	From            string
	SubjectContains string
	ConversationID  string    // exact match
	Since           time.Time // created_at >= Since
	Until           time.Time // created_at <  Until
	// Labels filters rows where ALL given labels are present on the
	// message (AND-match via Postgres @> array containment). Empty slice
	// means "no label constraint" — matches both labelled and unlabelled
	// rows. Handler-layer validates each entry against the same charset
	// rule used on writes so callers can't smuggle SQL through here.
	Labels []string
}

// GetMessagesByAgent returns messages for an agent, filtered by status,
// direction, and the optional search filters on the MessageListFilter
// struct.
//
//   - direction: "inbound" (default for SDK polling), "outbound", or "all"
//     (used by the dashboard inbox).
//   - status: "unread" | "read" | "all" — only applies when direction
//     selects inbound rows; ignored on pure outbound queries.
//   - descending: cursor walks newest→oldest when true; oldest→newest
//     when false (FIFO polling).
//   - From / SubjectContains: case-insensitive substring (ILIKE).
//   - ConversationID: exact match.
//   - Since / Until: time-range bracket on created_at.
//
// The SELECT includes columns both consumers need: the inbox needs
// `status` (outbound HITL lifecycle), `webhook_status`/`last_error`
// (outbound delivery), and `octet_length(raw_message)` (size column);
// the polling SDK ignores these fields and reads only the existing
// inbound-relevant ones from the Message struct.
func (s *Store) GetMessagesByAgent(ctx context.Context, f MessageListFilter) ([]Message, error) {
	var query string
	var args []interface{}

	baseSelect := `SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient, m.to_recipients, m.cc, m.reply_to, m.subject, m.email_message_id, m.conversation_id, COALESCE(m.inbox_status, ''), COALESCE(m.status, ''), COALESCE(wd.status, ''), COALESCE(wd.last_error, ''), COALESCE(octet_length(m.raw_message), 0), m.created_at, m.labels, COALESCE(m.delivery_status, ''), COALESCE(m.delivery_detail, ''), COALESCE(m.sent_as, ''), m.auth_verdict, COALESCE(m.flagged, false), COALESCE(m.flag_reason, '')
		 FROM messages m
		 LEFT JOIN webhook_deliveries wd ON wd.message_id = m.id
		 WHERE m.agent_id = $1 AND m.expires_at > now()`

	switch f.Direction {
	case "outbound":
		query = baseSelect + ` AND m.direction = 'outbound'`
	case "all":
		query = baseSelect
	default: // "inbound" — default keeps SDK polling contract
		query = baseSelect + ` AND m.direction = 'inbound'`
	}

	// Inbox status filter only applies when inbound rows are in the
	// result set. Silently ignored for pure outbound queries — the
	// handler validates 400 on bad combinations before reaching here.
	if f.Direction != "outbound" {
		switch f.Status {
		case "all":
			// no extra clause
		case "read":
			query += ` AND m.inbox_status = 'read'`
		default: // "unread"
			if f.Direction == "inbound" {
				query += ` AND m.inbox_status = 'unread'`
			}
			// For direction='all', "unread" would silently drop every
			// outbound row (they have no inbox_status). That's a footgun
			// the dashboard never invokes — it always passes status="all"
			// when direction="all" — so we don't filter here.
		}
	}

	args = append(args, f.AgentID)

	// Optional search filters — each appends one arg and one WHERE
	// clause. Ordering matches the docstring so a code reader can
	// see at a glance which knobs map to which SQL fragment.
	//
	// ILIKE filters use ESCAPE '\' so the caller's literal `%`, `_`,
	// and `\` characters match themselves instead of acting as SQL
	// pattern wildcards. Without this, `?from=foo_bar` would also
	// match `fooXbar`, and `?from=%@acme.com` would match every row.
	// pgx parameter binding still protects against injection — this
	// is purely a "users expect substring search, not glob" fix.
	if f.From != "" {
		query += fmt.Sprintf(` AND m.sender ILIKE $%d ESCAPE '\'`, len(args)+1)
		args = append(args, "%"+escapeLikePattern(f.From)+"%")
	}
	if f.SubjectContains != "" {
		query += fmt.Sprintf(` AND m.subject ILIKE $%d ESCAPE '\'`, len(args)+1)
		args = append(args, "%"+escapeLikePattern(f.SubjectContains)+"%")
	}
	if f.ConversationID != "" {
		query += fmt.Sprintf(` AND m.conversation_id = $%d`, len(args)+1)
		args = append(args, f.ConversationID)
	}
	if !f.Since.IsZero() {
		query += fmt.Sprintf(` AND m.created_at >= $%d`, len(args)+1)
		args = append(args, f.Since)
	}
	if !f.Until.IsZero() {
		query += fmt.Sprintf(` AND m.created_at < $%d`, len(args)+1)
		args = append(args, f.Until)
	}
	if len(f.Labels) > 0 {
		// AND-match via @> array containment. The GIN index on labels
		// makes this O(log n) for the typical case (≤ 5 filter labels,
		// ≤ 100 labels per row). Empty caller-supplied labels are
		// stripped at the handler layer so we never produce
		// "labels @> ARRAY['']" which would match nothing.
		query += fmt.Sprintf(` AND m.labels @> $%d`, len(args)+1)
		args = append(args, f.Labels)
	}

	cursorCmp := ">"
	sortDir := "ASC"
	if f.Descending {
		cursorCmp = "<"
		sortDir = "DESC"
	}

	if f.AfterID != "" {
		query += fmt.Sprintf(` AND (m.created_at, m.id) %s ($%d, $%d)`, cursorCmp, len(args)+1, len(args)+2)
		args = append(args, f.AfterTime, f.AfterID)
	}

	query += fmt.Sprintf(` ORDER BY m.created_at %s, m.id %s LIMIT $%d`, sortDir, sortDir, len(args)+1)
	args = append(args, f.Limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var outboundDeliveryStatus string
		var authVerdict []byte
		if err := rows.Scan(
			&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo,
			&m.Subject, &m.EmailMessageID, &m.ConversationID,
			&m.InboxStatus, &m.Status, &m.WebhookStatus, &m.WebhookError, &m.SizeBytes,
			&m.CreatedAt, &m.Labels,
			&outboundDeliveryStatus, &m.DeliveryDetail, &m.SentAs, &authVerdict, &m.Flagged, &m.FlagReason,
		); err != nil {
			return nil, err
		}
		if err := unmarshalAuthVerdict(authVerdict, &m); err != nil {
			return nil, err
		}
		// DeliveryStatus is overloaded by direction: inbound rows carry the
		// inbox read/unread status under the legacy JSON key (the polling SDK
		// reads it there); outbound rows carry the messages.delivery_status
		// rollup. A row is one direction, so the sources never collide.
		if m.Direction == "outbound" {
			m.DeliveryStatus = outboundDeliveryStatus
		} else {
			m.DeliveryStatus = m.InboxStatus
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// GetMessageWithContent returns a full message including raw_message and auth_headers.
// Marks the message as 'read' if it was 'unread'.
func (s *Store) GetMessageWithContent(ctx context.Context, messageID, agentID string) (*Message, error) {
	m := &Message{}
	var authHeadersJSON []byte
	var authVerdict []byte
	var outboundDeliveryStatus string
	err := s.pool.QueryRow(ctx,
		`UPDATE messages SET inbox_status = CASE WHEN inbox_status = 'unread' THEN 'read' ELSE inbox_status END
		 WHERE id = $1 AND agent_id = $2 AND expires_at > now()
		 RETURNING id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, conversation_id, COALESCE(inbox_status, ''), raw_message, auth_headers, auth_verdict, COALESCE(flagged, false), COALESCE(flag_reason, ''), created_at, expires_at, labels, COALESCE(delivery_status, ''), COALESCE(delivery_detail, ''), COALESCE(sent_as, ''), COALESCE(body_text, ''), COALESCE(body_html, '')`,
		messageID, agentID,
	).Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo, &m.Subject, &m.EmailMessageID, &m.ConversationID, &m.InboxStatus, &m.RawMessage, &authHeadersJSON, &authVerdict, &m.Flagged, &m.FlagReason, &m.CreatedAt, &m.ExpiresAt, &m.Labels, &outboundDeliveryStatus, &m.DeliveryDetail, &m.SentAs, &m.BodyText, &m.BodyHTML)
	if err != nil {
		return nil, err
	}
	// DeliveryStatus is overloaded by direction (see Message.DeliveryStatus):
	// inbound carries inbox_status, outbound carries the delivery rollup.
	if m.Direction == "outbound" {
		m.DeliveryStatus = outboundDeliveryStatus
	} else {
		m.DeliveryStatus = m.InboxStatus
	}
	if authHeadersJSON != nil {
		if err := json.Unmarshal(authHeadersJSON, &m.AuthHeaders); err != nil {
			return nil, fmt.Errorf("unmarshal auth headers: %w", err)
		}
	}
	if err := unmarshalAuthVerdict(authVerdict, m); err != nil {
		return nil, err
	}
	return m, nil
}

// ErrLabelLimitExceeded reports that an add operation would push a
// message past MaxLabelsPerMessage. Mapped to HTTP 400 at the handler.
var ErrLabelLimitExceeded = errors.New("label limit exceeded")

// MaxLabelsPerMessage is the post-add cap on the labels[] column. The
// per-operation cap (max items in add_labels / remove_labels) is
// enforced earlier at the handler. The two together bound the array
// at a size where GIN containment + JSON marshalling stay cheap.
const MaxLabelsPerMessage = 100

// ModifyMessageLabels applies a delta — add then remove — to a
// message's labels[] in a single atomic statement. Returns the updated
// labels (deduplicated, sorted) so the caller can echo them back in
// the response without a second round-trip.
//
// Inputs are assumed already normalized (lowercased, charset-validated,
// dedup'd within each list, e2a:* gated). The store layer:
//   - applies adds first, then removes (so a label in both lists ends up removed)
//   - rejects if the post-add total would exceed MaxLabelsPerMessage
//   - returns ErrMessageNotFound if the row is missing / expired / cross-agent
//
// The whole thing runs as one UPDATE so a concurrent PATCH from a
// second client can't observe a partial state.
func (s *Store) ModifyMessageLabels(ctx context.Context, messageID, agentID string, add, remove []string) ([]string, error) {
	// Pre-check the post-add length against the cap. Done as a
	// dedicated SELECT-then-UPDATE so we can return a specific error
	// rather than a generic constraint violation — the handler maps
	// ErrLabelLimitExceeded to 400 with a useful message.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var current []string
	err = tx.QueryRow(ctx,
		`SELECT labels FROM messages WHERE id = $1 AND agent_id = $2 AND expires_at > now() FOR UPDATE`,
		messageID, agentID,
	).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMessageNotFound
		}
		return nil, err
	}

	// Apply the delta in-memory so the cap check is exact. The set
	// semantics here mirror what the SQL UPDATE below does:
	// labels' = (labels ∪ add) \ remove.
	labelSet := map[string]struct{}{}
	for _, l := range current {
		labelSet[l] = struct{}{}
	}
	for _, l := range add {
		labelSet[l] = struct{}{}
	}
	for _, l := range remove {
		delete(labelSet, l)
	}
	if len(labelSet) > MaxLabelsPerMessage {
		return nil, ErrLabelLimitExceeded
	}

	final := make([]string, 0, len(labelSet))
	for l := range labelSet {
		final = append(final, l)
	}
	sort.Strings(final)

	if _, err := tx.Exec(ctx,
		`UPDATE messages SET labels = $1 WHERE id = $2 AND agent_id = $3`,
		final, messageID, agentID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if final == nil {
		final = []string{}
	}
	return final, nil
}

// UpdateMessageDeliveryStatus sets the inbox_status on a message.
func (s *Store) UpdateMessageDeliveryStatus(ctx context.Context, messageID, agentID, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE messages SET inbox_status = $1 WHERE id = $2 AND agent_id = $3`,
		status, messageID, agentID,
	)
	return err
}

func (s *Store) DeleteExpiredMessages(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM messages WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// LookupConversationID finds a conversation_id by matching In-Reply-To / References
// message IDs against stored messages. Checks both email_message_id (inbound) and
// provider_message_id (outbound). Uses prefix matching because SES bare IDs stored
// in provider_message_id (e.g. <010f...>) may lack the @region.amazonses.com suffix
// that appears in the actual email headers sent to recipients.
func (s *Store) LookupConversationID(ctx context.Context, agentID string, messageIDs []string) (string, error) {
	if len(messageIDs) == 0 {
		return "", fmt.Errorf("no message IDs to look up")
	}

	var conversationID string
	err := s.pool.QueryRow(ctx,
		`SELECT conversation_id FROM messages
		 WHERE agent_id = $1
		   AND conversation_id <> ''
		   AND (
		     email_message_id = ANY($2)
		     OR provider_message_id = ANY($2)
		     OR EXISTS (
		       SELECT 1 FROM unnest($2::text[]) AS lookup(id)
		       WHERE lookup.id LIKE REPLACE(provider_message_id, '>', '%')
		         AND provider_message_id <> ''
		     )
		   )
		 ORDER BY created_at DESC LIMIT 1`,
		agentID, messageIDs,
	).Scan(&conversationID)
	if err != nil {
		return "", err
	}
	return conversationID, nil
}

// --- Conversations (thin read layer over messages.conversation_id) ---
//
// A conversation is the set of messages an agent sees that share a
// non-empty conversation_id. The shape is computed at read time —
// there's no `conversations` table, no persistence cost on top of
// the existing messages row. The trade-off is that listing requires
// an aggregate query; the partial index from migration 022 keeps it
// cheap on large agents.
//
// All Conversation* methods scope by agent_id. Cross-agent
// conversations (a user owns two agents and uses the same
// conversation_id) are intentionally split — a conversation is an
// agent-level concept, not a user-level one. If we ever want a
// user-wide "all conversations" view it gets a separate endpoint
// (mirrors the agents+messages vs. user+pending split that already
// exists).

// ConversationSummary is one row in the list endpoint. Aggregated
// counts + the "latest message" preview fields are enough to render
// an inbox-style conversation list without a per-row drill-down.
//
// HasUnread is true iff at least one INBOUND member is in
// inbox_status='unread'. Outbound pending_approval doesn't count —
// the conversation list is the agent's mailbox view, not the
// reviewer's HITL queue.
type ConversationSummary struct {
	ID             string    `json:"conversation_id"`
	LastMessageAt  time.Time `json:"last_message_at"`
	FirstMessageAt time.Time `json:"first_message_at"`
	MessageCount   int       `json:"message_count"`
	InboundCount   int       `json:"inbound_count"`
	OutboundCount  int       `json:"outbound_count"`
	HasUnread      bool      `json:"has_unread"`
	LatestSubject  string    `json:"latest_subject"`
	LatestSender   string    `json:"latest_sender"`
}

// ConversationDetail extends the summary with member messages and
// computed aggregates (participants set, label union). Messages are
// returned chronologically (oldest first) — the rendering convention
// for a thread view.
type ConversationDetail struct {
	ConversationSummary
	Participants []string  `json:"participants"`
	Labels       []string  `json:"labels"`
	Messages     []Message `json:"messages"`
}

// ConversationListFilter is the input to ListConversationsByAgent.
// Limit is capped to ConversationListHardCap at the storage layer
// regardless of what the caller passes; pagination is intentionally
// not in this slice (most agents have dozens of conversations, not
// thousands) and can be added cursor-style if a deployment needs it.
type ConversationListFilter struct {
	AgentID string
	Limit   int
	// Since / Until bracket the conversation's last_message_at —
	// "show me conversations that had activity in this window".
	// Zero values disable each bound.
	Since time.Time
	Until time.Time
}

// ConversationListHardCap is the maximum number of conversations a
// single list call returns. Higher requests are silently clamped.
// 100 covers the inbox-style use case; a deployment that needs more
// can either ask for higher (we'll bump it) or paginate (slice 2).
const ConversationListHardCap = 100

// ListConversationsByAgent groups the agent's non-expired messages
// by conversation_id and returns one row per conversation sorted by
// most-recent activity. Messages without a conversation_id are not
// included in any conversation — they remain individually visible
// via GetMessagesByAgent.
func (s *Store) ListConversationsByAgent(ctx context.Context, f ConversationListFilter) ([]ConversationSummary, error) {
	limit := f.Limit
	if limit <= 0 || limit > ConversationListHardCap {
		limit = ConversationListHardCap
	}

	query := `
		SELECT
		  conversation_id,
		  MAX(created_at)                          AS last_message_at,
		  MIN(created_at)                          AS first_message_at,
		  COUNT(*)                                 AS message_count,
		  COUNT(*) FILTER (WHERE direction='inbound')  AS inbound_count,
		  COUNT(*) FILTER (WHERE direction='outbound') AS outbound_count,
		  -- BOOL_OR returns NULL when every row's expression is NULL
		  -- (e.g. all-outbound conversations where inbox_status is
		  -- NULL — the column is nullable). COALESCE to false so
		  -- the *bool scan never fails on legitimate edge cases.
		  COALESCE(BOOL_OR(direction='inbound' AND inbox_status='unread'), false) AS has_unread,
		  (ARRAY_AGG(COALESCE(subject, '') ORDER BY created_at DESC))[1] AS latest_subject,
		  (ARRAY_AGG(COALESCE(sender, '')  ORDER BY created_at DESC))[1] AS latest_sender
		FROM messages
		WHERE agent_id = $1
		  AND conversation_id <> ''
		  AND expires_at > now()
		GROUP BY conversation_id`

	args := []interface{}{f.AgentID}
	if !f.Since.IsZero() {
		query += fmt.Sprintf(` HAVING MAX(created_at) >= $%d`, len(args)+1)
		args = append(args, f.Since)
		if !f.Until.IsZero() {
			query += fmt.Sprintf(` AND MAX(created_at) < $%d`, len(args)+1)
			args = append(args, f.Until)
		}
	} else if !f.Until.IsZero() {
		query += fmt.Sprintf(` HAVING MAX(created_at) < $%d`, len(args)+1)
		args = append(args, f.Until)
	}
	query += fmt.Sprintf(` ORDER BY MAX(created_at) DESC, conversation_id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationSummary
	for rows.Next() {
		var c ConversationSummary
		if err := rows.Scan(
			&c.ID, &c.LastMessageAt, &c.FirstMessageAt,
			&c.MessageCount, &c.InboundCount, &c.OutboundCount,
			&c.HasUnread, &c.LatestSubject, &c.LatestSender,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConversationByID returns the aggregate summary fields plus every
// member message, ordered oldest-first (chronological reading order).
// Returns ErrMessageNotFound when no non-expired messages exist for
// the given (agentID, conversationID) — mirrors the
// "looks-like-not-found-on-cross-agent" convention used by single-
// message reads. The same code path handles "wrong agent" and "real
// non-existent": either way the agent has no business seeing it.
//
// Participants are computed as the union of sender + recipient +
// each row's to_recipients / cc / bcc (when populated). Empty
// strings are dropped. Labels are the union of all members'
// labels[]; both are sorted lexicographically for stable output.
func (s *Store) GetConversationByID(ctx context.Context, agentID, conversationID string) (*ConversationDetail, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient,
		        m.to_recipients, m.cc, m.bcc, m.reply_to,
		        m.subject, COALESCE(m.email_message_id, ''),
		        COALESCE(m.method, ''), COALESCE(m.message_type, ''),
		        m.conversation_id, COALESCE(m.inbox_status, ''),
		        COALESCE(m.status, ''),
		        m.created_at, m.expires_at,
		        m.labels,
		        COALESCE(m.delivery_status, ''), COALESCE(m.delivery_detail, ''), COALESCE(m.sent_as, ''), m.auth_verdict,
		        COALESCE(m.flagged, false), COALESCE(m.flag_reason, '')
		 FROM messages m
		 WHERE m.agent_id = $1
		   AND m.conversation_id = $2
		   AND m.expires_at > now()
		 ORDER BY m.created_at ASC, m.id ASC`,
		agentID, conversationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	d := &ConversationDetail{}
	participantSet := map[string]struct{}{}
	labelSet := map[string]struct{}{}

	for rows.Next() {
		var m Message
		var outboundDeliveryStatus string
		var authVerdict []byte
		if err := rows.Scan(
			&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient,
			&m.ToRecipients, &m.CC, &m.BCC, &m.ReplyTo,
			&m.Subject, &m.EmailMessageID,
			&m.Method, &m.Type,
			&m.ConversationID, &m.InboxStatus,
			&m.Status,
			&m.CreatedAt, &m.ExpiresAt,
			&m.Labels,
			&outboundDeliveryStatus, &m.DeliveryDetail, &m.SentAs, &authVerdict,
			&m.Flagged, &m.FlagReason,
		); err != nil {
			return nil, err
		}
		if err := unmarshalAuthVerdict(authVerdict, &m); err != nil {
			return nil, err
		}
		// DeliveryStatus is overloaded by direction (see Message.DeliveryStatus):
		// inbound carries inbox_status, outbound carries the delivery rollup.
		if m.Direction == "outbound" {
			m.DeliveryStatus = outboundDeliveryStatus
		} else {
			m.DeliveryStatus = m.InboxStatus
		}
		d.Messages = append(d.Messages, m)

		// Accumulate aggregates as we go — cheaper than a second
		// pass and keeps memory bounded to the unique-strings set.
		if m.Sender != "" {
			participantSet[m.Sender] = struct{}{}
		}
		if m.Recipient != "" {
			participantSet[m.Recipient] = struct{}{}
		}
		for _, a := range m.ToRecipients {
			if a != "" {
				participantSet[a] = struct{}{}
			}
		}
		for _, a := range m.CC {
			if a != "" {
				participantSet[a] = struct{}{}
			}
		}
		for _, a := range m.BCC {
			if a != "" {
				participantSet[a] = struct{}{}
			}
		}
		for _, l := range m.Labels {
			labelSet[l] = struct{}{}
		}

		// Maintain the aggregate counts inline.
		d.MessageCount++
		if m.Direction == "inbound" {
			d.InboundCount++
			if m.InboxStatus == "unread" {
				d.HasUnread = true
			}
		} else if m.Direction == "outbound" {
			d.OutboundCount++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if d.MessageCount == 0 {
		return nil, ErrMessageNotFound
	}

	d.ID = conversationID
	// Messages are oldest-first so [0] is first and [n-1] is last.
	d.FirstMessageAt = d.Messages[0].CreatedAt
	d.LastMessageAt = d.Messages[d.MessageCount-1].CreatedAt
	latest := d.Messages[d.MessageCount-1]
	d.LatestSubject = latest.Subject
	d.LatestSender = latest.Sender

	d.Participants = make([]string, 0, len(participantSet))
	for p := range participantSet {
		d.Participants = append(d.Participants, p)
	}
	sort.Strings(d.Participants)

	d.Labels = make([]string, 0, len(labelSet))
	for l := range labelSet {
		d.Labels = append(d.Labels, l)
	}
	sort.Strings(d.Labels)

	return d, nil
}

// --- User management ---

func (s *Store) CreateOrGetUser(ctx context.Context, email, name, googleSub string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, name, google_subject)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (google_subject) DO UPDATE SET email = EXCLUDED.email, name = EXCLUDED.name
		 RETURNING id, email, name, google_subject, created_at`,
		generateID(), email, name, googleSub,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	// Idempotent: existing users return early in EnsureUserHasSigningSecret.
	if err := s.EnsureUserHasSigningSecret(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("ensure signing secret: %w", err)
	}
	return u, nil
}

// BootstrapUser finds a user by email, or creates one with a synthetic
// google_subject if none exists. Used by the -bootstrap-email CLI flag
// for self-host first-run, where there's no Google OAuth flow yet.
func (s *Store) BootstrapUser(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, name, google_subject, created_at FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err == nil {
		// Existing user — make sure they still have at least one secret
		// (covers the case where the migration backfill didn't run yet).
		if err := s.EnsureUserHasSigningSecret(ctx, u.ID); err != nil {
			return nil, fmt.Errorf("ensure signing secret: %w", err)
		}
		return u, nil
	}
	id := generateID()
	err = s.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, name, google_subject)
		 VALUES ($1, $2, 'bootstrap', $3)
		 RETURNING id, email, name, google_subject, created_at`,
		id, email, "bootstrap:"+id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := s.EnsureUserHasSigningSecret(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("ensure signing secret: %w", err)
	}
	return u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, name, google_subject, created_at FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUserName persists a new display name on the user row and
// returns the updated User. Input validation (length, whitespace) is
// the caller's responsibility — this layer only enforces that the row
// exists.
func (s *Store) UpdateUserName(ctx context.Context, userID, name string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`UPDATE users SET name = $1 WHERE id = $2
		 RETURNING id, email, name, google_subject, created_at`,
		name, userID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// --- Session management ---

const SessionTTL = 7 * 24 * time.Hour

func (s *Store) CreateUserSession(ctx context.Context, userID string) (string, error) {
	token := "sess_" + randomHex32() // opaque session cookie value
	expiresAt := time.Now().Add(SessionTTL)
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_sessions (token, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4)`,
		token, userID, time.Now(), expiresAt,
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) GetUserSession(ctx context.Context, token string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`SELECT u.id, u.email, u.name, u.google_subject, u.created_at
		 FROM user_sessions s JOIN users u ON s.user_id = u.id
		 WHERE s.token = $1 AND s.expires_at > now()`, token,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) DeleteUserSession(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE token = $1`, token)
	return err
}

func (s *Store) DeleteExpiredUserSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- Dashboard aggregates ---

// DashboardStats is the workspace-level summary returned by
// GetDashboardStats. Each section corresponds to one of the cards on the
// redesigned dashboard's stats strip; null/zero values render as "—"
// in the UI, so deployments without E2A_USAGE_TRACKING enabled
// degrade gracefully.
type DashboardStats struct {
	Today              DashboardTodayStats   `json:"today"`
	Pending            DashboardPendingStats `json:"pending"`
	DeliverySuccessPct float64               `json:"delivery_success_pct"`
	SampleWindowDays   int                   `json:"sample_window_days"`
	// InboundWindow / OutboundWindow are the totals over the same
	// SampleWindowDays as DeliverySuccessPct. The dashboard at-a-glance
	// strip uses Today.*; the settings page uses these window totals
	// at a 30-day window (?window=30). Sum over usage_summaries rows
	// in the lookback period.
	InboundWindow  int `json:"inbound_window"`
	OutboundWindow int `json:"outbound_window"`
}

type DashboardTodayStats struct {
	Inbound          int `json:"inbound"`
	Outbound         int `json:"outbound"`
	InboundDeltaPct  int `json:"inbound_delta_pct"`
	OutboundDeltaPct int `json:"outbound_delta_pct"`
}

type DashboardPendingStats struct {
	Count         int `json:"count"`
	OldestSeconds int `json:"oldest_seconds"`
}

// DashboardDefaultWindowDays is the lookback for the dashboard strip
// when the caller doesn't request a specific window.
const DashboardDefaultWindowDays = 7

// DashboardMaxWindowDays caps the lookback to keep the underlying SQL
// scan bounded. 90 days is generous for any UI surface we currently
// have and remains efficient given the per-user index on
// usage_summaries.
const DashboardMaxWindowDays = 90

// GetDashboardStats returns workspace-level aggregates for the
// authenticated user, with a configurable lookback window. windowDays
// controls Inbound/Outbound totals AND the delivery-success ratio's
// sample period — passing 0 falls back to DashboardDefaultWindowDays
// (7), values above DashboardMaxWindowDays (90) are clamped.
//
// Three independent reads — kept separate because the source tables
// have different indexes and one slow read shouldn't slow the others.
// All reads are O(rows-for-this-user-only) thanks to the existing
// per-user indexes.
//
// Robust to missing data: deployments without usage tracking enabled
// (E2A_USAGE_TRACKING=false — the default for self-hosters) return
// zero counts rather than erroring. Same for users who have no
// messages yet. The UI renders zero values as "—".
//
// Delta percentages: today vs yesterday on usage_summaries. Avoids
// divide-by-zero when yesterday was zero by returning 0. 100% in/de-
// crease maps to ±100; values clipped at ±999 for integer width.
func (s *Store) GetDashboardStats(ctx context.Context, userID string, windowDays int) (*DashboardStats, error) {
	if windowDays <= 0 {
		windowDays = DashboardDefaultWindowDays
	}
	if windowDays > DashboardMaxWindowDays {
		windowDays = DashboardMaxWindowDays
	}
	stats := &DashboardStats{
		SampleWindowDays: windowDays,
	}

	// 1) Today's usage and yesterday's baseline. LEFT JOIN trick keeps
	// the query a single row even when one or both buckets are absent.
	var todayInbound, todayOutbound, yesterdayInbound, yesterdayOutbound int
	err := s.pool.QueryRow(ctx,
		`SELECT
		   COALESCE((SELECT inbound_count  FROM usage_summaries WHERE user_id = $1 AND bucket_date = current_date), 0),
		   COALESCE((SELECT outbound_count FROM usage_summaries WHERE user_id = $1 AND bucket_date = current_date), 0),
		   COALESCE((SELECT inbound_count  FROM usage_summaries WHERE user_id = $1 AND bucket_date = current_date - 1), 0),
		   COALESCE((SELECT outbound_count FROM usage_summaries WHERE user_id = $1 AND bucket_date = current_date - 1), 0)`,
		userID).Scan(&todayInbound, &todayOutbound, &yesterdayInbound, &yesterdayOutbound)
	if err != nil {
		return nil, fmt.Errorf("today/yesterday usage: %w", err)
	}
	stats.Today = DashboardTodayStats{
		Inbound:          todayInbound,
		Outbound:         todayOutbound,
		InboundDeltaPct:  deltaPct(todayInbound, yesterdayInbound),
		OutboundDeltaPct: deltaPct(todayOutbound, yesterdayOutbound),
	}

	// 2) Pending HITL approvals across the user's agents. Joining via
	// the agent_id keeps the per-user partial index on messages
	// (idx_messages_pending_approval) usable.
	var pendingCount int
	var oldestSec *int
	err = s.pool.QueryRow(ctx,
		`SELECT count(*),
		        CASE WHEN count(*) = 0 THEN NULL
		             ELSE EXTRACT(EPOCH FROM (now() - MIN(m.created_at)))::int
		        END
		 FROM messages m
		 JOIN agent_identities a ON a.id = m.agent_id
		 WHERE a.user_id = $1 AND m.status = 'pending_approval'`,
		userID).Scan(&pendingCount, &oldestSec)
	if err != nil {
		return nil, fmt.Errorf("pending count: %w", err)
	}
	stats.Pending.Count = pendingCount
	if oldestSec != nil {
		stats.Pending.OldestSeconds = *oldestSec
	}

	// 3) Window aggregates: inbound + outbound totals and the delivery
	// success ratio, all over the same lookback. Three subqueries in
	// one round-trip — usage_summaries is keyed (user_id, bucket_date)
	// so the per-user index handles each scan cheaply. windowDays is
	// validated above (1..90), so direct interpolation into the SQL
	// is safe and keeps the query plan-cacheable.
	var winInbound, winOutbound int
	var successRatio *float64
	err = s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT
		   COALESCE((SELECT sum(inbound_count)::int  FROM usage_summaries
		             WHERE user_id = $1 AND bucket_date > current_date - %d), 0) AS inbound_window,
		   COALESCE((SELECT sum(outbound_count)::int FROM usage_summaries
		             WHERE user_id = $1 AND bucket_date > current_date - %d), 0) AS outbound_window,
		   (SELECT (count(*) FILTER (WHERE wd.status = 'delivered'))::float
		            / NULLIF(count(*) FILTER (WHERE wd.status IN ('delivered','failed')), 0)
		      FROM webhook_deliveries wd
		      JOIN messages m ON m.id = wd.message_id
		      JOIN agent_identities a ON a.id = m.agent_id
		      WHERE a.user_id = $1
		        AND wd.created_at > now() - interval '%d days')`,
			windowDays, windowDays, windowDays),
		userID).Scan(&winInbound, &winOutbound, &successRatio)
	if err != nil {
		return nil, fmt.Errorf("window aggregates: %w", err)
	}
	stats.InboundWindow = winInbound
	stats.OutboundWindow = winOutbound
	if successRatio != nil {
		// Round to one decimal place — 99.6 is more useful than 99.555555.
		stats.DeliverySuccessPct = float64(int(*successRatio*1000+0.5)) / 10.0
	}

	return stats, nil
}

// deltaPct computes the integer percentage change of current vs
// previous. Zero previous → 0 (no arrow in UI). Clipped to ±999 to
// keep the value width manageable.
func deltaPct(current, previous int) int {
	if previous == 0 {
		return 0
	}
	delta := float64(current-previous) / float64(previous) * 100
	if delta > 999 {
		return 999
	}
	if delta < -999 {
		return -999
	}
	return int(delta)
}

// --- Per-user API keys ---

// Credential scope (Slice 5a / design §5). The scope a credential carries —
// not the auth method — determines its blast radius.
const (
	// ScopeAccount is account-wide admin: agent/domain/key management, account
	// settings. The pre-redesign default; what an `e2a_acct_…` key holds.
	ScopeAccount = "account"
	// ScopeAgent is bound to a single agent (runtime/inbox tier): the credential
	// IS the agent. Pinned to one agent_id and barred from account-only ops.
	ScopeAgent = "agent"
)

// ValidScope reports whether s is a known credential scope.
func ValidScope(s string) bool { return s == ScopeAccount || s == ScopeAgent }

// Principal is the authenticated caller resolved from a credential: the owning
// user plus the credential's scope and (for agent-scoped credentials) the agent
// it is bound to. The scope/agent binding is what the v1 handlers enforce the
// hard scope ceiling against (design §5 / decision 10).
type Principal struct {
	User    *User
	Scope   string // ScopeAccount | ScopeAgent
	AgentID string // non-empty only when Scope == ScopeAgent
}

type APIKey struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Name         string    `json:"name"`
	KeyPrefix    string    `json:"key_prefix"`
	PlaintextKey string    `json:"key,omitempty"` // only set once at creation, never stored
	CreatedAt    time.Time `json:"created_at"`
	// Scope is the credential's blast radius (ScopeAccount | ScopeAgent).
	// Backfilled to ScopeAccount for pre-Slice-5a keys.
	Scope string `json:"scope"`
	// AgentID is the bound agent for ScopeAgent keys; nil for account keys.
	AgentID *string `json:"agent_id,omitempty"`
	// LastUsedAt is updated by GetUserByAPIKey on every successful
	// AuthenticateRequest. NULL on keys that have never been used.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// ExpiresAt is the optional hard expiry. AuthenticateRequest rejects
	// keys whose expires_at has passed. NULL means "never expires"
	// (the backward-compatible default).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func hashAPIKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey issues a fresh ACCOUNT-scoped API key for the user. expiresAt is
// the optional hard expiration; pass nil to issue a never-expiring key (the
// backward-compatible default). This is the account-tier convenience wrapper
// over CreateScopedAPIKey — the self-host default key.
func (s *Store) CreateAPIKey(ctx context.Context, userID, name string, expiresAt *time.Time) (*APIKey, error) {
	return s.CreateScopedAPIKey(ctx, userID, name, ScopeAccount, "", expiresAt)
}

// CreateScopedAPIKey issues a fresh API key with an explicit scope (Slice 5a).
//   - ScopeAccount: account-wide admin; agentID must be empty; prefix e2a_acct_.
//   - ScopeAgent: bound to agentID (which must be a non-empty agent owned by the
//     user); prefix e2a_agt_. The key can only act as that one agent.
//
// The visible prefix makes a key's blast radius obvious at a glance, and the DB
// CHECK (scope='agent') == (agent_id IS NOT NULL) backstops the binding.
func (s *Store) CreateScopedAPIKey(ctx context.Context, userID, name, scope, agentID string, expiresAt *time.Time) (*APIKey, error) {
	if !ValidScope(scope) {
		return nil, fmt.Errorf("invalid credential scope %q", scope)
	}
	if scope == ScopeAgent && agentID == "" {
		return nil, fmt.Errorf("agent-scoped key requires an agent_id")
	}
	if scope == ScopeAccount && agentID != "" {
		return nil, fmt.Errorf("account-scoped key must not name an agent")
	}
	// For an agent-scoped key, the named agent must exist and be owned by the
	// same user — otherwise a caller could mint a key bound to someone else's
	// agent (the FK alone wouldn't catch cross-user binding).
	if scope == ScopeAgent {
		owns, err := s.userOwnsAgent(ctx, agentID, userID)
		if err != nil {
			return nil, err
		}
		if !owns {
			return nil, fmt.Errorf("agent %q not found or not owned by user", agentID)
		}
	}

	id := "apk_" + generateID()
	plaintext := generateAPIKey(scope)
	keyHash := hashAPIKey(plaintext)
	// Show the scoped prefix + a few key chars (e.g. "e2a_agt_abcd…").
	prefix := plaintext[:16]
	now := time.Now()
	var agentCol *string
	if scope == ScopeAgent {
		agentCol = &agentID
	}
	ak := &APIKey{
		ID:           id,
		UserID:       userID,
		Name:         name,
		KeyPrefix:    prefix,
		PlaintextKey: plaintext,
		CreatedAt:    now,
		Scope:        scope,
		AgentID:      agentCol,
		ExpiresAt:    expiresAt,
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, name, key_prefix, key_hash, scope, agent_id, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		ak.ID, ak.UserID, ak.Name, ak.KeyPrefix, keyHash, ak.Scope, agentCol, ak.CreatedAt, ak.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return ak, nil
}

// userOwnsAgent reports whether agentID exists and is owned by userID.
func (s *Store) userOwnsAgent(ctx context.Context, agentID, userID string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agent_identities WHERE id = $1 AND user_id = $2)`,
		agentID, userID,
	).Scan(&exists)
	return exists, err
}

func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, key_prefix, COALESCE(scope, 'account'), agent_id, created_at, last_used_at, expires_at
		   FROM api_keys WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &k.Scope, &k.AgentID, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) DeleteAPIKey(ctx context.Context, keyID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, keyID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("api key not found or not owned by user")
	}
	return nil
}

// GetUserByAPIKey authenticates a bearer token and returns the owning
// user. Rejects revoked keys and time-expired keys; touches last_used_at
// only on the success path so the column stays a real "last successful
// authentication" signal (rather than "last attempt").
//
// Expiration semantics: expires_at IS NULL means the key never expires
// (preserves the pre-migration default). A non-null expires_at must be in
// the future, evaluated against now() in the same query so there's no
// clock skew between row read and check.
func (s *Store) GetUserByAPIKey(ctx context.Context, apiKey string) (*User, error) {
	p, err := s.GetPrincipalByAPIKey(ctx, apiKey)
	if err != nil {
		return nil, err
	}
	return p.User, nil
}

// GetPrincipalByAPIKey authenticates a bearer token and returns the full
// principal — the owning user PLUS the key's scope and bound agent (Slice 5a).
// Same validation/last_used semantics as GetUserByAPIKey (it delegates here).
// A legacy key with a NULL scope column resolves to ScopeAccount, preserving
// pre-redesign authority.
func (s *Store) GetPrincipalByAPIKey(ctx context.Context, apiKey string) (*Principal, error) {
	keyHash := hashAPIKey(apiKey)
	u := &User{}
	var scope string
	var agentID *string
	err := s.pool.QueryRow(ctx,
		`WITH touched AS (
		   UPDATE api_keys SET last_used_at = now()
		   WHERE key_hash = $1
		     AND revoked_at IS NULL
		     AND (expires_at IS NULL OR expires_at > now())
		   RETURNING user_id, COALESCE(scope, 'account') AS scope, agent_id
		 )
		 SELECT u.id, u.email, u.name, u.google_subject, u.created_at, t.scope, t.agent_id
		 FROM touched t JOIN users u ON u.id = t.user_id`, keyHash,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt, &scope, &agentID)
	if err != nil {
		return nil, err
	}
	p := &Principal{User: u, Scope: scope}
	if agentID != nil {
		p.AgentID = *agentID
	}
	return p, nil
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure means the OS RNG is broken — without it
		// we'd silently emit an all-zero ID. Panic to surface a 500
		// rather than poison the database with predictable identifiers.
		panic(fmt.Sprintf("identity: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// generateAPIKey mints a random key with a scope-revealing prefix (Slice 5a):
// e2a_acct_… for account keys, e2a_agt_… for agent keys. The prefix is cosmetic
// for validation (keys are matched by hash of the full string), but makes a
// key's blast radius obvious wherever it's pasted or logged. Legacy `e2a_…`
// keys minted before this change keep validating — the hash is over the whole
// string, so the prefix change only affects newly minted keys.
func generateAPIKey(scope string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Same reasoning as generateID — an all-zero API key would be
		// catastrophic (predictable auth credential).
		panic(fmt.Sprintf("identity: crypto/rand failed: %v", err))
	}
	prefix := "e2a_acct_"
	if scope == ScopeAgent {
		prefix = "e2a_agt_"
	}
	return prefix + hex.EncodeToString(b)
}

// randomHex32 returns 32 bytes of crypto-random data hex-encoded. Shared by the
// session-token path; panics on RNG failure (same reasoning as generateID).
func randomHex32() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("identity: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// --- Webhook signing secrets ---

// MaxSigningSecretsPerUser caps how many active signing secrets a user
// can hold at once. Two slots covers the standard rotation flow (create
// new, swap, delete old); a hard cap higher than that mostly catches
// runaway scripts. Easy to raise later if real users need more.
const MaxSigningSecretsPerUser = 5

// Sentinel errors so API handlers can map error → HTTP status with
// errors.Is rather than string-matching the message text. Tests can
// also assert against them directly.
var (
	ErrSigningSecretCapReached       = fmt.Errorf("at most %d signing secrets per user; delete one before creating another", MaxSigningSecretsPerUser)
	ErrCannotDeleteLastSigningSecret = errors.New("cannot delete the last signing secret; create a new one first")
	ErrSigningSecretNotFound         = errors.New("signing secret not found or not owned by user")
)

// SigningSecret is one of a user's HMAC secrets used to sign their
// agents' inbound webhook payloads and HITL approval magic-link tokens.
//
// The plaintext Secret is only set in the response to a fresh
// CreateSigningSecret call (and what's persisted in the DB row); list
// operations omit it and surface a SecretPrefix preview instead.
type SigningSecret struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	Name         string     `json:"name"`
	Secret       string     `json:"secret,omitempty"`        // only on creation
	SecretPrefix string     `json:"secret_prefix,omitempty"` // first 12 chars, for list/get
	CreatedAt    time.Time  `json:"created_at"`
	LastSignedAt *time.Time `json:"last_signed_at,omitempty"`
}

// SigningSecretWithValue carries the plaintext Secret alongside the ID
// so the relay can both sign with the value and (asynchronously)
// update last_signed_at on the right row. Returned by
// GetUserSigningSecrets in most-recent-first order.
type SigningSecretWithValue struct {
	ID     string
	Secret string
}

func generateSigningSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never returns an error on supported platforms;
		// if it does, panic — secret generation is a hard prerequisite.
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// withUserSecretsLock takes a row lock on the user's row for the
// duration of fn. Serializes concurrent CreateSigningSecret /
// DeleteSigningSecret / EnsureUserHasSigningSecret calls for the same
// user so the MaxSigningSecretsPerUser check + insert is race-free, and
// the "refuse last delete" check + delete is race-free.
//
// SELECT ... FOR UPDATE is preferred over pg_advisory_xact_lock here
// because the lock is scoped to a real row (no name-collision concerns,
// no interaction with table-level locks like TRUNCATE in test
// environments). Released when the transaction commits or rolls back.
func (s *Store) withUserSecretsLock(ctx context.Context, userID string, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedID); err != nil {
		return fmt.Errorf("lock user %s for signing-secret op: %w", userID, err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// EnsureUserHasSigningSecret guarantees the user has at least one
// signing secret, creating a "default" one if not. Idempotent.
// Concurrent callers serialize via the per-user advisory lock so we
// can't accidentally insert two "default" rows.
func (s *Store) EnsureUserHasSigningSecret(ctx context.Context, userID string) error {
	return s.withUserSecretsLock(ctx, userID, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM webhook_signing_secrets WHERE user_id = $1`, userID,
		).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO webhook_signing_secrets (id, user_id, secret, name, created_at)
			 VALUES ($1, $2, $3, $4, NOW())`,
			"wsec_"+generateID(), userID, generateSigningSecret(), "default",
		)
		return err
	})
}

// CreateSigningSecret mints a new secret for the user. The plaintext
// secret value is set on the returned struct exactly once; subsequent
// reads (List/Get) only see the prefix.
//
// Returns ErrSigningSecretCapReached if the user is already at
// MaxSigningSecretsPerUser. Race-free under concurrent callers via the
// per-user advisory lock.
//
// Empty `name` is normalized server-side to "unnamed" so the dashboard
// always has something to display.
func (s *Store) CreateSigningSecret(ctx context.Context, userID, name string) (*SigningSecret, error) {
	if name == "" {
		name = "unnamed"
	}
	id := "wsec_" + generateID()
	plaintext := generateSigningSecret()
	now := time.Now()

	err := s.withUserSecretsLock(ctx, userID, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM webhook_signing_secrets WHERE user_id = $1`, userID,
		).Scan(&count); err != nil {
			return err
		}
		if count >= MaxSigningSecretsPerUser {
			return ErrSigningSecretCapReached
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO webhook_signing_secrets (id, user_id, secret, name, created_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, userID, plaintext, name, now,
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &SigningSecret{
		ID:           id,
		UserID:       userID,
		Name:         name,
		Secret:       plaintext,
		SecretPrefix: plaintext[:12],
		CreatedAt:    now,
	}, nil
}

// ListSigningSecrets returns the user's secrets in most-recent-first
// order. Populates both Secret (plaintext) and SecretPrefix; callers
// that build a list shape for the dashboard get to choose which to
// surface.
func (s *Store) ListSigningSecrets(ctx context.Context, userID string) ([]SigningSecret, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, secret, substring(secret, 1, 12), created_at, last_signed_at
		 FROM webhook_signing_secrets WHERE user_id = $1
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SigningSecret
	for rows.Next() {
		var s SigningSecret
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.Secret, &s.SecretPrefix, &s.CreatedAt, &s.LastSignedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetUserSigningSecrets returns the plaintext secret values for a user
// (paired with their IDs), most-recent-first. The relay signs with
// [0] and asynchronously updates last_signed_at on that ID. The HITL
// token verifier tries each Secret in turn. Caller must NOT log the
// Secret values.
func (s *Store) GetUserSigningSecrets(ctx context.Context, userID string) ([]SigningSecretWithValue, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, secret FROM webhook_signing_secrets WHERE user_id = $1 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SigningSecretWithValue
	for rows.Next() {
		var v SigningSecretWithValue
		if err := rows.Scan(&v.ID, &v.Secret); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteSigningSecret removes a secret. Refuses to delete the user's
// last secret — every user must keep at least one so webhooks remain
// verifiable. Race-free under concurrent callers via the per-user
// row lock.
//
// Check order matters: ownership first (so an attacker probing IDs
// they don't own gets 404, not "cannot delete last" leaking that the
// caller has only 1 secret), then the floor.
func (s *Store) DeleteSigningSecret(ctx context.Context, secretID, userID string) error {
	return s.withUserSecretsLock(ctx, userID, func(tx pgx.Tx) error {
		var found int
		if err := tx.QueryRow(ctx,
			`SELECT 1 FROM webhook_signing_secrets WHERE id = $1 AND user_id = $2`,
			secretID, userID,
		).Scan(&found); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSigningSecretNotFound
			}
			return err
		}
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM webhook_signing_secrets WHERE user_id = $1`, userID,
		).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return ErrCannotDeleteLastSigningSecret
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM webhook_signing_secrets WHERE id = $1 AND user_id = $2`,
			secretID, userID,
		)
		return err
	})
}

// TouchSigningSecretLastSigned records that the relay used this secret
// to sign a payload. Best-effort — failure is logged but does not block
// the actual signing operation.
func (s *Store) TouchSigningSecretLastSigned(ctx context.Context, secretID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE webhook_signing_secrets SET last_signed_at = NOW() WHERE id = $1`,
		secretID,
	)
	return err
}
