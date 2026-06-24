package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// WorkspaceHeaderInput is the shared embed that models the X-E2A-Workspace
// request header in the OpenAPI contract (§4.4). A human web/CLI session uses
// it to select which of the user's workspaces a request acts in; the actual
// resolution happens upstream in the authenticator (principalFromSession), so
// handlers never read this field — it exists ONLY so the header is declared in
// the spec and visible to the generated SDKs. Modeling it as a Huma header
// input field (the way Idempotency-Key is) rather than a SecurityScheme is what
// keeps it SDK-visible and TestSpecGoldenNoDrift-stable.
//
// Precedence (enforced in the auth layer, documented here): on key/OAuth auth
// the workspace is intrinsic, so a matching header is ignored and a header
// naming a different workspace is rejected — it is a session-only selector.
type WorkspaceHeaderInput struct {
	Workspace string `header:"X-E2A-Workspace" doc:"Active workspace id (ws_…). Session-only selector: chooses which of your workspaces this request acts in. Ignored for API-key / OAuth credentials, where the workspace is intrinsic to the credential."`
}

// --- wire views ---------------------------------------------------------

// WorkspaceView is a workspace plus the caller's role in it (§4.4).
type WorkspaceView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role,omitempty" enum:"admin,member" doc:"The caller's role in this workspace. Omitted on reads where the role is not resolved."`
	CreatedAt time.Time `json:"created_at"`
}

func workspaceView(w identity.Workspace, role string) WorkspaceView {
	return WorkspaceView{ID: w.ID, Name: w.Name, Role: role, CreatedAt: w.CreatedAt}
}

// MemberView is one workspace member with role + identity (§4.4).
type MemberView struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name,omitempty"`
	Role      string    `json:"role" enum:"admin,member"`
	CreatedAt time.Time `json:"created_at"`
}

func memberView(m identity.WorkspaceMember) MemberView {
	return MemberView{UserID: m.UserID, Email: m.Email, Name: m.Name, Role: m.Role, CreatedAt: m.CreatedAt}
}

// InvitationView is a pending invitation's non-secret metadata (§4.4). The
// bearer token is returned only once, at creation, in CreateInvitationResponse.
type InvitationView struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Role      string     `json:"role" enum:"admin,member"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func invitationView(i identity.WorkspaceInvitation) InvitationView {
	return InvitationView{ID: i.ID, Email: i.Email, Role: i.Role, Status: i.Status, ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt}
}

// --- inputs / outputs ---------------------------------------------------

type listWorkspacesOutput struct{ Body Page[WorkspaceView] }

type getWorkspaceInput struct {
	ID string `path:"id"`
	WorkspaceHeaderInput
}
type workspaceOutput struct{ Body WorkspaceView }

type renameWorkspaceInput struct {
	ID   string `path:"id"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100" doc:"New workspace display name."`
	}
	WorkspaceHeaderInput
}

type listMembersInput struct {
	ID string `path:"id"`
	WorkspaceHeaderInput
}
type listMembersOutput struct{ Body Page[MemberView] }

type setMemberRoleInput struct {
	ID     string `path:"id"`
	UserID string `path:"user_id"`
	Body   struct {
		Role string `json:"role" enum:"admin,member" doc:"The member's new role."`
	}
	WorkspaceHeaderInput
}
type memberOutput struct{ Body MemberView }

type removeMemberInput struct {
	ID     string `path:"id"`
	UserID string `path:"user_id"`
	WorkspaceHeaderInput
}
type removeMemberOutput struct{ Status int }

type createInvitationInput struct {
	ID   string `path:"id"`
	Body struct {
		Email string `json:"email" format:"email" doc:"Email address to invite."`
		Role  string `json:"role,omitempty" enum:"admin,member" doc:"Role to grant on accept (default member)."`
	}
	WorkspaceHeaderInput
}

// CreateInvitationResponse is the pending-invite metadata plus the one-time
// bearer token (used to build the accept link). The token is shown once.
type CreateInvitationResponse struct {
	InvitationView
	Token string `json:"token" doc:"One-time invitation bearer token (e2a_inv_…). Shown once; used to build the accept link."`
}
type createInvitationOutput struct{ Body CreateInvitationResponse }

type listInvitationsInput struct {
	ID string `path:"id"`
	WorkspaceHeaderInput
}
type listInvitationsOutput struct{ Body Page[InvitationView] }

type revokeInvitationInput struct {
	ID           string `path:"id"`
	InvitationID string `path:"invitation_id"`
	WorkspaceHeaderInput
}
type revokeInvitationOutput struct{ Status int }

type acceptInvitationInput struct {
	Token string `path:"token"`
}
type acceptInvitationOutput struct {
	Status int
	Body   WorkspaceView
}

func (s *Server) registerWorkspaces() {
	bearer := []map[string][]string{{"bearer": {}}}

	// Workspaces (no POST/DELETE in v1 — creation/teardown deferred; §2).
	huma.Register(s.API, huma.Operation{
		OperationID: "listWorkspaces", Method: http.MethodGet, Path: "/v1/workspaces",
		Summary: "List my workspaces", Tags: []string{"workspaces"},
		Description: "Every workspace you are a live member of, each annotated with your role. Your personal (default) workspace sorts first.",
		Security:    bearer,
	}, s.handleListWorkspaces)

	huma.Register(s.API, huma.Operation{
		OperationID: "getWorkspace", Method: http.MethodGet, Path: "/v1/workspaces/{id}",
		Summary: "Get a workspace", Tags: []string{"workspaces"},
		Description: "A workspace by id, with your role. Any live member.",
		Security:    bearer,
	}, s.handleGetWorkspace)

	huma.Register(s.API, huma.Operation{
		OperationID: "renameWorkspace", Method: http.MethodPatch, Path: "/v1/workspaces/{id}",
		Summary: "Rename a workspace", Tags: []string{"workspaces"},
		Description: "Change a workspace's display name (e.g. \"Josh's Workspace\" → \"Acme\"). Admin only; reachable only through a human session.",
		Security:    bearer,
	}, s.handleRenameWorkspace)

	// Members — the permission CRUD.
	huma.Register(s.API, huma.Operation{
		OperationID: "listMembers", Method: http.MethodGet, Path: "/v1/workspaces/{id}/members",
		Summary: "List workspace members", Tags: []string{"workspaces"},
		Description: "Members and their roles. Any live member.",
		Security:    bearer,
	}, s.handleListMembers)

	huma.Register(s.API, huma.Operation{
		OperationID: "setMemberRole", Method: http.MethodPatch, Path: "/v1/workspaces/{id}/members/{user_id}",
		Summary: "Set a member's role", Tags: []string{"workspaces"},
		Description: "Promote to admin or demote to member. Promotion is the transfer-admin mechanism (admins are peers). Cannot demote the last admin. Admin only.",
		Security:    bearer,
	}, s.handleSetMemberRole)

	huma.Register(s.API, huma.Operation{
		OperationID: "removeMember", Method: http.MethodDelete, Path: "/v1/workspaces/{id}/members/{user_id}",
		Summary: "Remove a member (or leave)", Tags: []string{"workspaces"},
		Description: "Remove a member, or leave the workspace by targeting yourself. Cannot remove the last admin. Admin (or self for a leave).",
		Security:    bearer, DefaultStatus: http.StatusNoContent,
	}, s.handleRemoveMember)

	// Invitations.
	huma.Register(s.API, huma.Operation{
		OperationID: "createInvitation", Method: http.MethodPost, Path: "/v1/workspaces/{id}/invitations",
		Summary: "Invite a member", Tags: []string{"workspaces"},
		Description: "Invite an email to join with a role. Sends an accept link. Inviting an existing member returns 409 already_member (use PATCH …/members to change a role). Rate-limited. Admin only.",
		Security:    bearer, DefaultStatus: http.StatusCreated,
	}, s.handleCreateInvitation)

	huma.Register(s.API, huma.Operation{
		OperationID: "listInvitations", Method: http.MethodGet, Path: "/v1/workspaces/{id}/invitations",
		Summary: "List pending invitations", Tags: []string{"workspaces"},
		Description: "Pending invitations for the workspace. Admin only.",
		Security:    bearer,
	}, s.handleListInvitations)

	huma.Register(s.API, huma.Operation{
		OperationID: "revokeInvitation", Method: http.MethodDelete, Path: "/v1/workspaces/{id}/invitations/{invitation_id}",
		Summary: "Revoke a pending invitation", Tags: []string{"workspaces"},
		Description: "Revoke a pending invitation; its accept link stops working. Admin only.",
		Security:    bearer, DefaultStatus: http.StatusNoContent,
	}, s.handleRevokeInvitation)

	huma.Register(s.API, huma.Operation{
		OperationID: "acceptInvitation", Method: http.MethodPost, Path: "/v1/invitations/{token}/accept",
		Summary: "Accept an invitation", Tags: []string{"workspaces"},
		Description: "Accept an invitation. Requires the signed-in user's email to match the invited email. Idempotent (a second accept by the already-joined user returns 200). A revoked/expired/torn-down invitation returns 410.",
		Security:    bearer,
	}, s.handleAcceptInvitation)
}

// --- handlers -----------------------------------------------------------

func (s *Server) handleListWorkspaces(ctx context.Context, _ *struct{}) (*listWorkspacesOutput, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.ListWorkspacesForUser == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "workspaces are not available on this deployment")
	}
	wss, roles, err := s.deps.ListWorkspacesForUser(ctx, p.User.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list workspaces")
	}
	views := make([]WorkspaceView, 0, len(wss))
	for i, w := range wss {
		views = append(views, workspaceView(w, roles[i]))
	}
	return &listWorkspacesOutput{Body: NewPage(views, "")}, nil
}

func (s *Server) handleGetWorkspace(ctx context.Context, in *getWorkspaceInput) (*workspaceOutput, error) {
	// Any live member may read. Re-verify the caller's membership of the
	// requested id rather than trusting the resolved active workspace — the id
	// in the path may differ from the active one.
	p, role, err := s.memberOf(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	_ = p
	w, err := s.deps.GetWorkspace(ctx, in.ID)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return &workspaceOutput{Body: workspaceView(*w, role)}, nil
}

func (s *Server) handleRenameWorkspace(ctx context.Context, in *renameWorkspaceInput) (*workspaceOutput, error) {
	p, err := s.adminOf(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if s.deps.RenameWorkspace == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "workspaces are not available on this deployment")
	}
	if err := s.deps.RenameWorkspace(ctx, in.ID, in.Body.Name, p.User.ID); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	w, err := s.deps.GetWorkspace(ctx, in.ID)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return &workspaceOutput{Body: workspaceView(*w, identity.RoleAdmin)}, nil
}

func (s *Server) handleListMembers(ctx context.Context, in *listMembersInput) (*listMembersOutput, error) {
	if _, _, err := s.memberOf(ctx, in.ID); err != nil {
		return nil, err
	}
	members, err := s.deps.ListMembers(ctx, in.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list members")
	}
	views := make([]MemberView, 0, len(members))
	for _, m := range members {
		views = append(views, memberView(m))
	}
	return &listMembersOutput{Body: NewPage(views, "")}, nil
}

func (s *Server) handleSetMemberRole(ctx context.Context, in *setMemberRoleInput) (*memberOutput, error) {
	p, err := s.adminOf(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !identity.ValidRole(in.Body.Role) {
		return nil, NewError(http.StatusBadRequest, "invalid_role", "role must be 'admin' or 'member'")
	}
	if err := s.deps.SetMemberRole(ctx, in.ID, in.UserID, in.Body.Role, p.User.ID); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	// Re-read for the post-update view.
	members, err := s.deps.ListMembers(ctx, in.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to load member")
	}
	for _, m := range members {
		if m.UserID == in.UserID {
			return &memberOutput{Body: memberView(m)}, nil
		}
	}
	return nil, NewError(http.StatusNotFound, "not_found", "member not found")
}

func (s *Server) handleRemoveMember(ctx context.Context, in *removeMemberInput) (*removeMemberOutput, error) {
	// Remove is admin; leave (self-target) is allowed for any member. We resolve
	// membership of the path workspace and branch: a non-admin may only remove
	// themselves.
	p, role, err := s.memberOf(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if role != identity.RoleAdmin && in.UserID != p.User.ID {
		return nil, NewError(http.StatusForbidden, "forbidden", "only an admin may remove another member; you may only leave (remove yourself)")
	}
	if err := s.deps.RemoveMember(ctx, in.ID, in.UserID, p.User.ID); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return &removeMemberOutput{Status: http.StatusNoContent}, nil
}

func (s *Server) handleCreateInvitation(ctx context.Context, in *createInvitationInput) (*createInvitationOutput, error) {
	p, err := s.adminOf(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	role := in.Body.Role
	if role == "" {
		role = identity.RoleMember
	}
	if !identity.ValidRole(role) {
		return nil, NewError(http.StatusBadRequest, "invalid_role", "role must be 'admin' or 'member'")
	}
	// Case-fold the email (case only, NOT Gmail dot/plus stripping) before the
	// already-member check and storage (§4.6).
	email := identity.NormalizeEmail(in.Body.Email)
	if email == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "email is required")
	}

	// Rate-limit per workspace so a compromised admin session can't turn invites
	// into a spam cannon (§4.6).
	if s.deps.InviteLimit != nil {
		ok, retryAfter, _, _, _ := s.deps.InviteLimit(in.ID)
		if !ok {
			secs := int(retryAfter.Round(time.Second).Seconds())
			if secs < 1 {
				secs = 1
			}
			return nil, NewError(http.StatusTooManyRequests, "rate_limited",
				"too many invitations for this workspace; slow down").
				WithDetails(map[string]any{"retry_after_seconds": secs})
		}
	}

	// Invite-existing-member → 409 already_member, pointing the caller at PATCH
	// …/members for role changes (§4.6). Same for same-role / self-invite.
	if s.deps.IsMemberByEmail != nil {
		member, mErr := s.deps.IsMemberByEmail(ctx, in.ID, email)
		if mErr != nil {
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to check membership")
		}
		if member {
			return nil, NewError(http.StatusConflict, "already_member",
				"that email is already a member of this workspace; use PATCH /v1/workspaces/{id}/members/{user_id} to change their role")
		}
	}

	inv, err := s.deps.CreateInvitation(ctx, in.ID, email, role, p.User.ID)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}

	// Send the accept link via the system-mail noreply path. Best-effort: a
	// send failure does not roll back the pending row (the admin can re-invite,
	// which rotates the token); log + continue so the API still returns the
	// invitation. The token is returned in the body regardless.
	if s.deps.SendInvitationEmail != nil {
		_ = s.deps.SendInvitationEmail(ctx, email, in.ID, inv.PlaintextToken)
	}

	return &createInvitationOutput{Body: CreateInvitationResponse{
		InvitationView: invitationView(*inv),
		Token:          inv.PlaintextToken,
	}}, nil
}

func (s *Server) handleListInvitations(ctx context.Context, in *listInvitationsInput) (*listInvitationsOutput, error) {
	if _, err := s.adminOf(ctx, in.ID); err != nil {
		return nil, err
	}
	invs, err := s.deps.ListPendingInvitations(ctx, in.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list invitations")
	}
	views := make([]InvitationView, 0, len(invs))
	for _, i := range invs {
		views = append(views, invitationView(i))
	}
	return &listInvitationsOutput{Body: NewPage(views, "")}, nil
}

func (s *Server) handleRevokeInvitation(ctx context.Context, in *revokeInvitationInput) (*revokeInvitationOutput, error) {
	p, err := s.adminOf(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if err := s.deps.RevokeInvitation(ctx, in.ID, in.InvitationID, p.User.ID); err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return &revokeInvitationOutput{Status: http.StatusNoContent}, nil
}

func (s *Server) handleAcceptInvitation(ctx context.Context, in *acceptInvitationInput) (*acceptInvitationOutput, error) {
	// Accept authenticates the user (any valid credential) but does NOT require
	// workspace membership — that is exactly what this op grants. The token +
	// email match are the authorization.
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if s.deps.AcceptInvitation == nil {
		return nil, NewError(http.StatusNotImplemented, "not_implemented", "workspaces are not available on this deployment")
	}
	member, err := s.deps.AcceptInvitation(ctx, in.Token, p.User.ID, identity.NormalizeEmail(p.User.Email))
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrInvitationEmailMismatch):
			return nil, NewError(http.StatusForbidden, "invitation_email_mismatch",
				"this invitation is for a different email address; sign in with the invited account").
				WithDetails(map[string]any{"signed_in_as": p.User.Email})
		case errors.Is(err, identity.ErrInvitationNotFound):
			return nil, NewError(http.StatusGone, "invitation_gone",
				"this invitation is no longer valid (revoked, expired, or the workspace was removed)")
		default:
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to accept invitation")
		}
	}
	w, err := s.deps.GetWorkspace(ctx, member.WorkspaceID)
	if err != nil {
		return nil, mapWorkspaceErr(err)
	}
	return &acceptInvitationOutput{Status: http.StatusOK, Body: workspaceView(*w, member.Role)}, nil
}

// --- authz helpers ------------------------------------------------------

// memberOf authenticates the caller and confirms they are a live member of the
// path-supplied workspace id, returning the principal and their role in THAT
// workspace. It re-resolves membership of the path id rather than trusting the
// resolved active workspace (which may differ from the id in the path). A
// non-member → 403, never a silent fallback (§5 header-spoofing).
func (s *Server) memberOf(ctx context.Context, workspaceID string) (*identity.Principal, string, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, "", err
	}
	if s.deps.ResolveMembership == nil {
		return nil, "", NewError(http.StatusNotImplemented, "not_implemented", "workspaces are not available on this deployment")
	}
	role, err := s.deps.ResolveMembership(ctx, p.User.ID, workspaceID)
	if err != nil {
		if errors.Is(err, identity.ErrNotMember) {
			return nil, "", NewError(http.StatusForbidden, "forbidden", "you are not a member of this workspace")
		}
		return nil, "", NewError(http.StatusInternalServerError, "internal_error", "failed to resolve membership")
	}
	return p, role, nil
}

// adminOf is memberOf with two extra guards: the credential must be account-
// scoped (admin ops are never agent-pinned) and the caller's role in the path
// workspace must be admin (§4.3.1 — admin is session-only and member-capped for
// keys/tokens). Returns the principal on success.
func (s *Server) adminOf(ctx context.Context, workspaceID string) (*identity.Principal, error) {
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	p, role, err := s.memberOf(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if role != identity.RoleAdmin {
		return nil, NewError(http.StatusForbidden, "forbidden",
			"this operation requires the admin role in this workspace; admin authority is reachable only through a human session")
	}
	return p, nil
}

// mapWorkspaceErr maps the identity sentinel errors to envelope HTTP statuses.
func mapWorkspaceErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrWorkspaceNotFound):
		return NewError(http.StatusNotFound, "not_found", "workspace not found")
	case errors.Is(err, identity.ErrNotMember):
		return NewError(http.StatusNotFound, "not_found", "member not found")
	case errors.Is(err, identity.ErrLastAdmin):
		return NewError(http.StatusConflict, "last_admin",
			"cannot leave, remove, or demote the only admin; promote another member to admin first")
	case errors.Is(err, identity.ErrAlreadyMember):
		return NewError(http.StatusConflict, "already_member",
			"that email is already a member of this workspace")
	case errors.Is(err, identity.ErrInvitationNotFound):
		return NewError(http.StatusGone, "invitation_gone", "invitation not found or no longer pending")
	case errors.Is(err, identity.ErrInvitationEmailMismatch):
		return NewError(http.StatusForbidden, "invitation_email_mismatch", "invitation is for a different email address")
	default:
		return NewError(http.StatusInternalServerError, "internal_error", "workspace operation failed")
	}
}
