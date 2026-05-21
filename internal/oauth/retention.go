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
	ClientsDeleted       int64
}

// Total reports the sum across tables. Useful for the "nothing to do"
// branch in the cleanup loop where the operator log is suppressed.
func (r RetentionResult) Total() int64 {
	return r.AuthCodesDeleted + r.AccessTokensDeleted + r.RefreshTokensDeleted +
		r.PKCERequestsDeleted + r.ClientsDeleted
}

// CleanupExpired removes rows that no longer affect any live grant:
//
//   - oauth_auth_codes whose expires_at is in the past — codes are
//     single-use 60s lifetime; an expired row will never be redeemed.
//   - oauth_pkce_requests past expires_at — paired with the auth code
//     by request_id; on its own lifecycle but the same idea.
//   - oauth_access_tokens that are revoked OR expired AND old enough
//     that no operator would still be looking at them (24h grace —
//     access tokens are cheap to lose and the audit trail lives on
//     the refresh chain anyway).
//   - oauth_refresh_tokens that have been dead for more than the
//     refresh grace (30d). "Dead" = expires_at in the past OR
//     revoked_at set. The same 30d cutoff applies to both branches —
//     an expired-but-never-revoked token lingers exactly as long as
//     a revoked one. Operators looking at "why did this connection
//     stop working last week" have 30d to inspect.
//   - oauth_clients created via DCR (anonymous), older than 90d, with
//     no remaining access/refresh tokens. RFC 7591 §4 explicitly
//     allows expiring open-registration clients; without this an
//     attacker could fill the table by registering through a /64
//     IPv6 prefix over time. Operator-curated clients
//     (created_via != 'dcr') are never touched.
//
// Non-revoked, non-expired (or never-expires NULL expires_at) refresh
// tokens are left alone regardless of age — operators who set
// RefreshTokenLifespan=-1 depend on this.
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
		clientGrace  = 90 * 24 * time.Hour
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

	// DCR-client pruning: 90d after creation, with no surviving tokens.
	// The NOT EXISTS guards against deleting a client mid-refresh.
	// Operator-curated clients (created_via != 'dcr') are exempt — we
	// only ever drop anonymous-registered rows. Done as a separate
	// step (not in the loop above) because the WHERE shape is uniquely
	// existential and the time arg differs.
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM oauth_clients
		 WHERE created_via = 'dcr'
		   AND created_at < $1
		   AND NOT EXISTS (
		         SELECT 1 FROM oauth_access_tokens   WHERE client_id = oauth_clients.client_id)
		   AND NOT EXISTS (
		         SELECT 1 FROM oauth_refresh_tokens  WHERE client_id = oauth_clients.client_id)
		   AND NOT EXISTS (
		         SELECT 1 FROM oauth_auth_codes      WHERE client_id = oauth_clients.client_id)
	`, now.Add(-clientGrace))
	if err != nil {
		return res, fmt.Errorf("oauth cleanup: clients: %w", err)
	}
	res.ClientsDeleted = tag.RowsAffected()

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

// ExportConnectionsForUser returns one row per CONSENT — the unit of
// "I authorized this client to use this agent" from the user's
// perspective. Refresh tokens rotate (potentially daily) and within
// the 30-day grace each rotation leaves a separate row; a row-per-
// refresh export would dump dozens of duplicate entries for one
// consent act, which is misleading for a GDPR Art. 15 deliverable
// (EDPB Guidelines 01/2022 §82-86: data must be in an "intelligible
// form"). We aggregate by (client_id, agent_email) — the natural
// grouping the user reasoned about at the consent screen.
//
// Per group we report:
//   - IssuedAt: the earliest rotation's created_at — when the user
//     first authorized this client+agent pair
//   - ExpiresAt: the latest rotation's expires_at — when access goes
//     away absent another refresh
//   - RevokedAt: latest revoked_at if EVERY row in the group is
//     revoked, else NULL — a half-revoked group is still effectively
//     active because at least one refresh row can still hand out
//     access tokens
//
// The session JSONB carries the agent_email we pinned at consent
// time, so we extract it via a JSONB path expression rather than
// rejoining the agent table.
func (s *Storage) ExportConnectionsForUser(ctx context.Context, userID string) ([]ConnectionEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
		    rt.client_id,
		    c.client_name,
		    COALESCE(rt.request->'session'->>'AgentEmail', '')              AS agent_email,
		    array_to_string(c.scopes, ' ')                                  AS scope,
		    MIN(rt.created_at)                                              AS issued_at,
		    MAX(rt.expires_at)                                              AS expires_at,
		    CASE WHEN bool_and(rt.revoked_at IS NOT NULL)
		         THEN MAX(rt.revoked_at)
		         ELSE NULL
		    END                                                             AS revoked_at
		  FROM oauth_refresh_tokens rt
		  JOIN oauth_clients c ON c.client_id = rt.client_id
		 WHERE rt.user_id = $1
		 GROUP BY rt.client_id, c.client_name, agent_email, c.scopes
		 ORDER BY issued_at
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
