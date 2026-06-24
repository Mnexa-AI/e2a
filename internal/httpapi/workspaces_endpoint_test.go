package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// fakeWS is an in-memory workspace backend the workspace deps bind to, so the
// real Huma handlers (authz + status mapping) are exercised end-to-end without
// Postgres. Membership/role live in members[ws][user]=role.
type fakeWS struct {
	members     map[string]map[string]string // ws → user → role
	names       map[string]string            // ws → name
	emails      map[string]string            // user → email
	invitations map[string]*identity.WorkspaceInvitation
	sent        []string // emails the invitation sender was asked to deliver
	inviteOK    bool     // whether InviteLimit allows
}

func newFakeWS() *fakeWS {
	return &fakeWS{
		members:     map[string]map[string]string{},
		names:       map[string]string{},
		emails:      map[string]string{},
		invitations: map[string]*identity.WorkspaceInvitation{},
		inviteOK:    true,
	}
}

func (f *fakeWS) role(ws, user string) (string, bool) {
	if m, ok := f.members[ws]; ok {
		r, ok := m[user]
		return r, ok
	}
	return "", false
}

func (f *fakeWS) deps(princ func(r *http.Request) (*identity.Principal, error)) Deps {
	return Deps{
		PrincipalAuthenticator: princ,
		ResolveMembership: func(ctx context.Context, userID, workspaceID string) (string, error) {
			if r, ok := f.role(workspaceID, userID); ok {
				return r, nil
			}
			return "", identity.ErrNotMember
		},
		GetWorkspace: func(ctx context.Context, id string) (*identity.Workspace, error) {
			if name, ok := f.names[id]; ok {
				return &identity.Workspace{ID: id, Name: name}, nil
			}
			return nil, identity.ErrWorkspaceNotFound
		},
		ListWorkspacesForUser: func(ctx context.Context, userID string) ([]identity.Workspace, []string, error) {
			var wss []identity.Workspace
			var roles []string
			for ws, m := range f.members {
				if r, ok := m[userID]; ok {
					wss = append(wss, identity.Workspace{ID: ws, Name: f.names[ws]})
					roles = append(roles, r)
				}
			}
			return wss, roles, nil
		},
		RenameWorkspace: func(ctx context.Context, workspaceID, name, actorUserID string) error {
			if _, ok := f.names[workspaceID]; !ok {
				return identity.ErrWorkspaceNotFound
			}
			f.names[workspaceID] = name
			return nil
		},
		ListMembers: func(ctx context.Context, workspaceID string) ([]identity.WorkspaceMember, error) {
			var out []identity.WorkspaceMember
			for u, r := range f.members[workspaceID] {
				out = append(out, identity.WorkspaceMember{WorkspaceID: workspaceID, UserID: u, Role: r, Email: f.emails[u]})
			}
			return out, nil
		},
		SetMemberRole: func(ctx context.Context, workspaceID, userID, newRole, actorUserID string) error {
			m, ok := f.members[workspaceID]
			if !ok {
				return identity.ErrWorkspaceNotFound
			}
			cur, ok := m[userID]
			if !ok {
				return identity.ErrNotMember
			}
			if cur == newRole {
				return nil
			}
			if cur == identity.RoleAdmin && newRole == identity.RoleMember && f.countAdmins(workspaceID) <= 1 {
				return identity.ErrLastAdmin
			}
			m[userID] = newRole
			return nil
		},
		RemoveMember: func(ctx context.Context, workspaceID, userID, actorUserID string) error {
			m, ok := f.members[workspaceID]
			if !ok {
				return identity.ErrWorkspaceNotFound
			}
			cur, ok := m[userID]
			if !ok {
				return identity.ErrNotMember
			}
			if cur == identity.RoleAdmin && f.countAdmins(workspaceID) <= 1 {
				return identity.ErrLastAdmin
			}
			delete(m, userID)
			return nil
		},
		IsMemberByEmail: func(ctx context.Context, workspaceID, email string) (bool, error) {
			for u := range f.members[workspaceID] {
				if identity.NormalizeEmail(f.emails[u]) == identity.NormalizeEmail(email) {
					return true, nil
				}
			}
			return false, nil
		},
		CreateInvitation: func(ctx context.Context, workspaceID, email, role, invitedBy string) (*identity.WorkspaceInvitation, error) {
			inv := &identity.WorkspaceInvitation{
				ID: "inv_" + email, WorkspaceID: workspaceID, Email: email, Role: role,
				Status: "pending", CreatedAt: time.Now(), PlaintextToken: "e2a_inv_tok_" + email,
			}
			f.invitations[inv.PlaintextToken] = inv
			return inv, nil
		},
		ListPendingInvitations: func(ctx context.Context, workspaceID string) ([]identity.WorkspaceInvitation, error) {
			var out []identity.WorkspaceInvitation
			for _, inv := range f.invitations {
				if inv.WorkspaceID == workspaceID && inv.Status == "pending" {
					out = append(out, *inv)
				}
			}
			return out, nil
		},
		RevokeInvitation: func(ctx context.Context, workspaceID, invitationID, actorUserID string) error {
			for _, inv := range f.invitations {
				if inv.ID == invitationID && inv.WorkspaceID == workspaceID && inv.Status == "pending" {
					inv.Status = "revoked"
					return nil
				}
			}
			return identity.ErrInvitationNotFound
		},
		AcceptInvitation: func(ctx context.Context, token, userID, userEmail string) (*identity.WorkspaceMember, error) {
			inv, ok := f.invitations[token]
			if !ok || inv.Status != "pending" {
				return nil, identity.ErrInvitationNotFound
			}
			if identity.NormalizeEmail(inv.Email) != identity.NormalizeEmail(userEmail) {
				return nil, identity.ErrInvitationEmailMismatch
			}
			inv.Status = "accepted"
			if f.members[inv.WorkspaceID] == nil {
				f.members[inv.WorkspaceID] = map[string]string{}
			}
			f.members[inv.WorkspaceID][userID] = inv.Role
			return &identity.WorkspaceMember{WorkspaceID: inv.WorkspaceID, UserID: userID, Role: inv.Role}, nil
		},
		SendInvitationEmail: func(ctx context.Context, email, workspaceID, token string) error {
			f.sent = append(f.sent, email)
			return nil
		},
		InviteLimit: func(key string) (bool, time.Duration, int, int, int) {
			return f.inviteOK, time.Minute, 50, 49, 60
		},
		Legacy: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
}

func (f *fakeWS) countAdmins(ws string) int {
	n := 0
	for _, r := range f.members[ws] {
		if r == identity.RoleAdmin {
			n++
		}
	}
	return n
}

// wsTestServer wires the fake with a PrincipalAuthenticator keyed by bearer:
//
//	"Bearer admin"  → u_admin (account session)
//	"Bearer member" → u_member (account session)
//	"Bearer agent"  → u_member but agent-scoped (to test the scope ceiling)
//	"Bearer outsider" → u_outsider (no membership)
//
// The active workspace/role on the principal is set from the fake so the
// session principal carries Workspace+Role like principalFromSession would.
func wsTestServer(t *testing.T, f *fakeWS) *httptest.Server {
	t.Helper()
	princ := func(r *http.Request) (*identity.Principal, error) {
		var userID, scope, agentID string
		switch r.Header.Get("Authorization") {
		case "Bearer admin":
			userID, scope = "u_admin", identity.ScopeAccount
		case "Bearer member":
			userID, scope = "u_member", identity.ScopeAccount
		case "Bearer agent":
			userID, scope, agentID = "u_member", identity.ScopeAgent, "bot@acme.com"
		case "Bearer outsider":
			userID, scope = "u_outsider", identity.ScopeAccount
		default:
			return nil, errors.New("unauthorized")
		}
		p := &identity.Principal{
			User:    &identity.User{ID: userID, Email: f.emails[userID]},
			Scope:   scope,
			AgentID: agentID,
			Role:    identity.RoleMember,
		}
		// Resolve active workspace ws_1 for membership-carrying principals.
		if r, ok := f.role("ws_1", userID); ok {
			p.Workspace = &identity.Workspace{ID: "ws_1", Name: f.names["ws_1"]}
			p.Role = r
		}
		return p, nil
	}
	srv := httptest.NewServer(New(f.deps(princ)))
	t.Cleanup(srv.Close)
	return srv
}

func doWS(t *testing.T, srv *httptest.Server, method, path, bearer string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func seedWorkspace(f *fakeWS) {
	f.names["ws_1"] = "Acme"
	f.members["ws_1"] = map[string]string{"u_admin": identity.RoleAdmin, "u_member": identity.RoleMember}
	f.emails["u_admin"] = "admin@acme.com"
	f.emails["u_member"] = "member@acme.com"
	f.emails["u_outsider"] = "outsider@elsewhere.com"
}

// --- happy paths --------------------------------------------------------

func TestWorkspaces_ListAndGet(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)

	resp, body := doWS(t, srv, "GET", "/v1/workspaces", "member", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d body=%s", resp.StatusCode, body)
	}
	var list struct {
		Items []WorkspaceView `json:"items"`
	}
	json.Unmarshal(body, &list)
	if len(list.Items) != 1 || list.Items[0].ID != "ws_1" || list.Items[0].Role != identity.RoleMember {
		t.Fatalf("unexpected list: %+v", list.Items)
	}

	// GET single, as admin.
	resp, body = doWS(t, srv, "GET", "/v1/workspaces/ws_1", "admin", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get: %d body=%s", resp.StatusCode, body)
	}
	var w WorkspaceView
	json.Unmarshal(body, &w)
	if w.Name != "Acme" || w.Role != identity.RoleAdmin {
		t.Fatalf("unexpected workspace: %+v", w)
	}
}

func TestWorkspaces_GetForbiddenForOutsider(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)
	resp, _ := doWS(t, srv, "GET", "/v1/workspaces/ws_1", "outsider", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider GET: %d, want 403", resp.StatusCode)
	}
}

func TestWorkspaces_RenameAdminOnly(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)

	// Member cannot rename → 403.
	resp, _ := doWS(t, srv, "PATCH", "/v1/workspaces/ws_1", "member", map[string]string{"name": "NewName"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member rename: %d, want 403", resp.StatusCode)
	}
	// Admin can rename.
	resp, body := doWS(t, srv, "PATCH", "/v1/workspaces/ws_1", "admin", map[string]string{"name": "NewName"})
	if resp.StatusCode != 200 {
		t.Fatalf("admin rename: %d body=%s", resp.StatusCode, body)
	}
	if f.names["ws_1"] != "NewName" {
		t.Fatalf("name not updated: %q", f.names["ws_1"])
	}
	// Agent-scoped credential cannot reach an admin op (scope ceiling) → 403.
	resp, _ = doWS(t, srv, "PATCH", "/v1/workspaces/ws_1", "agent", map[string]string{"name": "X"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent rename: %d, want 403", resp.StatusCode)
	}
}

// --- members ------------------------------------------------------------

func TestMembers_ListAndSetRole(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)

	resp, body := doWS(t, srv, "GET", "/v1/workspaces/ws_1/members", "member", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list members: %d body=%s", resp.StatusCode, body)
	}
	// Member cannot change roles → 403 (assert before any promotion).
	resp, _ = doWS(t, srv, "PATCH", "/v1/workspaces/ws_1/members/u_admin", "member", map[string]string{"role": "member"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member set-role: %d, want 403", resp.StatusCode)
	}
	// Admin promotes member.
	resp, body = doWS(t, srv, "PATCH", "/v1/workspaces/ws_1/members/u_member", "admin", map[string]string{"role": "admin"})
	if resp.StatusCode != 200 {
		t.Fatalf("promote: %d body=%s", resp.StatusCode, body)
	}
	if r, _ := f.role("ws_1", "u_member"); r != identity.RoleAdmin {
		t.Fatalf("role not promoted: %q", r)
	}
}

func TestMembers_LastAdminGuard(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f) // u_admin is the only admin
	srv := wsTestServer(t, f)
	// Demoting the sole admin → 409 last_admin.
	resp, body := doWS(t, srv, "PATCH", "/v1/workspaces/ws_1/members/u_admin", "admin", map[string]string{"role": "member"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("demote sole admin: %d body=%s, want 409", resp.StatusCode, body)
	}
	if code := envCode(body); code != "last_admin" {
		t.Fatalf("demote sole admin code=%q, want last_admin", code)
	}
}

func TestMembers_LeaveVsRemove(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)

	// Member removing another member → 403 (only admin or self).
	resp, _ := doWS(t, srv, "DELETE", "/v1/workspaces/ws_1/members/u_admin", "member", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member removing other: %d, want 403", resp.StatusCode)
	}
	// Member leaving (self) → 204.
	resp, _ = doWS(t, srv, "DELETE", "/v1/workspaces/ws_1/members/u_member", "member", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("member self-leave: %d, want 204", resp.StatusCode)
	}
	if _, ok := f.role("ws_1", "u_member"); ok {
		t.Fatal("member still present after leave")
	}
}

// --- invitations --------------------------------------------------------

func TestInvitations_CreateAndAccept(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	f.emails["u_new"] = "newbie@acme.com"
	srv := wsTestServer(t, f)

	// Admin invites a new email.
	resp, body := doWS(t, srv, "POST", "/v1/workspaces/ws_1/invitations", "admin",
		map[string]string{"email": "Newbie@Acme.com", "role": "member"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite: %d body=%s", resp.StatusCode, body)
	}
	var created CreateInvitationResponse
	json.Unmarshal(body, &created)
	if created.Token == "" {
		t.Fatal("invite returned no token")
	}
	if created.Email != "newbie@acme.com" {
		t.Fatalf("invite email not normalized: %q", created.Email)
	}
	if len(f.sent) != 1 || f.sent[0] != "newbie@acme.com" {
		t.Fatalf("invite email not sent: %+v", f.sent)
	}

	// Member cannot invite → 403.
	resp, _ = doWS(t, srv, "POST", "/v1/workspaces/ws_1/invitations", "member",
		map[string]string{"email": "x@acme.com"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member invite: %d, want 403", resp.StatusCode)
	}

	// Accept by the (different) authenticated invitee — wire a principal whose
	// email matches. We reuse the outsider bearer but the accept handler keys on
	// the principal's email, so set it to the invited address via u_new.
	acceptPrinc := func(token, userID, userEmail string) (*identity.WorkspaceMember, error) {
		return f.deps(nil).AcceptInvitation(context.Background(), token, userID, userEmail)
	}
	if _, err := acceptPrinc(created.Token, "u_new", "newbie@acme.com"); err != nil {
		t.Fatalf("accept store-level: %v", err)
	}
	if r, _ := f.role("ws_1", "u_new"); r != identity.RoleMember {
		t.Fatalf("invitee not joined: %q", r)
	}
}

func TestInvitations_AlreadyMember409(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)
	// Inviting an existing member's email → 409 already_member.
	resp, body := doWS(t, srv, "POST", "/v1/workspaces/ws_1/invitations", "admin",
		map[string]string{"email": "Member@Acme.com"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("invite existing member: %d body=%s, want 409", resp.StatusCode, body)
	}
	if code := envCode(body); code != "already_member" {
		t.Fatalf("code=%q, want already_member", code)
	}
}

func TestInvitations_RateLimited(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	f.inviteOK = false
	srv := wsTestServer(t, f)
	resp, body := doWS(t, srv, "POST", "/v1/workspaces/ws_1/invitations", "admin",
		map[string]string{"email": "new@acme.com"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate-limited invite: %d body=%s, want 429", resp.StatusCode, body)
	}
}

func TestInvitations_AcceptEmailMismatch403(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)
	// Create a pending invitation directly.
	inv, _ := f.deps(nil).CreateInvitation(context.Background(), "ws_1", "invited@acme.com", "member", "u_admin")
	// Sign in as a different email (outsider) and accept → 403 mismatch.
	resp, body := doWS(t, srv, "POST", "/v1/invitations/"+inv.PlaintextToken+"/accept", "outsider", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatch accept: %d body=%s, want 403", resp.StatusCode, body)
	}
	if code := envCode(body); code != "invitation_email_mismatch" {
		t.Fatalf("code=%q, want invitation_email_mismatch", code)
	}
}

func TestInvitations_AcceptTornDown410(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)
	resp, body := doWS(t, srv, "POST", "/v1/invitations/e2a_inv_does_not_exist/accept", "outsider", nil)
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("torn-down accept: %d body=%s, want 410", resp.StatusCode, body)
	}
	if code := envCode(body); code != "invitation_gone" {
		t.Fatalf("code=%q, want invitation_gone", code)
	}
}

func TestInvitations_AcceptIdempotent200(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	f.emails["u_outsider"] = "outsider@elsewhere.com"
	srv := wsTestServer(t, f)
	// Invite the outsider's own email so the accept email matches.
	inv, _ := f.deps(nil).CreateInvitation(context.Background(), "ws_1", "outsider@elsewhere.com", "member", "u_admin")
	resp, body := doWS(t, srv, "POST", "/v1/invitations/"+inv.PlaintextToken+"/accept", "outsider", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first accept: %d body=%s, want 200", resp.StatusCode, body)
	}
	// The membership now exists. A re-accept of the (now-accepted) token returns
	// gone in this fake (status flips to accepted) — the store-level idempotent
	// path is covered by the DB-backed identity test. Here we assert the joined
	// outsider can now read the workspace (membership took effect).
	resp, _ = doWS(t, srv, "GET", "/v1/workspaces/ws_1", "outsider", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("post-accept GET: %d, want 200", resp.StatusCode)
	}
}

func TestInvitations_RevokeAdminOnly(t *testing.T) {
	f := newFakeWS()
	seedWorkspace(f)
	srv := wsTestServer(t, f)
	inv, _ := f.deps(nil).CreateInvitation(context.Background(), "ws_1", "p@acme.com", "member", "u_admin")
	// Member cannot revoke → 403.
	resp, _ := doWS(t, srv, "DELETE", "/v1/workspaces/ws_1/invitations/"+inv.ID, "member", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member revoke: %d, want 403", resp.StatusCode)
	}
	// Admin revokes → 204.
	resp, _ = doWS(t, srv, "DELETE", "/v1/workspaces/ws_1/invitations/"+inv.ID, "admin", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin revoke: %d, want 204", resp.StatusCode)
	}
}

// envCode pulls the machine code out of an error envelope body.
func envCode(body []byte) string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(body, &e)
	return e.Error.Code
}
