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
