package httpapi

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// Scope enforcement — the hard scope ceiling (Slice 5a / design §5 / decision
// 10). A credential's scope, not its auth method, bounds its blast radius:
//
//   - account scope: account-wide admin (agent/domain/key management, account
//     settings). Reaches everything the owner owns.
//   - agent scope: bound to a single agent (runtime/inbox tier). May act ONLY
//     as that one agent, and is barred from account-only operations.
//
// These helpers are the single choke points handlers call so the ceiling is
// enforced uniformly. They return a 403 envelope (machine code "forbidden"),
// distinct from the 401 "unauthorized" a missing/invalid credential yields.

// requireAccountScope authenticates the caller and rejects agent-scoped
// credentials with 403. Use on account-only operations (create/delete agent,
// domain claim/verify/delete, API-key and account management) — the structural
// guarantee that a leaked agent credential cannot widen its own authority.
func (s *Server) requireAccountScope(ctx context.Context) (*identity.Principal, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Scope != identity.ScopeAccount {
		return nil, NewError(http.StatusForbidden, "forbidden",
			"this operation requires an account-scoped credential; agent-scoped credentials cannot perform account administration")
	}
	return p, nil
}

// requireAccountUser is the user-returning convenience over requireAccountScope
// for the many account-admin handlers that only need user.ID — a clean drop-in
// for requireUser on operations the agent tier must not reach.
func (s *Server) requireAccountUser(ctx context.Context) (*identity.User, error) {
	p, err := s.requireAccountScope(ctx)
	if err != nil {
		return nil, err
	}
	return p.User, nil
}

// requireWorkspaceRole authenticates the caller and confirms their role in the
// resolved active workspace meets minRole (§4.3 / §4.3.1). It is the role
// choke point that sits alongside the scope choke points: scope (credential
// blast radius) and role (membership authority) compose, and the effective
// permission is their intersection.
//
//   - minRole == RoleMember → any live member passes. This is the floor for
//     resource ops (agents / domains / keys): everything a single-user account
//     can do today.
//   - minRole == RoleAdmin → only an admin session passes. This gates the
//     people/workspace/billing ops (invite/remove/change-role, rename, billing).
//
// Admin authority is reachable ONLY through a human web session (§4.3.1): every
// API key and OAuth/MCP token is member-capped (their Principal.Role is fixed
// to member at auth time, regardless of who minted them), so a leaked
// credential can never reach an admin op. Callers that also need an
// account-scoped credential (most admin ops) should compose this with
// requireAccountScope.
//
// A resolved workspace is required — a principal whose workspace did not
// resolve (e.g. an account-scope OAuth token with no pinned workspace that
// somehow reached here) fails closed with 403.
func (s *Server) requireWorkspaceRole(ctx context.Context, minRole string) (*identity.Principal, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Workspace == nil {
		return nil, NewError(http.StatusForbidden, "forbidden",
			"no active workspace resolved for this credential")
	}
	if minRole == identity.RoleAdmin && p.Role != identity.RoleAdmin {
		return nil, NewError(http.StatusForbidden, "forbidden",
			"this operation requires the admin role in the active workspace; admin authority is reachable only through a human session")
	}
	return p, nil
}

// requireWorkspaceAdmin is the admin convenience over requireWorkspaceRole for
// the people/workspace/billing ops. It composes the admin-role check with the
// account-scope ceiling (admin ops are never agent-pinned) so a single call
// enforces both axes.
func (s *Server) requireWorkspaceAdmin(ctx context.Context) (*identity.Principal, error) {
	if _, err := s.requireAccountScope(ctx); err != nil {
		return nil, err
	}
	return s.requireWorkspaceRole(ctx, identity.RoleAdmin)
}

// requireWorkspaceMember is the member-floor convenience over
// requireWorkspaceRole for resource ops. Any live member of the active
// workspace passes (admins included, since admin ⊇ member).
func (s *Server) requireWorkspaceMember(ctx context.Context) (*identity.Principal, error) {
	return s.requireWorkspaceRole(ctx, identity.RoleMember)
}

// requireAgentAccess authenticates the caller and confirms it may act as
// agentID. Account-scoped credentials may act as any agent they own (ownership
// is still checked separately by the handler). An agent-scoped credential is
// pinned: it may act ONLY as the single agent it is bound to — any other agent
// is 403, even if the same owner owns both.
func (s *Server) requireAgentAccess(ctx context.Context, agentID string) (*identity.Principal, error) {
	p, err := s.requirePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if p.Scope == identity.ScopeAgent && p.AgentID != agentID {
		return nil, NewError(http.StatusForbidden, "forbidden",
			"this agent-scoped credential is bound to a different agent")
	}
	return p, nil
}
