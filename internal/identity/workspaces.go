package identity

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SystemWorkspaceID is the protected sentinel workspace seeded by Migration A
// (048). It owns rows with no real user — the shared mail domain and any
// usage_events rows already NULLed by ON DELETE SET NULL. It is guarded
// against teardown (§5) and is never a personal workspace.
const SystemWorkspaceID = "ws_system"

// Roles. The split is team/workspace administration (admin) vs. resource
// operation (member): members run the workspace's infrastructure (agents,
// domains, keys); admins additionally manage people, the workspace lifecycle,
// and billing. The workspace creator is the first admin. Admins are peers —
// there is no super-admin above them. (§4.3)
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// ValidRole reports whether r is a known workspace role.
func ValidRole(r string) bool { return r == RoleAdmin || r == RoleMember }

// Sentinel errors so handlers can map error → HTTP status via errors.Is.
var (
	// ErrWorkspaceNotFound — no workspace with that id.
	ErrWorkspaceNotFound = errors.New("workspace not found")
	// ErrNotMember — the user is not a live member of the workspace.
	ErrNotMember = errors.New("not a member of this workspace")
	// ErrLastAdmin — the operation would leave the workspace with zero
	// admins; fail closed (§5, B1).
	ErrLastAdmin = errors.New("cannot leave/remove/demote the last admin; promote another member first")
	// ErrInvitationNotFound — token resolves to no live pending invitation
	// (torn-down / revoked / expired → 410 gone, fail closed; §4.6).
	ErrInvitationNotFound = errors.New("invitation not found or no longer pending")
	// ErrInvitationEmailMismatch — the authenticated user's normalized email
	// does not match the invitation's email (§4.6 → 403).
	ErrInvitationEmailMismatch = errors.New("invitation is for a different email address")
	// ErrAlreadyMember — invite targets a user who is already a member
	// (§4.6 → 409 already_member; PATCH …/members is the role writer).
	ErrAlreadyMember = errors.New("user is already a member of this workspace")
)

// Workspace is the billed tenant: it owns agents, domains, keys, limits, and
// usage. Individuals own nothing directly — access is membership.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy *string   `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkspaceMember is one (workspace, user) membership row with its role.
type WorkspaceMember struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	InvitedBy   *string   `json:"invited_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	// Email / Name are JOIN'd from users for list rendering; empty on the
	// bare membership read paths.
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// WorkspaceInvitation is a pending/resolved invite to join a workspace. The
// bearer token is never stored — only its SHA-256 hash (cf. api_keys).
type WorkspaceInvitation struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	InvitedBy   *string    `json:"invited_by,omitempty"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	// PlaintextToken is set exactly once at creation (returned to the caller
	// so it can build the accept link) and never persisted.
	PlaintextToken string `json:"token,omitempty"`
}

// DefaultWorkspaceID returns the deterministic id of a user's personal
// (default) workspace. It MUST match Migration A's backfill expression
// ('ws_' || md5(user_id)) so the helper and the migration converge on the
// same row (§4.2 fallthrough, §4.5). Every user always has this workspace.
func DefaultWorkspaceID(userID string) string {
	sum := md5.Sum([]byte(userID))
	return "ws_" + hex.EncodeToString(sum[:])
}

// hashInviteToken hashes an invitation bearer token for storage/lookup.
// Mirrors hashAPIKey — we persist only the hash, never the plaintext.
func hashInviteToken(plaintext string) string { return hashAPIKey(plaintext) }

// generateInviteToken mints a fresh ≥128-bit CSPRNG invitation bearer token
// with the e2a_inv_ prefix (§4.6). Like API keys, the token is matched by
// hash of the full string, so the prefix is cosmetic but legible.
func generateInviteToken() string {
	return "e2a_inv_" + randomHex32()
}

// personalWorkspaceName builds the default name for a user's personal
// workspace: "{name}'s Workspace", falling back to the normalized email
// local-part when name is blank (mirrors Migration A's CASE expression and
// §4.5).
func personalWorkspaceName(name, email string) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return trimmed + "'s Workspace"
	}
	local := NormalizeEmail(email)
	if at := strings.IndexByte(local, '@'); at >= 0 {
		local = local[:at]
	}
	if local == "" {
		local = "My"
	}
	return local + "'s Workspace"
}

// ensurePersonalWorkspace is the single shared helper that provisions a
// user's personal workspace + admin membership inside the caller's
// transaction (blocker B3 — no user-creation path may bypass it). Idempotent:
// the workspace id is deterministic (DefaultWorkspaceID) and both inserts use
// ON CONFLICT DO NOTHING, so a returning login does not double-provision and a
// re-run is a no-op. The user is inserted as admin — full control by
// construction (§4.5).
func ensurePersonalWorkspace(ctx context.Context, tx pgx.Tx, userID, name, email string) (string, error) {
	wsID := DefaultWorkspaceID(userID)
	if _, err := tx.Exec(ctx,
		`INSERT INTO workspaces (id, name, created_by)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO NOTHING`,
		wsID, personalWorkspaceName(name, email), userID,
	); err != nil {
		return "", fmt.Errorf("ensure personal workspace: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		 VALUES ($1, $2, 'admin', $2)
		 ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		wsID, userID,
	); err != nil {
		return "", fmt.Errorf("ensure personal workspace membership: %w", err)
	}
	return wsID, nil
}

// ---------------------------------------------------------------------------
// Workspace reads
// ---------------------------------------------------------------------------

// GetWorkspace returns a workspace by id, or ErrWorkspaceNotFound.
func (s *Store) GetWorkspace(ctx context.Context, id string) (*Workspace, error) {
	w := &Workspace{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, created_by, created_at FROM workspaces WHERE id = $1`, id,
	).Scan(&w.ID, &w.Name, &w.CreatedBy, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return w, nil
}

// ListWorkspacesForUser returns every workspace the user is a live member of,
// each annotated with the caller's role (§4.4 GET /v1/workspaces). The user's
// own default workspace sorts first (created_at ASC), then by join order.
func (s *Store) ListWorkspacesForUser(ctx context.Context, userID string) ([]Workspace, []string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT w.id, w.name, w.created_by, w.created_at, m.role
		   FROM workspace_members m
		   JOIN workspaces w ON w.id = m.workspace_id
		  WHERE m.user_id = $1
		  ORDER BY w.created_at ASC, w.id ASC`,
		userID,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var workspaces []Workspace
	var roles []string
	for rows.Next() {
		var w Workspace
		var role string
		if err := rows.Scan(&w.ID, &w.Name, &w.CreatedBy, &w.CreatedAt, &role); err != nil {
			return nil, nil, err
		}
		workspaces = append(workspaces, w)
		roles = append(roles, role)
	}
	return workspaces, roles, rows.Err()
}

// RenameWorkspace sets a workspace's display name (admin-only at the handler
// layer; this method does not check role). Returns ErrWorkspaceNotFound when
// no workspace matched.
func (s *Store) RenameWorkspace(ctx context.Context, workspaceID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE workspaces SET name = $2 WHERE id = $1`, workspaceID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrWorkspaceNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// Membership
// ---------------------------------------------------------------------------

// ResolveMembership returns the user's role in the workspace, or ErrNotMember
// when no live membership exists. This is the hot session/OAuth authz lookup
// (key auth needs no read — role is constant 'member', workspace intrinsic;
// §6). Backed by idx_workspace_members_user (user_id) INCLUDE (role).
func (s *Store) ResolveMembership(ctx context.Context, userID, workspaceID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotMember
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// ResolveActiveWorkspace implements the §4.2 active-workspace resolution for a
// human session. Given the authenticated user, the (possibly empty)
// X-E2A-Workspace header value, and the session cookie token, it returns the
// resolved workspace and the caller's role in it:
//
//  1. Header present → the user MUST be a live member of it; a non-member id
//     yields ErrNotMember (the handler maps that to 403 — never a silent
//     fallback, §5 header-spoofing).
//  2. Header absent → use the session's last_active_workspace_id, but ONLY
//     after re-verifying live membership; if the user was removed from it (or
//     it was torn down) fall through.
//  3. Fallthrough → the user's deterministic default workspace, which always
//     exists, so resolution never fails or 403s on the no-header path.
//
// On the no-header success path it advances last_active_workspace_id (advisory
// only — never an authz input) conditionally, so steady-state requests do zero
// extra writes (§4.2). sessionToken may be empty (e.g. CLI handoff without a
// cookie); last_active is then simply not tracked.
func (s *Store) ResolveActiveWorkspace(ctx context.Context, userID, headerWorkspaceID, sessionToken string) (*Workspace, string, error) {
	// 1. Header present — membership-verified, never a silent fallback.
	if headerWorkspaceID != "" {
		role, err := s.ResolveMembership(ctx, userID, headerWorkspaceID)
		if err != nil {
			return nil, "", err // ErrNotMember → 403 at the handler
		}
		w, err := s.GetWorkspace(ctx, headerWorkspaceID)
		if err != nil {
			return nil, "", err
		}
		return w, role, nil
	}

	// 2. Header absent — try last_active, re-verifying live membership.
	if sessionToken != "" {
		if last, err := s.sessionLastActiveWorkspace(ctx, sessionToken); err == nil && last != "" {
			if role, mErr := s.ResolveMembership(ctx, userID, last); mErr == nil {
				w, wErr := s.GetWorkspace(ctx, last)
				if wErr == nil {
					return w, role, nil
				}
			}
			// removed from last_active or it was torn down → fall through.
		}
	}

	// 3. Fallthrough — the user's default workspace (always exists).
	defaultID := DefaultWorkspaceID(userID)
	role, err := s.ResolveMembership(ctx, userID, defaultID)
	if err != nil {
		return nil, "", err
	}
	w, err := s.GetWorkspace(ctx, defaultID)
	if err != nil {
		return nil, "", err
	}
	// Advance last_active (advisory only) conditionally, so steady-state
	// requests do zero extra writes.
	if sessionToken != "" {
		_ = s.touchSessionLastActiveWorkspace(ctx, sessionToken, defaultID)
	}
	return w, role, nil
}

// sessionLastActiveWorkspace reads the advisory last_active_workspace_id off a
// live (unexpired) session, or "" when unset / the session is gone.
func (s *Store) sessionLastActiveWorkspace(ctx context.Context, token string) (string, error) {
	var ws *string
	err := s.pool.QueryRow(ctx,
		`SELECT last_active_workspace_id FROM user_sessions
		  WHERE token = $1 AND expires_at > now()`, token,
	).Scan(&ws)
	if err != nil {
		return "", err
	}
	if ws == nil {
		return "", nil
	}
	return *ws, nil
}

// touchSessionLastActiveWorkspace records the advisory active workspace on the
// session, written conditionally (IS DISTINCT FROM) so a steady-state request
// that re-resolves the same workspace does zero writes (§4.2). Advisory only —
// never read as an authz input without a fresh membership re-verification.
func (s *Store) touchSessionLastActiveWorkspace(ctx context.Context, token, workspaceID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE user_sessions SET last_active_workspace_id = $2
		  WHERE token = $1 AND last_active_workspace_id IS DISTINCT FROM $2`,
		token, workspaceID,
	)
	return err
}

// ListMembers returns the workspace's members with their roles, joined to the
// users table for email/name. Ordered admins-first, then by join time (§4.4).
func (s *Store) ListMembers(ctx context.Context, workspaceID string) ([]WorkspaceMember, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m.workspace_id, m.user_id, m.role, m.invited_by, m.created_at,
		        u.email, u.name
		   FROM workspace_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.workspace_id = $1
		  ORDER BY (m.role = 'admin') DESC, m.created_at ASC, m.user_id ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []WorkspaceMember
	for rows.Next() {
		var m WorkspaceMember
		if err := rows.Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt, &m.Email, &m.Name); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// CountAdmins returns the number of admin members in a workspace. Used by the
// last-admin guard; callers that need write-skew safety must call it inside a
// tx that has first locked the workspace row (lockWorkspace / §5 B1).
func (s *Store) CountAdmins(ctx context.Context, workspaceID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'admin'`,
		workspaceID,
	).Scan(&n)
	return n, err
}

// AddMember inserts a membership row directly (used by tests and the internal
// teardown/seed paths). The invite-accept flow uses AcceptInvitation, which
// adds membership transactionally alongside the status flip.
func (s *Store) AddMember(ctx context.Context, workspaceID, userID, role, invitedBy string) error {
	if !ValidRole(role) {
		return fmt.Errorf("invalid role %q", role)
	}
	var inviter *string
	if invitedBy != "" {
		inviter = &invitedBy
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		workspaceID, userID, role, inviter,
	)
	return err
}

// SetMemberRole changes a member's role. Promote/demote is the transfer-admin
// mechanism (admins are peers). Demoting the last admin is refused under the
// shared-row lock (§5, B1): the workspace row is locked FOR UPDATE first, then
// a plain count(*) — serializing concurrent demotes/leaves so two callers
// can't both observe count=2 and both demote. Returns ErrNotMember if the
// target is not a member, ErrLastAdmin if the demote would orphan the
// workspace.
func (s *Store) SetMemberRole(ctx context.Context, workspaceID, userID, newRole string) error {
	if !ValidRole(newRole) {
		return fmt.Errorf("invalid role %q", newRole)
	}
	return s.withWorkspaceLock(ctx, workspaceID, func(tx pgx.Tx) error {
		var cur string
		err := tx.QueryRow(ctx,
			`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
			workspaceID, userID,
		).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return err
		}
		if cur == newRole {
			return nil // idempotent no-op
		}
		// Demoting an admin → member: refuse if they are the last admin.
		if cur == RoleAdmin && newRole == RoleMember {
			n, err := countAdminsTx(ctx, tx, workspaceID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		_, err = tx.Exec(ctx,
			`UPDATE workspace_members SET role = $3 WHERE workspace_id = $1 AND user_id = $2`,
			workspaceID, userID, newRole,
		)
		return err
	})
}

// RemoveMember deletes a membership row (remove, or self = leave). Refuses to
// remove the last admin under the shared-row lock (§5, B1). Returns
// ErrNotMember when the user is not a member, ErrLastAdmin when the removal
// would orphan the workspace of admins. Hard-delete keeps a later re-accept
// INSERT clean (§4.6 step 5).
func (s *Store) RemoveMember(ctx context.Context, workspaceID, userID string) error {
	return s.withWorkspaceLock(ctx, workspaceID, func(tx pgx.Tx) error {
		var role string
		err := tx.QueryRow(ctx,
			`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
			workspaceID, userID,
		).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return err
		}
		if role == RoleAdmin {
			n, err := countAdminsTx(ctx, tx, workspaceID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		_, err = tx.Exec(ctx,
			`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
			workspaceID, userID,
		)
		return err
	})
}

// withWorkspaceLock runs fn inside a transaction that has first taken a row
// lock on the workspace (SELECT id FROM workspaces WHERE id = $1 FOR UPDATE).
// This is the correct last-admin concurrency mechanism (§5, B1): every
// membership-mutating tx serializes on the single shared workspace row, so a
// plain count(*) inside fn is write-skew-safe (the earlier
// "count(*) … FOR UPDATE" approach was wrong — Postgres rejects FOR UPDATE
// with an aggregate, and locking member rows doesn't prevent two concurrent
// demotes from both seeing count=2). Returns ErrWorkspaceNotFound if the
// workspace doesn't exist.
func (s *Store) withWorkspaceLock(ctx context.Context, workspaceID string, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked string
	err = tx.QueryRow(ctx, `SELECT id FROM workspaces WHERE id = $1 FOR UPDATE`, workspaceID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkspaceNotFound
	}
	if err != nil {
		return fmt.Errorf("lock workspace %s: %w", workspaceID, err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// countAdminsTx counts admins within an open tx (after the workspace row is
// locked). Plain count(*) — no FOR UPDATE on the aggregate (§5, B1).
func countAdminsTx(ctx context.Context, tx pgx.Tx, workspaceID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'admin'`,
		workspaceID,
	).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Invitations
// ---------------------------------------------------------------------------

// DefaultInvitationTTL is how long an invitation stays acceptable before it is
// treated as expired (a torn-down/expired token → 410 gone, §4.6).
const DefaultInvitationTTL = 7 * 24 * time.Hour

// CreateInvitation mints a pending invitation for (workspace, email) and
// returns it with the one-time plaintext token set. The email must already be
// NormalizeEmail-folded by the caller (§4.6). Re-inviting the same email
// upserts the pending row (rotating the token + role). Callers must first
// reject invite-existing-member (→ 409 already_member) — this method does not
// check membership. role must be 'admin' or 'member'.
func (s *Store) CreateInvitation(ctx context.Context, workspaceID, email, role, invitedBy string) (*WorkspaceInvitation, error) {
	if !ValidRole(role) {
		return nil, fmt.Errorf("invalid role %q", role)
	}
	id := "inv_" + generateID()
	plaintext := generateInviteToken()
	tokenHash := hashInviteToken(plaintext)
	expiresAt := time.Now().Add(DefaultInvitationTTL)
	var inviter *string
	if invitedBy != "" {
		inviter = &invitedBy
	}

	inv := &WorkspaceInvitation{}
	// Re-invite upserts the existing pending row (the partial UNIQUE on
	// (workspace_id, email) WHERE status='pending'): rotate the token, role,
	// inviter, and expiry. A prior accepted/revoked/expired row is excluded
	// from the partial index, so it does not block a fresh pending invite.
	err := s.pool.QueryRow(ctx,
		`INSERT INTO workspace_invitations
		     (id, workspace_id, email, role, token_hash, invited_by, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
		 ON CONFLICT (workspace_id, email) WHERE status = 'pending'
		 DO UPDATE SET role = EXCLUDED.role,
		               token_hash = EXCLUDED.token_hash,
		               invited_by = EXCLUDED.invited_by,
		               expires_at = EXCLUDED.expires_at
		 RETURNING id, workspace_id, email, role, invited_by, status, expires_at, accepted_at, created_at`,
		id, workspaceID, email, role, tokenHash, inviter, expiresAt,
	).Scan(&inv.ID, &inv.WorkspaceID, &inv.Email, &inv.Role, &inv.InvitedBy,
		&inv.Status, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	inv.PlaintextToken = plaintext
	return inv, nil
}

// GetInvitationByToken resolves a bearer token to its live pending,
// unexpired invitation. Returns ErrInvitationNotFound when the token matches
// no row, the row is not 'pending', or it has expired (→ 410 gone; §4.6). Does
// NOT mutate state — AcceptInvitation does the locked accept.
func (s *Store) GetInvitationByToken(ctx context.Context, token string) (*WorkspaceInvitation, error) {
	tokenHash := hashInviteToken(token)
	inv := &WorkspaceInvitation{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, workspace_id, email, role, invited_by, status, expires_at, accepted_at, created_at
		   FROM workspace_invitations
		  WHERE token_hash = $1
		    AND status = 'pending'
		    AND (expires_at IS NULL OR expires_at > now())`,
		tokenHash,
	).Scan(&inv.ID, &inv.WorkspaceID, &inv.Email, &inv.Role, &inv.InvitedBy,
		&inv.Status, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvitationNotFound
	}
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// ListPendingInvitations returns the workspace's pending invitations
// (admin-only at the handler layer). Excludes accepted/revoked/expired rows.
func (s *Store) ListPendingInvitations(ctx context.Context, workspaceID string) ([]WorkspaceInvitation, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, workspace_id, email, role, invited_by, status, expires_at, accepted_at, created_at
		   FROM workspace_invitations
		  WHERE workspace_id = $1 AND status = 'pending'
		  ORDER BY created_at DESC`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invs []WorkspaceInvitation
	for rows.Next() {
		var inv WorkspaceInvitation
		if err := rows.Scan(&inv.ID, &inv.WorkspaceID, &inv.Email, &inv.Role, &inv.InvitedBy,
			&inv.Status, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt); err != nil {
			return nil, err
		}
		invs = append(invs, inv)
	}
	return invs, rows.Err()
}

// RevokeInvitation flips a pending invitation to 'revoked' (admin-only). The
// consumed/old token then resolves to no live pending row (→ 410). Returns
// ErrInvitationNotFound when no pending invitation with that id exists in the
// workspace (idempotent: revoking an already-resolved invite is a not-found).
func (s *Store) RevokeInvitation(ctx context.Context, workspaceID, invitationID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE workspace_invitations SET status = 'revoked'
		  WHERE id = $1 AND workspace_id = $2 AND status = 'pending'`,
		invitationID, workspaceID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvitationNotFound
	}
	return nil
}

// AcceptInvitation consumes a pending invitation in a single transaction
// (§4.6 step 4): lock the invitation row FOR UPDATE; re-check it is pending
// and unexpired; verify the authenticated user's normalized email equals the
// invitation's email; INSERT workspace_members … ON CONFLICT DO NOTHING; flip
// status → accepted. Token possession AND email match are both required, and
// the status-flip-in-tx is the single-use guard.
//
// Behaviors:
//   - double-accept by an already-joined user → returns (member, nil) with no
//     error (idempotent 200): the ON CONFLICT DO NOTHING + a still-pending
//     row that we flip, OR an already-accepted row whose member exists.
//   - email mismatch → ErrInvitationEmailMismatch (→ 403, the handler names
//     expected-vs-actual).
//   - torn-down/revoked/expired (no live pending row) → ErrInvitationNotFound
//     (→ 410 gone, fail closed).
//
// userEmail must be the authenticated user's NormalizeEmail-folded address.
func (s *Store) AcceptInvitation(ctx context.Context, token, userID, userEmail string) (*WorkspaceMember, error) {
	tokenHash := hashInviteToken(token)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var (
		invID, workspaceID, inviteEmail, role, status string
		expiresAt                                      *time.Time
	)
	// Lock the invitation row. We intentionally do NOT filter on status here so
	// we can distinguish a genuinely-gone token (no row at all) from a
	// double-accept (status already 'accepted' for the same token).
	err = tx.QueryRow(ctx,
		`SELECT id, workspace_id, email, role, status, expires_at
		   FROM workspace_invitations WHERE token_hash = $1 FOR UPDATE`,
		tokenHash,
	).Scan(&invID, &workspaceID, &inviteEmail, &role, &status, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvitationNotFound
	}
	if err != nil {
		return nil, err
	}

	// Email match is required regardless of status (a stolen token must not be
	// acceptable under the wrong account, even for the idempotent path).
	if NormalizeEmail(inviteEmail) != NormalizeEmail(userEmail) {
		return nil, ErrInvitationEmailMismatch
	}

	// Idempotent double-accept: an already-accepted invite whose membership
	// exists → return the member, no error. If the membership somehow vanished
	// (re-join after leave consumes the token, so this is rare), fall through
	// to not-found.
	if status == "accepted" {
		m, mErr := getMemberTx(ctx, tx, workspaceID, userID)
		if mErr != nil {
			return nil, ErrInvitationNotFound
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return m, nil
	}

	// Anything other than a live pending row → gone (revoked / expired).
	if status != "pending" {
		return nil, ErrInvitationNotFound
	}
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, ErrInvitationNotFound
	}

	// Insert the membership (ON CONFLICT DO NOTHING covers a concurrent
	// double-accept that already joined the user).
	if _, err := tx.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, invited_by)
		 SELECT $1, $2, $3, invited_by FROM workspace_invitations WHERE id = $4
		 ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		workspaceID, userID, role, invID,
	); err != nil {
		return nil, err
	}

	// Single-use guard: flip status → accepted.
	if _, err := tx.Exec(ctx,
		`UPDATE workspace_invitations SET status = 'accepted', accepted_at = now() WHERE id = $1`,
		invID,
	); err != nil {
		return nil, err
	}

	m, err := getMemberTx(ctx, tx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// getMemberTx reads a single membership row within an open tx.
func getMemberTx(ctx context.Context, tx pgx.Tx, workspaceID, userID string) (*WorkspaceMember, error) {
	m := &WorkspaceMember{}
	err := tx.QueryRow(ctx,
		`SELECT workspace_id, user_id, role, invited_by, created_at
		   FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	).Scan(&m.WorkspaceID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}
