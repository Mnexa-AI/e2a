package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UserExport is the structured dump returned by ExportUserData. It's
// designed to be a complete, machine-readable record of everything a
// single user owns in the system — the right-of-access counterpart to
// DeleteUserData below. Sensitive secrets are deliberately excluded:
//
//   - API key plaintexts are not stored anywhere (only hashes), so
//     they can't be exported even in principle.
//   - google_subject is an internal OAuth identifier with no value to
//     the user and is omitted on purpose.
//   - User session tokens are transient and excluded.
type UserExport struct {
	GeneratedAt      time.Time              `json:"generated_at"`
	SchemaVersion    string                 `json:"schema_version"`
	User             UserExportUser         `json:"user"`
	Domains          []Domain               `json:"domains"`
	Agents           []AgentIdentity        `json:"agents"`
	APIKeys          []APIKeyExportEntry    `json:"api_keys"`
	Messages         []Message              `json:"messages"`
	UsageEvents      []UsageEventEntry      `json:"usage_events,omitempty"`
	OAuthConnections []OAuthConnectionEntry `json:"oauth_connections,omitempty"`
} // @name UserExport

// OAuthConnectionEntry is one OAuth/MCP client connection. The
// underlying token signatures are intentionally excluded — they are
// credential-equivalent. The agent_email is the per-grant binding
// captured at consent time.
type OAuthConnectionEntry struct {
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	AgentEmail string     `json:"agent_email"`
	Scope      string     `json:"scope"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
} // @name OAuthConnectionEntry

// UserExportUser mirrors User but omits the google_subject internal
// identifier from the export payload.
type UserExportUser struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
} // @name UserExportUser

// APIKeyExportEntry is the API-key shape included in the export. We
// expose only metadata; the hash itself stays internal because (a) it's
// not useful to the user, (b) it's a credential equivalent for offline
// dictionary attacks if leaked.
type APIKeyExportEntry struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
} // @name APIKeyExportEntry

// UsageEventEntry is one row of the usage_events table for the user.
type UsageEventEntry struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Domain    string    `json:"domain"`
	Direction string    `json:"direction"`
	EventType string    `json:"event_type"`
	CreatedAt time.Time `json:"created_at"`
} // @name UsageEventEntry

// ExportUserData gathers everything a user owns into a single struct
// for the right-of-access flow. Reads run inside a REPEATABLE READ
// transaction so the snapshot is internally consistent even if writes
// arrive while the export is being assembled.
func (s *Store) ExportUserData(ctx context.Context, userID string) (*UserExport, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("export: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Profile
	var u UserExportUser
	err = tx.QueryRow(ctx,
		`SELECT id, email, name, created_at FROM users WHERE id = $1`, userID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("export: load user: %w", err)
	}

	// Domains
	domains, err := scanDomainsForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load domains: %w", err)
	}

	// Agents
	agents, err := scanAgentsForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load agents: %w", err)
	}

	// API keys (metadata only)
	keys, err := scanAPIKeysForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load api keys: %w", err)
	}

	// Messages — across all the user's agents in one query so the order
	// is consistent and pagination doesn't matter for the export.
	messages, err := scanMessagesForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load messages: %w", err)
	}

	// Usage events (only present if E2A_USAGE_TRACKING is on)
	events, err := scanUsageEventsForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load usage events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("export: commit: %w", err)
	}

	return &UserExport{
		GeneratedAt:   time.Now().UTC(),
		SchemaVersion: "1",
		User:          u,
		Domains:       domains,
		Agents:        agents,
		APIKeys:       keys,
		Messages:      messages,
		UsageEvents:   events,
	}, nil
}

// DeleteUserDataResult breaks out per-table row counts for audit logs.
// Operators receiving a deletion request often have to attest to what
// was removed; returning structured counts beats parsing a log line.
type DeleteUserDataResult struct {
	UsageEventsDeleted        int64 `json:"usage_events_deleted"`
	UsageSummariesDeleted     int64 `json:"usage_summaries_deleted"`
	MessagesDeleted           int64 `json:"messages_deleted"`
	AgentsDeleted             int64 `json:"agents_deleted"`
	DomainsDeleted            int64 `json:"domains_deleted"`
	APIKeysDeleted            int64 `json:"api_keys_deleted"`
	SessionsDeleted           int64 `json:"sessions_deleted"`
	OAuthAuthCodesDeleted     int64 `json:"oauth_auth_codes_deleted,omitempty"`
	OAuthAccessTokensDeleted  int64 `json:"oauth_access_tokens_deleted,omitempty"`
	OAuthRefreshTokensDeleted int64 `json:"oauth_refresh_tokens_deleted,omitempty"`
	UserDeleted               bool  `json:"user_deleted"`
} // @name DeleteUserDataResult

// DeleteUserData wipes everything tied to a user in a single transaction.
//
// Schema cascades cover most of it (user_sessions, domains, agent_identities,
// api_keys, usage_summaries all `ON DELETE CASCADE` from users; messages
// cascade through agent_identities; webhook_deliveries cascade through
// messages). The one row that doesn't is usage_events: its FK is `ON
// DELETE SET NULL` so analytics survives, which we explicitly override
// here for full deletion.
//
// Per-table counts are returned to the caller so an operator can attest
// to what was removed in audit / compliance contexts.
func (s *Store) DeleteUserData(ctx context.Context, userID string) (*DeleteUserDataResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("delete: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	res := &DeleteUserDataResult{}

	// Count first so the result is informative even when CASCADE handles
	// the actual delete. We do a single SELECT per table — much cheaper
	// than counting after-the-fact.
	queries := []struct {
		dst   *int64
		query string
	}{
		{&res.SessionsDeleted, `SELECT count(*) FROM user_sessions WHERE user_id = $1`},
		{&res.DomainsDeleted, `SELECT count(*) FROM domains WHERE user_id = $1`},
		{&res.AgentsDeleted, `SELECT count(*) FROM agent_identities WHERE user_id = $1`},
		{&res.APIKeysDeleted, `SELECT count(*) FROM api_keys WHERE user_id = $1`},
		{&res.MessagesDeleted, `SELECT count(*) FROM messages m JOIN agent_identities a ON a.id = m.agent_id WHERE a.user_id = $1`},
		{&res.UsageEventsDeleted, `SELECT count(*) FROM usage_events WHERE user_id = $1`},
		{&res.UsageSummariesDeleted, `SELECT count(*) FROM usage_summaries WHERE user_id = $1`},
	}
	for _, q := range queries {
		if err := tx.QueryRow(ctx, q.query, userID).Scan(q.dst); err != nil {
			return nil, fmt.Errorf("delete: count: %w", err)
		}
	}

	// Override the SET NULL behavior on usage_events for a complete wipe.
	if _, err := tx.Exec(ctx, `DELETE FROM usage_events WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("delete: usage_events: %w", err)
	}

	// Cascade does the rest: user_sessions, domains, agent_identities
	// (and through them, messages → webhook_deliveries), api_keys,
	// usage_summaries.
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("delete: users: %w", err)
	}
	res.UserDeleted = tag.RowsAffected() == 1

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("delete: commit: %w", err)
	}
	return res, nil
}

// --- internal scan helpers; private since they're only used by the
//     export path. Each takes a pgx.Tx so the caller controls
//     isolation/lifecycle. ---

func scanDomainsForUser(ctx context.Context, tx pgx.Tx, userID string) ([]Domain, error) {
	rows, err := tx.Query(ctx,
		`SELECT domain, user_id, verified, verification_token, created_at, verified_at
		   FROM domains WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Domain, &d.UserID, &d.Verified, &d.VerificationToken, &d.CreatedAt, &d.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanAgentsForUser(ctx context.Context, tx pgx.Tx, userID string) ([]AgentIdentity, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, domain, name,
		        hitl_enabled, hitl_ttl_seconds, hitl_expiration_action,
		        public, created_at, user_id
		   FROM agent_identities WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentIdentity
	for rows.Next() {
		var a AgentIdentity
		if err := rows.Scan(&a.ID, &a.Domain, &a.Name,
			&a.HITLEnabled, &a.HITLTTLSeconds, &a.HITLExpirationAction,
			&a.Public, &a.CreatedAt, &a.UserID); err != nil {
			return nil, err
		}
		// Domain verification flag is on the joined domain row; for
		// the export we don't need it, leave default false.
		a.populateEmail()
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAPIKeysForUser(ctx context.Context, tx pgx.Tx, userID string) ([]APIKeyExportEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, key_prefix, created_at, last_used_at, revoked_at
		   FROM api_keys WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKeyExportEntry
	for rows.Next() {
		var k APIKeyExportEntry
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func scanMessagesForUser(ctx context.Context, tx pgx.Tx, userID string) ([]Message, error) {
	// COALESCE the nullable string columns into '' so we can scan into
	// plain `string` rather than dragging in sql.NullString. Matches the
	// ListActivityByAgent pattern used elsewhere in this package.
	rows, err := tx.Query(ctx,
		`SELECT m.id, m.agent_id, m.direction, m.sender, m.recipient,
		        m.subject, m.email_message_id, m.provider_message_id,
		        COALESCE(m.method, ''), COALESCE(m.message_type, ''),
		        m.raw_message, m.auth_headers, m.conversation_id,
		        COALESCE(m.inbox_status, ''),
		        m.created_at, m.expires_at,
		        m.to_recipients, m.cc, m.bcc, m.reply_to,
		        m.status, m.approval_expires_at, m.reviewed_at,
		        COALESCE(m.rejection_reason, ''),
		        m.edited, COALESCE(m.body_text, ''), COALESCE(m.body_html, ''),
		        m.attachments_json
		   FROM messages m
		   JOIN agent_identities a ON a.id = m.agent_id
		  WHERE a.user_id = $1
		  ORDER BY m.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var authHeadersJSON []byte
		var attachmentsJSON []byte
		if err := rows.Scan(
			&m.ID, &m.AgentID, &m.Direction, &m.Sender, &m.Recipient,
			&m.Subject, &m.EmailMessageID, &m.ProviderMessageID, &m.Method, &m.Type,
			&m.RawMessage, &authHeadersJSON, &m.ConversationID, &m.DeliveryStatus,
			&m.CreatedAt, &m.ExpiresAt,
			&m.ToRecipients, &m.CC, &m.BCC, &m.ReplyTo,
			&m.Status, &m.ApprovalExpiresAt, &m.ReviewedAt, &m.RejectionReason,
			&m.Edited, &m.BodyText, &m.BodyHTML, &attachmentsJSON,
		); err != nil {
			return nil, err
		}
		if len(authHeadersJSON) > 0 {
			_ = json.Unmarshal(authHeadersJSON, &m.AuthHeaders)
		}
		if len(attachmentsJSON) > 0 {
			m.AttachmentsJSON = json.RawMessage(attachmentsJSON)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanUsageEventsForUser(ctx context.Context, tx pgx.Tx, userID string) ([]UsageEventEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, agent_id, domain, direction, event_type, created_at
		   FROM usage_events WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageEventEntry
	for rows.Next() {
		var e UsageEventEntry
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Domain, &e.Direction, &e.EventType, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
