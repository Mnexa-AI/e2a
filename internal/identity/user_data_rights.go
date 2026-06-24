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
	Domains          []Domain               `json:"domains" nullable:"false"`
	Agents           []AgentIdentity        `json:"agents" nullable:"false"`
	APIKeys          []APIKeyExportEntry    `json:"api_keys" nullable:"false"`
	Messages         []Message                    `json:"messages" nullable:"false"`
	Suppressions     []SuppressionExportEntry     `json:"suppressions" nullable:"false"`
	ProtectionEvents []ProtectionEventExportEntry `json:"protection_events" nullable:"false"`
	UsageEvents      []UsageEventEntry            `json:"usage_events,omitempty" nullable:"false"`
	OAuthConnections []OAuthConnectionEntry       `json:"oauth_connections,omitempty" nullable:"false"`
} // @name UserExport

// SuppressionExportEntry is one suppressed recipient address the account owns.
// source_message_id is an internal correlation id, deliberately omitted.
type SuppressionExportEntry struct {
	Address   string    `json:"address"`
	Reason    string    `json:"reason,omitempty"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
} // @name SuppressionExportEntry

// ProtectionEventExportEntry is one row of the protection_events audit log for
// the user's agents — a metadata projection. The provider-forensics `raw` blob
// and the `spans`/`categories` detector internals are omitted (internal, noisy,
// and not user-meaningful); the disposition (source/reason/action) and the
// counterparty address that tripped a gate are included.
type ProtectionEventExportEntry struct {
	ID          string    `json:"id"`
	MessageID   string    `json:"message_id"`
	AgentID     string    `json:"agent_id"`
	Direction   string    `json:"direction"`
	Source      string    `json:"source"`
	Reason      string    `json:"reason"`
	Action      string    `json:"action"`
	SubjectAddr string    `json:"subject_addr,omitempty"`
	Detector    string    `json:"detector,omitempty"`
	Score       *float64  `json:"score,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
} // @name ProtectionEventExportEntry

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

	// Suppressions (recipient addresses the account suppressed)
	suppressions, err := scanSuppressionsForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load suppressions: %w", err)
	}

	// Protection events (the screening audit log across the user's agents)
	protectionEvents, err := scanProtectionEventsForUser(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("export: load protection events: %w", err)
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
		GeneratedAt:      time.Now().UTC(),
		SchemaVersion:    "2",
		User:             u,
		Domains:          domains,
		Agents:           agents,
		APIKeys:          keys,
		Messages:         messages,
		Suppressions:     suppressions,
		ProtectionEvents: protectionEvents,
		UsageEvents:      events,
	}, nil
}

// scanSuppressionsForUser loads the user's suppression list for the export.
func scanSuppressionsForUser(ctx context.Context, tx pgx.Tx, userID string) ([]SuppressionExportEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT address, COALESCE(reason, ''), source, created_at
		   FROM suppressions WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SuppressionExportEntry{}
	for rows.Next() {
		var e SuppressionExportEntry
		if err := rows.Scan(&e.Address, &e.Reason, &e.Source, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// scanProtectionEventsForUser loads the screening audit log across all the user's
// agents (metadata projection — see ProtectionEventExportEntry).
func scanProtectionEventsForUser(ctx context.Context, tx pgx.Tx, userID string) ([]ProtectionEventExportEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT pe.id, pe.message_id, pe.agent_id, pe.direction, pe.source, pe.reason,
		        pe.action, COALESCE(pe.subject_addr, ''), COALESCE(pe.detector, ''), pe.score, pe.created_at
		   FROM protection_events pe
		   JOIN agent_identities a ON a.id = pe.agent_id
		  WHERE a.user_id = $1
		  ORDER BY pe.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProtectionEventExportEntry{}
	for rows.Next() {
		var e ProtectionEventExportEntry
		if err := rows.Scan(&e.ID, &e.MessageID, &e.AgentID, &e.Direction, &e.Source, &e.Reason,
			&e.Action, &e.SubjectAddr, &e.Detector, &e.Score, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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

// DeleteUserData wipes a user's identity and tears down only the workspaces
// they solely own — see DeleteUserDataTx for the full workspace-aware contract.
func (s *Store) DeleteUserData(ctx context.Context, userID string) (*DeleteUserDataResult, error) {
	return s.DeleteUserDataTx(ctx, userID, nil)
}

// DeleteUserDataTx deletes a user's identity in a single Serializable
// transaction, made workspace-aware for the multi-user model (§5, blockers
// B1/B2). It is NOT a blanket cascade through users any more — a user no
// longer owns workspace resources directly, so deleting them must not nuke a
// teammate's shared agents/domains/keys.
//
// The flow (§5):
//
//  1. Identity-owned credentials die with the human: user_sessions and the
//     oauth_* tables still ON DELETE CASCADE from users, so the final
//     DELETE FROM users revokes the user's live OAuth/MCP tokens (the
//     GDPR/security requirement — a deleted human keeps no working bearer).
//
//  2. For every workspace the user belongs to that STILL has other members,
//     detach rather than delete: re-attribute the user's workspace-owned rows
//     to a surviving admin (so the user-cascade, still live pre-Migration B,
//     leaves them) and remove the user's membership. Shared resources of a
//     multi-member workspace are never deleted.
//     Fails closed (ErrSoleAdminWorkspace) if the user is the sole admin of
//     such a workspace — promote another member first.
//
//  3. The user's solo workspaces (every member count == 1, including their
//     deterministic default) are torn down: the per-domain SES deprovision
//     hook runs for their domains, then the user-cascade removes the
//     workspace-owned rows and the now-orphaned workspace rows are deleted
//     (their memberships/invitations/audit_log cascade via workspace_id).
//
//  4. usage_events is ON DELETE SET NULL (analytics survives a user delete by
//     default), so its rows are not auto-removed; we delete only the rows that
//     belong to a torn-down (solo) workspace — shared usage of a surviving
//     multi-member workspace is left intact (tenant-aware, §5).
//
// perDomainInTx is the optional SES deprovision hook; it runs ONLY for domains
// of torn-down (solo) workspaces (§5) — never for a surviving multi-member
// workspace's domains, which a bare FK cascade would have wrongly orphaned.
// A nil hook is a plain delete (dev / no SES).
//
// Per-table counts are returned for audit/compliance attestation.
func (s *Store) DeleteUserDataTx(ctx context.Context, userID string, perDomainInTx func(ctx context.Context, tx pgx.Tx, domain string) error) (*DeleteUserDataResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("delete: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	res := &DeleteUserDataResult{}

	// Classify every workspace the user belongs to: solo (only member → tear
	// down) vs multi-member (detach). Lock each workspace row FOR UPDATE so the
	// member-count read is write-skew-safe against a concurrent leave/remove
	// (the same shared-row lock the membership mutators take, §5 B1). Reading
	// the user's own membership role in the same pass gives the sole-admin guard.
	rows, err := tx.Query(ctx,
		`SELECT w.id, m.role,
		        (SELECT count(*) FROM workspace_members wm WHERE wm.workspace_id = w.id) AS member_count,
		        (SELECT count(*) FROM workspace_members wm WHERE wm.workspace_id = w.id AND wm.role = 'admin') AS admin_count
		   FROM workspace_members m
		   JOIN workspaces w ON w.id = m.workspace_id
		  WHERE m.user_id = $1
		  ORDER BY w.id
		  FOR UPDATE OF w`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("delete: classify workspaces: %w", err)
	}
	type wsClass struct {
		id   string
		solo bool
	}
	var workspaces []wsClass
	for rows.Next() {
		var (
			id          string
			role        string
			memberCount int
			adminCount  int
		)
		if err := rows.Scan(&id, &role, &memberCount, &adminCount); err != nil {
			rows.Close()
			return nil, fmt.Errorf("delete: scan workspace class: %w", err)
		}
		// ws_system is a protected sentinel — never torn down (§5). A user
		// should never be a member of it, but guard defensively.
		if id == SystemWorkspaceID {
			continue
		}
		solo := memberCount <= 1
		if !solo && role == RoleAdmin && adminCount <= 1 {
			rows.Close()
			return nil, ErrSoleAdminWorkspace
		}
		workspaces = append(workspaces, wsClass{id: id, solo: solo})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("delete: classify workspaces: %w", err)
	}
	rows.Close()

	// Count first so the result is informative even when CASCADE handles the
	// actual delete. Scoped to the user (audit attestation of what this human
	// owned/operated), not to the surviving workspaces.
	queries := []struct {
		dst   *int64
		query string
	}{
		{&res.SessionsDeleted, `SELECT count(*) FROM user_sessions WHERE user_id = $1`},
		{&res.DomainsDeleted, `SELECT count(*) FROM domains WHERE user_id = $1`},
		{&res.AgentsDeleted, `SELECT count(*) FROM agent_identities WHERE user_id = $1`},
		{&res.APIKeysDeleted, `SELECT count(*) FROM api_keys WHERE user_id = $1`},
		{&res.MessagesDeleted, `SELECT count(*) FROM messages m JOIN agent_identities a ON a.id = m.agent_id WHERE a.user_id = $1`},
		{&res.UsageSummariesDeleted, `SELECT count(*) FROM usage_summaries WHERE user_id = $1`},
	}
	for _, q := range queries {
		if err := tx.QueryRow(ctx, q.query, userID).Scan(q.dst); err != nil {
			return nil, fmt.Errorf("delete: count: %w", err)
		}
	}

	// (2) Multi-member workspaces: detach. Re-attribute the user's
	// workspace-owned rows to a surviving admin so the still-live user-cascade
	// (Migration B deferred) leaves the shared resources intact, then drop the
	// user's membership. NULL-ing user_id would violate the (still NOT NULL)
	// FKs on several owned tables in deploy-1, so we re-home to a real surviving
	// member rather than detach to NULL.
	for _, w := range workspaces {
		if w.solo {
			continue
		}
		var heir string
		err := tx.QueryRow(ctx,
			`SELECT user_id FROM workspace_members
			  WHERE workspace_id = $1 AND user_id <> $2
			  ORDER BY (role = 'admin') DESC, created_at ASC, user_id ASC
			  LIMIT 1`,
			w.id, userID,
		).Scan(&heir)
		if err != nil {
			return nil, fmt.Errorf("delete: pick heir for %s: %w", w.id, err)
		}
		if err := reattributeWorkspaceRows(ctx, tx, w.id, userID, heir); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
			w.id, userID,
		); err != nil {
			return nil, fmt.Errorf("delete: remove membership in %s: %w", w.id, err)
		}
	}

	// (3) Solo workspaces: run the SES deprovision hook for their domains
	// BEFORE the cascade removes the domain rows. Hook runs only here — never
	// for a surviving multi-member workspace's shared domains (§5).
	if perDomainInTx != nil {
		for _, w := range workspaces {
			if !w.solo {
				continue
			}
			domains, derr := scanDomainsForWorkspace(ctx, tx, w.id)
			if derr != nil {
				return nil, fmt.Errorf("delete: load domains for teardown of %s: %w", w.id, derr)
			}
			for _, d := range domains {
				if err := perDomainInTx(ctx, tx, d); err != nil {
					return nil, fmt.Errorf("delete: enqueue sender teardown for %s: %w", d, err)
				}
			}
		}
	}

	// (3 cont.) Tear down the solo workspaces by workspace_id — the cascade
	// owner in the new model (§4.1). We do NOT rely on the user-cascade here:
	// the account_usage storage trigger writes rows keyed by workspace_id with
	// a NULL user_id, so a user-cascade alone would leave them and the
	// workspace-delete would hit the workspace_id FK. Deleting by workspace_id
	// removes every owned row regardless of its (possibly NULL) user_id.
	// usage_events for these workspaces is also removed here (§5 (4)): it is
	// ON DELETE SET NULL so it would otherwise survive — shared usage of a
	// surviving multi-member workspace is left untouched.
	soloIDs := make([]string, 0, len(workspaces))
	for _, w := range workspaces {
		if !w.solo {
			continue
		}
		soloIDs = append(soloIDs, w.id)
		ue, err := teardownWorkspaceRows(ctx, tx, w.id)
		if err != nil {
			return nil, err
		}
		res.UsageEventsDeleted += ue
	}
	_ = soloIDs

	// (1) Delete the user. user_sessions + oauth_* still cascade from users
	// (identity-owned → the user's live OAuth/MCP tokens are revoked, the
	// GDPR/security requirement). The user's solo workspace-owned rows are
	// already gone (torn down above); the multi-member workspaces' rows were
	// re-homed to a surviving member and survive the user-cascade.
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("delete: users: %w", err)
	}
	res.UserDeleted = tag.RowsAffected() == 1

	// (3 cont.) Remove the now-emptied solo workspace rows (membership,
	// invitations, audit_log cascade via workspace_id ON DELETE CASCADE).
	for _, w := range workspaces {
		if !w.solo {
			continue
		}
		if _, err := tx.Exec(ctx, `DELETE FROM workspaces WHERE id = $1`, w.id); err != nil {
			return nil, fmt.Errorf("delete: tear down workspace %s: %w", w.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("delete: commit: %w", err)
	}
	return res, nil
}

// teardownWorkspaceRows deletes every workspace-owned row for a torn-down
// (solo) workspace, by workspace_id — the cascade owner in the new model.
// Order respects the FKs: api_keys (agent_id → agent_identities) and
// agent_identities (cascades messages / send_attempts / protection_events)
// before domains (agent_identities.domain → domains ON DELETE NO ACTION).
// Returns the usage_events row count removed (for the audit result). The
// workspace row itself is deleted by the caller after the user delete so its
// membership/invitation/audit_log rows cascade cleanly.
func teardownWorkspaceRows(ctx context.Context, tx pgx.Tx, workspaceID string) (usageEventsDeleted int64, err error) {
	// usage_events is ON DELETE SET NULL (no cascade) — delete explicitly and
	// report the count.
	tag, err := tx.Exec(ctx, `DELETE FROM usage_events WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("teardown %s: usage_events: %w", workspaceID, err)
	}
	usageEventsDeleted = tag.RowsAffected()

	// Ordered deletes. api_keys before agent_identities (FK), agent_identities
	// before domains (NO ACTION FK from agent_identities.domain).
	for _, q := range []string{
		`DELETE FROM api_keys                WHERE workspace_id = $1`,
		`DELETE FROM agent_identities        WHERE workspace_id = $1`,
		`DELETE FROM domains                 WHERE workspace_id = $1`,
		`DELETE FROM suppressions            WHERE workspace_id = $1`,
		`DELETE FROM webhook_events          WHERE workspace_id = $1`,
		`DELETE FROM webhooks                WHERE workspace_id = $1`,
		`DELETE FROM webhook_signing_secrets WHERE workspace_id = $1`,
		`DELETE FROM idempotency_keys        WHERE workspace_id = $1`,
		`DELETE FROM usage_summaries         WHERE workspace_id = $1`,
		`DELETE FROM account_usage           WHERE workspace_id = $1`,
		`DELETE FROM account_limits          WHERE workspace_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, workspaceID); err != nil {
			return usageEventsDeleted, fmt.Errorf("teardown %s: %w", workspaceID, err)
		}
	}
	return usageEventsDeleted, nil
}

// reattributeWorkspaceRows re-homes every workspace-owned row currently
// attributed to fromUser within workspaceID to toUser, so that deleting
// fromUser (whose user_id ON DELETE CASCADE is still live pre-Migration B)
// does not take a surviving multi-member workspace's shared resources (§5,
// B1/B2). It touches exactly the workspace-owned tables that carry a user_id
// audit column; identity-owned tables (sessions, oauth_*) are untouched.
func reattributeWorkspaceRows(ctx context.Context, tx pgx.Tx, workspaceID, fromUser, toUser string) error {
	// Each statement is scoped to the workspace AND the leaving user so it
	// never touches another tenant's rows. account_limits / account_usage are
	// one-per-workspace (PK flipped to workspace_id) so re-attribution is just
	// an audit-column update.
	stmts := []string{
		`UPDATE domains                 SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE agent_identities        SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE api_keys                SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE suppressions            SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE webhooks                SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE webhook_events          SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE webhook_signing_secrets SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE account_limits          SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE account_usage           SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE usage_summaries         SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		`UPDATE usage_events            SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
		// idempotency_keys PK was flipped to (workspace_id, key); re-attribute
		// its audit user_id too so the cascade leaves the workspace's dedup rows.
		`UPDATE idempotency_keys        SET user_id = $3 WHERE workspace_id = $1 AND user_id = $2`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q, workspaceID, fromUser, toUser); err != nil {
			return fmt.Errorf("delete: reattribute rows in %s: %w", workspaceID, err)
		}
	}
	return nil
}

// scanDomainsForWorkspace returns the domain names owned by a workspace, used
// to run the SES deprovision hook for a torn-down (solo) workspace (§5).
func scanDomainsForWorkspace(ctx context.Context, tx pgx.Tx, workspaceID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT domain FROM domains WHERE workspace_id = $1 ORDER BY domain`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
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
		        hitl_ttl_seconds, hitl_expiration_action,
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
			&a.HITLTTLSeconds, &a.HITLExpirationAction,
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
