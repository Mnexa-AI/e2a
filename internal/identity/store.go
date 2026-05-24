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
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/dkim"
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
}

type AgentIdentity struct {
	ID                   string    `json:"id"`
	Domain               string    `json:"domain"`
	Email                string    `json:"email"`
	Name                 string    `json:"name"`
	WebhookURL           string    `json:"webhook_url"`
	AgentMode            string    `json:"agent_mode"`
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
	// yet — avoids painting fresh agents red. Meaningless for
	// agent_mode='local'; the frontend hides the badge in that case.
	WebhookHealthy bool `json:"webhook_healthy"`
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
	ConversationID    string            `json:"conversation_id,omitempty"`
	DeliveryStatus    string            `json:"delivery_status,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	ExpiresAt         time.Time         `json:"expires_at"`
	WebhookStatus     string            `json:"webhook_status,omitempty"`
	WebhookError      string            `json:"webhook_error,omitempty"`
	WebhookAttempts   int               `json:"webhook_attempts,omitempty"`
	// SizeBytes is the byte length of raw_message. Populated by load paths
	// that compute it (e.g. GetMessagesByAgent for the dashboard inbox).
	// Zero on load paths that don't — the inbox renders "—" in that case.
	SizeBytes         int               `json:"size_bytes,omitempty"`
	// InboxStatus mirrors messages.inbox_status ('unread' | 'read') for
	// inbound rows. Kept separate from DeliveryStatus (which currently
	// carries the same value under a confusing JSON key — see line 161)
	// so the dashboard's inbox can read it under a non-overloaded key.
	// Empty on outbound rows. Populated by GetMessagesByAgent.
	InboxStatus       string            `json:"inbox_status,omitempty"`

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

	// HITL approval fields. Status defaults to 'sent'; body and attachments
	// are populated only while a message is in 'pending_approval', and are
	// scrubbed on any terminal transition.
	Status             string          `json:"status,omitempty"`
	ApprovalExpiresAt  *time.Time      `json:"approval_expires_at,omitempty"`
	ReviewedAt         *time.Time      `json:"reviewed_at,omitempty"`
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
	ReviewedByName  *string `json:"reviewed_by_name,omitempty"`
	RejectionReason string          `json:"rejection_reason,omitempty"`
	Edited             bool            `json:"edited,omitempty"`
	BodyText           string          `json:"body_text,omitempty"`
	BodyHTML           string          `json:"body_html,omitempty"`
	AttachmentsJSON    json.RawMessage `json:"attachments,omitempty"`
}

// Message status values mirror the CHECK constraint in migration 003_hitl.sql.
const (
	MessageStatusSent             = "sent"
	MessageStatusPendingApproval  = "pending_approval"
	MessageStatusRejected         = "rejected"
	MessageStatusExpiredApproved  = "expired_approved"
	MessageStatusExpiredRejected  = "expired_rejected"
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
// Creates if new, overwrites user_id if unverified, returns if verified+same
// user, errors if verified+different user. Callers are responsible for
// rejecting the configured shared domain before invoking this — the store
// has no concept of a reserved domain.
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

	// Atomic upsert: insert new or overwrite unverified. The DKIM
	// columns are only written on a true INSERT — the ON CONFLICT
	// branch leaves any existing key in place so re-claiming an
	// unverified domain doesn't invalidate signatures on mail already
	// in flight.
	d := &Domain{}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO domains (domain, user_id, verified, verification_token, dkim_selector, dkim_public_key, dkim_private_key)
		 VALUES ($1, $2, false, $3, $4, $5, $6)
		 ON CONFLICT (domain) DO UPDATE
		 SET user_id = EXCLUDED.user_id,
		     verification_token = EXCLUDED.verification_token
		 WHERE domains.verified = false
		 RETURNING domain, user_id, verified, verification_token, created_at, verified_at, is_primary, last_checked_at, COALESCE(dkim_selector, ''), COALESCE(dkim_public_key, '')`,
		domain, userID, verificationToken, nullIfEmpty(dkimSelector), nullIfEmpty(dkimPubKey), nullIfEmptyBytes(dkimPrivKey),
	).Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt, &d.IsPrimary, &d.LastCheckedAt, &d.DKIMSelector, &d.DKIMPublicKey)

	if err == nil {
		return d, nil
	}

	// No row returned — domain exists and is verified. Check ownership.
	existing := &Domain{}
	err = s.pool.QueryRow(ctx,
		`SELECT domain, user_id, verified, verification_token, created_at, verified_at, is_primary, last_checked_at, COALESCE(dkim_selector, ''), COALESCE(dkim_public_key, '')
		 FROM domains WHERE domain = $1`, domain,
	).Scan(&existing.Domain, &existing.UserID, &existing.Verified, &existing.VerificationToken, &existing.CreatedAt, &existing.VerifiedAt, &existing.IsPrimary, &existing.LastCheckedAt, &existing.DKIMSelector, &existing.DKIMPublicKey)
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

// GetDKIMKey returns the stored selector + private key bytes for a
// domain, used by the outbound signer. Returns ("", nil, nil) when the
// domain has no key — callers MUST treat this as "skip signing" and
// fall back to whatever the relay-level fallback does.
func (s *Store) GetDKIMKey(ctx context.Context, domain string) (string, []byte, error) {
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

// LookupDomain returns a domain if it exists and is owned by the given user.
func (s *Store) LookupDomain(ctx context.Context, domain, userID string) (*Domain, error) {
	d := &Domain{}
	err := s.pool.QueryRow(ctx,
		`SELECT domain, user_id, verified, verification_token, created_at, verified_at, is_primary, last_checked_at, COALESCE(dkim_selector, ''), COALESCE(dkim_public_key, '')
		 FROM domains WHERE domain = $1 AND user_id = $2`,
		normalizeDomain(domain), userID,
	).Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt, &d.IsPrimary, &d.LastCheckedAt, &d.DKIMSelector, &d.DKIMPublicKey)
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
		if err := rows.Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt, &d.IsPrimary, &d.LastCheckedAt, &d.DKIMSelector, &d.DKIMPublicKey, &d.AgentCount); err != nil {
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
	tag, err := s.pool.Exec(ctx,
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
	return nil
}

// --- Agent CRUD ---

// GetAgentByID looks up an agent by its ID (full email) with domain verification status.
func (s *Store) GetAgentByID(ctx context.Context, id string) (*AgentIdentity, error) {
	a := &AgentIdentity{}
	err := s.pool.QueryRow(ctx,
		`SELECT a.id, a.domain, a.user_id, a.name, a.webhook_url, a.agent_mode, a.public, a.created_at,
		        a.hitl_enabled, a.hitl_ttl_seconds, a.hitl_expiration_action,
		        d.verified as domain_verified
		 FROM agent_identities a
		 JOIN domains d ON a.domain = d.domain
		 WHERE a.id = $1`, id,
	).Scan(&a.ID, &a.Domain, &a.UserID, &a.Name, &a.WebhookURL, &a.AgentMode, &a.Public, &a.CreatedAt,
		&a.HITLEnabled, &a.HITLTTLSeconds, &a.HITLExpirationAction,
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
func (s *Store) CreateAgent(ctx context.Context, agentEmail, domain, name, webhookURL, agentMode, userID string) (*AgentIdentity, error) {
	return createAgent(ctx, s.pool, agentEmail, domain, name, webhookURL, agentMode, userID)
}

// CreateAgentTx inserts an agent inside a caller-owned transaction.
// Used by the OAuth consent flow so the slug auto-create row and the
// authorization-code insert (in oauth_auth_codes) commit together —
// without this, a code-issue failure after the agent commit would
// leave a phantom inbox the user never authorized.
func (s *Store) CreateAgentTx(ctx context.Context, tx pgx.Tx, agentEmail, domain, name, webhookURL, agentMode, userID string) (*AgentIdentity, error) {
	return createAgent(ctx, tx, agentEmail, domain, name, webhookURL, agentMode, userID)
}

// agentExecutor is the subset of pgxpool.Pool + pgx.Tx that
// createAgent needs. Lets the same body serve both stand-alone and
// in-transaction callers without duplicating the SQL.
type agentExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func createAgent(ctx context.Context, exec agentExecutor, agentEmail, domain, name, webhookURL, agentMode, userID string) (*AgentIdentity, error) {
	if agentMode == "" {
		agentMode = "cloud"
	}

	a := &AgentIdentity{
		ID:                   agentEmail,
		Domain:               normalizeDomain(domain),
		Name:                 name,
		WebhookURL:           webhookURL,
		AgentMode:            agentMode,
		Public:               true,
		CreatedAt:            time.Now(),
		UserID:               userID,
		HITLEnabled:          false,
		HITLTTLSeconds:       HITLDefaultTTLSeconds,
		HITLExpirationAction: HITLDefaultExpirationAct,
	}
	_, err := exec.Exec(ctx,
		`INSERT INTO agent_identities (id, domain, user_id, name, webhook_url, agent_mode, public, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.Domain, a.UserID, a.Name, a.WebhookURL, a.AgentMode, a.Public, a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	a.populateEmail()
	return a, nil
}

func (s *Store) UpdateAgentWebhook(ctx context.Context, agentID, userID, webhookURL string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_identities SET webhook_url = $1 WHERE id = $2 AND user_id = $3`,
		webhookURL, agentID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found or not owned by user")
	}
	return nil
}

func (s *Store) UpdateAgentMode(ctx context.Context, agentID, userID, agentMode, webhookURL string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE agent_identities SET agent_mode = $1, webhook_url = $2 WHERE id = $3 AND user_id = $4`,
		agentMode, webhookURL, agentID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agent not found or not owned by user")
	}
	return nil
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

// ListAgentsByUser returns all agents owned by the user, joined with
// domain verification AND enriched with per-agent stats for the
// dashboard. Five correlated subqueries compute
// inbound/outbound 7-day counts, pending approvals, last delivery, and
// webhook health in a single round-trip. Other load paths
// (GetAgentByID, GetAgentByEmail) intentionally don't compute these —
// only the dashboard needs them.
func (s *Store) ListAgentsByUser(ctx context.Context, userID string) ([]AgentIdentity, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT a.id, a.domain, a.user_id, a.name, a.webhook_url, a.agent_mode, a.public, a.created_at,
		        a.hitl_enabled, a.hitl_ttl_seconds, a.hitl_expiration_action,
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
		if err := rows.Scan(&a.ID, &a.Domain, &a.UserID, &a.Name, &a.WebhookURL, &a.AgentMode, &a.Public, &a.CreatedAt,
			&a.HITLEnabled, &a.HITLTTLSeconds, &a.HITLExpirationAction,
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

const MessageTTL = 30 * 24 * time.Hour // 30 days

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
func (s *Store) CreateInboundMessage(ctx context.Context, id, agentID, senderEmail, recipient, emailMessageID, subject, conversationID, deliveryStatus string, rawMessage []byte, authHeaders map[string]string, toRecipients, cc, replyTo []string) (*Message, error) {
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
		CreatedAt:      now,
		ExpiresAt:      now.Add(MessageTTL),
	}
	// inbox_status column has CHECK constraint: must be 'unread', 'read', or NULL
	var inboxStatus *string
	if m.DeliveryStatus == "unread" || m.DeliveryStatus == "read" {
		inboxStatus = &m.DeliveryStatus
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO messages (id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, raw_message, auth_headers, conversation_id, inbox_status, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		m.ID, m.AgentID, m.Direction, m.Sender, m.Recipient, m.ToRecipients, m.CC, m.ReplyTo, m.Subject, m.EmailMessageID, m.RawMessage, authHeadersJSON, m.ConversationID, inboxStatus, m.CreatedAt, m.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) GetInboundMessage(ctx context.Context, id string) (*Message, error) {
	m := &Message{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, raw_message, created_at, expires_at
		 FROM messages WHERE id = $1 AND direction = 'inbound' AND expires_at > now()`, id,
	).Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo, &m.Subject, &m.EmailMessageID, &m.RawMessage, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return m, nil
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
// inserted as SQL NULL rather than ''. Keeps body columns distinguishable
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
		        m.reviewed_by_user_id, r.name
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
		        m.status, m.approval_expires_at
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
// 'pending_approval' return ErrNotPendingApproval.
//
// Concurrency / failure mode notes:
//
//   - The row-level FOR UPDATE lock is held for the duration of the send
//     callback. In practice that is bounded by outbound.SMTPRelay's per-
//     attempt deadline (2min) plus its internal retry backoff (1s/5s/15s)
//     — worst case ~6.5min of lock on this single row. Other rows are
//     unaffected; deadlock is not possible because only one row is ever
//     locked per call.
//
//   - There is a narrow crash window where send() may succeed at SES but
//     the subsequent UPDATE or Commit fails (DB connection drop, pool
//     exhaustion). The transaction rolls back, the row stays pending, and
//     a retry — by the same reviewer or the expiration worker — would
//     re-send to SES. Eliminating this requires SES-side idempotency keys
//     or a separate "send attempts" table; deferred for v1. Callers that
//     see both a successful send callback and a subsequent error from
//     this function should log both rather than silently retry.
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
		 FOR UPDATE OF m`,
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

	result, err := send(&m)
	if err != nil {
		return nil, err
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
// system-scoped), SELECT ... FOR UPDATE SKIP LOCKED so concurrent workers
// don't race for the same row, and the terminal status is
// 'expired_approved' instead of 'sent'. On send failure the transaction
// rolls back; the worker should then call ExpireReject to move the row
// to a final state so the row doesn't get picked up on every sweep.
//
// Same concurrency / crash-window notes as ApproveAndSend apply: the
// row-level lock is held for the duration of the send callback (bounded
// by SMTPRelay timeouts), and a crash between SES acceptance and
// commit can leave a pending row that would re-send on the next sweep.
// SKIP LOCKED means multiple app instances can run the worker without
// contending on the same row.
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
		 FOR UPDATE SKIP LOCKED`,
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

	result, err := send(&m)
	if err != nil {
		return nil, err
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
		        COALESCE(m.conversation_id, ''), COALESCE(octet_length(m.raw_message), 0)
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
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.Subject, &m.EmailMessageID, &m.Method, &m.Type, &m.DeliveryStatus, &m.CreatedAt, &m.ExpiresAt, &m.WebhookStatus, &m.WebhookError, &m.WebhookAttempts, &m.ToRecipients, &m.CC, &m.BCC, &m.ConversationID, &m.SizeBytes); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// GetMessagesByAgent returns messages for an agent, filtered by status and
// direction.
//
//   - direction: "inbound" (default for SDK polling), "outbound", or "all"
//     (used by the dashboard inbox).
//   - status: "unread" | "read" | "all" — only applies when direction
//     selects inbound rows; ignored on pure outbound queries.
//   - descending: cursor walks newest→oldest when true (dashboard inbox);
//     oldest→newest when false (SDK polling — agents process the oldest
//     unread message first).
//
// The SELECT includes columns both consumers need: the inbox needs
// `status` (outbound HITL lifecycle), `webhook_status`/`last_error`
// (outbound delivery), and `octet_length(raw_message)` (size column);
// the polling SDK ignores these fields and reads only the existing
// inbound-relevant ones from the Message struct.
func (s *Store) GetMessagesByAgent(
	ctx context.Context,
	agentID, status, direction string,
	descending bool,
	limit int,
	afterTime time.Time,
	afterID string,
) ([]Message, error) {
	var query string
	var args []interface{}

	baseSelect := `SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient, m.to_recipients, m.cc, m.reply_to, m.subject, m.email_message_id, m.conversation_id, COALESCE(m.inbox_status, ''), COALESCE(m.status, ''), COALESCE(wd.status, ''), COALESCE(wd.last_error, ''), COALESCE(octet_length(m.raw_message), 0), m.created_at
		 FROM messages m
		 LEFT JOIN webhook_deliveries wd ON wd.message_id = m.id
		 WHERE m.agent_id = $1 AND m.expires_at > now()`

	switch direction {
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
	if direction != "outbound" {
		switch status {
		case "all":
			// no extra clause
		case "read":
			query += ` AND m.inbox_status = 'read'`
		default: // "unread"
			if direction == "inbound" {
				query += ` AND m.inbox_status = 'unread'`
			}
			// For direction='all', "unread" would silently drop every
			// outbound row (they have no inbox_status). That's a footgun
			// the dashboard never invokes — it always passes status="all"
			// when direction="all" — so we don't filter here.
		}
	}

	args = append(args, agentID)

	cursorCmp := ">"
	sortDir := "ASC"
	if descending {
		cursorCmp = "<"
		sortDir = "DESC"
	}

	if afterID != "" {
		query += fmt.Sprintf(` AND (m.created_at, m.id) %s ($%d, $%d)`, cursorCmp, len(args)+1, len(args)+2)
		args = append(args, afterTime, afterID)
	}

	query += fmt.Sprintf(` ORDER BY m.created_at %s, m.id %s LIMIT $%d`, sortDir, sortDir, len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo,
			&m.Subject, &m.EmailMessageID, &m.ConversationID,
			&m.InboxStatus, &m.Status, &m.WebhookStatus, &m.WebhookError, &m.SizeBytes,
			&m.CreatedAt,
		); err != nil {
			return nil, err
		}
		// Keep DeliveryStatus populated for back-compat — the polling SDK
		// path scans it under the old JSON key.
		m.DeliveryStatus = m.InboxStatus
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// GetMessageWithContent returns a full message including raw_message and auth_headers.
// Marks the message as 'read' if it was 'unread'.
func (s *Store) GetMessageWithContent(ctx context.Context, messageID, agentID string) (*Message, error) {
	m := &Message{}
	var authHeadersJSON []byte
	err := s.pool.QueryRow(ctx,
		`UPDATE messages SET inbox_status = CASE WHEN inbox_status = 'unread' THEN 'read' ELSE inbox_status END
		 WHERE id = $1 AND agent_id = $2 AND expires_at > now()
		 RETURNING id, agent_id, direction, sender, recipient, to_recipients, cc, reply_to, subject, email_message_id, conversation_id, COALESCE(inbox_status, ''), raw_message, auth_headers, created_at, expires_at`,
		messageID, agentID,
	).Scan(&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient, &m.ToRecipients, &m.CC, &m.ReplyTo, &m.Subject, &m.EmailMessageID, &m.ConversationID, &m.DeliveryStatus, &m.RawMessage, &authHeadersJSON, &m.CreatedAt, &m.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if authHeadersJSON != nil {
		if err := json.Unmarshal(authHeadersJSON, &m.AuthHeaders); err != nil {
			return nil, fmt.Errorf("unmarshal auth headers: %w", err)
		}
	}
	return m, nil
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
	token := generateAPIKey() // reuse for randomness
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

type APIKey struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	Name         string     `json:"name"`
	KeyPrefix    string     `json:"key_prefix"`
	PlaintextKey string     `json:"key,omitempty"` // only set once at creation, never stored
	CreatedAt    time.Time  `json:"created_at"`
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

// CreateAPIKey issues a fresh API key for the user. expiresAt is the
// optional hard expiration; pass nil to issue a never-expiring key (the
// backward-compatible default).
func (s *Store) CreateAPIKey(ctx context.Context, userID, name string, expiresAt *time.Time) (*APIKey, error) {
	id := "apk_" + generateID()
	plaintext := generateAPIKey()
	keyHash := hashAPIKey(plaintext)
	// Show first 8 chars as prefix (e.g. "e2a_abcd...")
	prefix := plaintext[:12]
	now := time.Now()
	ak := &APIKey{
		ID:           id,
		UserID:       userID,
		Name:         name,
		KeyPrefix:    prefix,
		PlaintextKey: plaintext,
		CreatedAt:    now,
		ExpiresAt:    expiresAt,
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO api_keys (id, user_id, name, key_prefix, key_hash, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ak.ID, ak.UserID, ak.Name, ak.KeyPrefix, keyHash, ak.CreatedAt, ak.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}
	return ak, nil
}

func (s *Store) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, key_prefix, created_at, last_used_at, expires_at FROM api_keys WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.KeyPrefix, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt); err != nil {
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
	keyHash := hashAPIKey(apiKey)
	u := &User{}
	err := s.pool.QueryRow(ctx,
		`WITH touched AS (
		   UPDATE api_keys SET last_used_at = now()
		   WHERE key_hash = $1
		     AND revoked_at IS NULL
		     AND (expires_at IS NULL OR expires_at > now())
		   RETURNING user_id
		 )
		 SELECT u.id, u.email, u.name, u.google_subject, u.created_at
		 FROM touched t JOIN users u ON u.id = t.user_id`, keyHash,
	).Scan(&u.ID, &u.Email, &u.Name, &u.GoogleSubject, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
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

func generateAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Same reasoning as generateID — an all-zero API key would be
		// catastrophic (predictable auth credential).
		panic(fmt.Sprintf("identity: crypto/rand failed: %v", err))
	}
	return "e2a_" + hex.EncodeToString(b)
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
	ErrSigningSecretCapReached     = fmt.Errorf("at most %d signing secrets per user; delete one before creating another", MaxSigningSecretsPerUser)
	ErrCannotDeleteLastSigningSecret = errors.New("cannot delete the last signing secret; create a new one first")
	ErrSigningSecretNotFound       = errors.New("signing secret not found or not owned by user")
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
