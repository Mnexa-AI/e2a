package oauth

import (
	"context"
	"fmt"
	"time"
)

// RetentionResult is the per-table row count from a single retention
// pass. Surfaced for the operator log line so volume changes are
// visible without parsing the SQL.
type RetentionResult struct {
	AuthCodesDeleted     int64
	AccessTokensDeleted  int64
	RefreshTokensDeleted int64
	PKCERequestsDeleted  int64
}

// Total reports the sum across tables. Useful for the "nothing to do"
// branch in the cleanup loop where the operator log is suppressed.
func (r RetentionResult) Total() int64 {
	return r.AuthCodesDeleted + r.AccessTokensDeleted + r.RefreshTokensDeleted + r.PKCERequestsDeleted
}

// CleanupExpired removes rows that no longer affect any live grant:
//
//   - oauth_auth_codes whose expires_at is in the past — codes are
//     single-use 60s lifetime; an expired row will never be redeemed.
//   - oauth_pkce_requests past expires_at — paired with the auth code
//     by request_id; on its own lifecycle but the same idea.
//   - oauth_access_tokens whose expires_at is past AND that have been
//     dead long enough to no longer matter for forensics (24h grace
//     window — short because access tokens are cheap and there's an
//     audit trail on the refresh chain).
//   - oauth_refresh_tokens whose expires_at is past, OR that were
//     revoked more than the refresh grace ago (30d — same as the
//     refresh lifetime so a revoked row stays around long enough for
//     an operator to grep "who revoked this connection and when").
//
// Non-revoked-but-active refresh tokens are left alone regardless of
// age — operators who set RefreshTokenLifespan=-1 (the never-expires
// mode) depend on this.
//
// Idempotent and safe to run concurrently with normal traffic: every
// DELETE is filtered by absolute timestamps, so two passes from two
// workers just race to delete the same already-stale rows.
func (s *Storage) CleanupExpired(ctx context.Context, now time.Time) (RetentionResult, error) {
	var res RetentionResult

	type step struct {
		dst   *int64
		query string
		arg   time.Time
	}
	const (
		accessGrace  = 24 * time.Hour
		refreshGrace = 30 * 24 * time.Hour
	)

	steps := []step{
		{&res.AuthCodesDeleted,
			`DELETE FROM oauth_auth_codes WHERE expires_at < $1`,
			now},
		{&res.PKCERequestsDeleted,
			`DELETE FROM oauth_pkce_requests WHERE expires_at < $1`,
			now},
		{&res.AccessTokensDeleted,
			`DELETE FROM oauth_access_tokens
			  WHERE expires_at < $1
			    AND (revoked_at IS NULL OR revoked_at < $1)`,
			now.Add(-accessGrace)},
		{&res.RefreshTokensDeleted,
			`DELETE FROM oauth_refresh_tokens
			  WHERE (expires_at IS NOT NULL AND expires_at < $1)
			     OR (revoked_at IS NOT NULL AND revoked_at < $1)`,
			now.Add(-refreshGrace)},
	}

	for _, st := range steps {
		tag, err := s.pool.Exec(ctx, st.query, st.arg)
		if err != nil {
			return res, fmt.Errorf("oauth cleanup: %w", err)
		}
		*st.dst = tag.RowsAffected()
	}
	return res, nil
}

// ConnectionEntry is one row of the user-export "OAuth connections"
// section. Represents a single MCP/native client the user has linked
// to one of their agents. The signature columns themselves stay
// internal — they are credential-equivalent (a leaked signature lets
// an attacker reconstruct the bearer through fosite's strategy).
type ConnectionEntry struct {
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	AgentEmail string     `json:"agent_email"`
	Scope      string     `json:"scope"`
	IssuedAt   time.Time  `json:"issued_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// ExportConnectionsForUser returns one row per refresh-token grant —
// the unit of "I authorized this client" from the user's perspective.
// Access tokens come and go via refresh rotation; the refresh row is
// the durable handle. The session JSONB carries the agent_email we
// pinned at consent time, so we extract it via a JSONB path expression
// rather than rejoining the agent table.
func (s *Storage) ExportConnectionsForUser(ctx context.Context, userID string) ([]ConnectionEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
		    rt.client_id,
		    c.client_name,
		    COALESCE(rt.request->'session'->>'AgentEmail', ''),
		    array_to_string(c.scopes, ' '),
		    rt.created_at,
		    rt.expires_at,
		    rt.revoked_at
		  FROM oauth_refresh_tokens rt
		  JOIN oauth_clients c ON c.client_id = rt.client_id
		 WHERE rt.user_id = $1
		 ORDER BY rt.created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("oauth export: query connections: %w", err)
	}
	defer rows.Close()

	var out []ConnectionEntry
	for rows.Next() {
		var e ConnectionEntry
		if err := rows.Scan(&e.ClientID, &e.ClientName, &e.AgentEmail, &e.Scope,
			&e.IssuedAt, &e.ExpiresAt, &e.RevokedAt); err != nil {
			return nil, fmt.Errorf("oauth export: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountUserOAuthRows returns the per-table row counts for a user.
// Used by DeleteUserData so operators can attest to what CASCADE
// removed when a user is wiped. Cheaper than a post-delete diff
// because we can run it inside the same SERIALIZABLE transaction.
type UserRowCounts struct {
	AuthCodes     int64
	AccessTokens  int64
	RefreshTokens int64
}

func (s *Storage) CountUserOAuthRows(ctx context.Context, userID string) (UserRowCounts, error) {
	var c UserRowCounts
	q := s.pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM oauth_auth_codes      WHERE user_id = $1),
		    (SELECT count(*) FROM oauth_access_tokens   WHERE user_id = $1),
		    (SELECT count(*) FROM oauth_refresh_tokens  WHERE user_id = $1)
	`, userID)
	if err := q.Scan(&c.AuthCodes, &c.AccessTokens, &c.RefreshTokens); err != nil {
		return c, fmt.Errorf("oauth count: %w", err)
	}
	return c, nil
}
