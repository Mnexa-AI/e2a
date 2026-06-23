package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// errStatus extracts the HTTP status + machine code from an envelope error.
func errStatus(err error) (int, string) {
	if e, ok := err.(*ErrorEnvelope); ok {
		return e.GetStatus(), e.Code()
	}
	return 0, ""
}

// principalCtx builds a context carrying a pre-resolved principal, the way the
// rate-limit middleware caches it — so the role choke points can be exercised
// without an HTTP round-trip or DB. requirePrincipal short-circuits on a cached
// principal (principalFromContext), so this drives requireWorkspaceRole and
// friends directly.
func principalCtx(p *identity.Principal) context.Context {
	return withPrincipal(context.Background(), p)
}

func acctMember(role string) *identity.Principal {
	return &identity.Principal{
		User:      &identity.User{ID: "u_1"},
		Scope:     identity.ScopeAccount,
		Workspace: &identity.Workspace{ID: "ws_1", Name: "Acme"},
		Role:      role,
	}
}

func agentKey(role string) *identity.Principal {
	return &identity.Principal{
		User:      &identity.User{ID: "u_1"},
		Scope:     identity.ScopeAgent,
		AgentID:   "support@acme.com",
		Workspace: &identity.Workspace{ID: "ws_1", Name: "Acme"},
		Role:      role,
	}
}

// TestRequireWorkspaceRole_Matrix is the role × scope authz matrix over the
// choke points (§4.3 / §4.3.1). It asserts:
//   - member floor passes any live member (admin ⊇ member);
//   - admin gate passes only an admin SESSION;
//   - keys/tokens are member-capped, so an agent/account key — whose Role is
//     fixed to member at auth time — can NEVER clear the admin gate, even if a
//     bug stamped it admin (requireWorkspaceAdmin also requires account scope,
//     barring agent-pinned credentials);
//   - a principal with no resolved workspace fails closed.
func TestRequireWorkspaceRole_Matrix(t *testing.T) {
	s := &Server{} // no deps needed: the cached principal short-circuits auth.

	t.Run("member floor admits admin and member", func(t *testing.T) {
		for _, role := range []string{identity.RoleAdmin, identity.RoleMember} {
			if _, err := s.requireWorkspaceMember(principalCtx(acctMember(role))); err != nil {
				t.Errorf("member floor rejected %s: %v", role, err)
			}
		}
	})

	t.Run("admin gate admits admin session only", func(t *testing.T) {
		if _, err := s.requireWorkspaceAdmin(principalCtx(acctMember(identity.RoleAdmin))); err != nil {
			t.Errorf("admin gate rejected admin session: %v", err)
		}
		_, err := s.requireWorkspaceAdmin(principalCtx(acctMember(identity.RoleMember)))
		if err == nil {
			t.Fatal("admin gate admitted a member session")
		}
		if status, _ := errStatus(err); status != http.StatusForbidden {
			t.Errorf("member at admin gate: status %d, want 403", status)
		}
	})

	t.Run("agent-scoped key cannot reach the admin gate", func(t *testing.T) {
		// Even if some bug stamped an agent key as admin, requireWorkspaceAdmin
		// composes the account-scope ceiling first → 403.
		_, err := s.requireWorkspaceAdmin(principalCtx(agentKey(identity.RoleAdmin)))
		if err == nil {
			t.Fatal("agent-scoped credential cleared the admin gate")
		}
		if status, _ := errStatus(err); status != http.StatusForbidden {
			t.Errorf("agent key at admin gate: status %d, want 403", status)
		}
	})

	t.Run("agent-scoped key clears the member floor for resource ops", func(t *testing.T) {
		// The member floor does not impose the account-scope ceiling — an
		// agent-scoped key is a live member of its workspace and may operate
		// resources (subject to its agent pinning, enforced separately by
		// requireAgentAccess).
		if _, err := s.requireWorkspaceMember(principalCtx(agentKey(identity.RoleMember))); err != nil {
			t.Errorf("member floor rejected agent key: %v", err)
		}
	})

	t.Run("no resolved workspace fails closed", func(t *testing.T) {
		p := &identity.Principal{User: &identity.User{ID: "u_1"}, Scope: identity.ScopeAccount, Role: identity.RoleMember}
		_, err := s.requireWorkspaceMember(principalCtx(p))
		if err == nil {
			t.Fatal("a principal with no workspace cleared the member floor")
		}
		if status, _ := errStatus(err); status != http.StatusForbidden {
			t.Errorf("no-workspace principal: status %d, want 403", status)
		}
	})
}
