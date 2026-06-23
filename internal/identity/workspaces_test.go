package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// newUser creates a user through CreateOrGetUser (which provisions the personal
// workspace + admin membership + signing secret in one tx) and returns it.
func newUser(t *testing.T, store *identity.Store, email, name, sub string) *identity.User {
	t.Helper()
	u, err := store.CreateOrGetUser(context.Background(), email, name, sub)
	if err != nil {
		t.Fatalf("CreateOrGetUser(%s): %v", email, err)
	}
	return u
}

// TestCreateOrGetUser_ProvisionsPersonalWorkspace asserts a fresh user lands as
// admin of a deterministic personal workspace, and that the workspace + signing
// secret are stamped (no NULL workspace rows). Also covers the idempotent
// returning-login path (no double-provision).
func TestCreateOrGetUser_ProvisionsPersonalWorkspace(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	u := newUser(t, store, "founder@example.com", "Founder", "gsub-founder")

	wsID := identity.DefaultWorkspaceID(u.ID)
	w, err := store.GetWorkspace(ctx, wsID)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if w.Name != "Founder's Workspace" {
		t.Errorf("workspace name = %q, want %q", w.Name, "Founder's Workspace")
	}
	role, err := store.ResolveMembership(ctx, u.ID, wsID)
	if err != nil {
		t.Fatalf("ResolveMembership: %v", err)
	}
	if role != identity.RoleAdmin {
		t.Errorf("role = %q, want admin", role)
	}

	// Signing secret stamped with the workspace (never NULL).
	var nullSecrets int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM webhook_signing_secrets WHERE user_id = $1 AND workspace_id IS NULL`,
		u.ID).Scan(&nullSecrets); err != nil {
		t.Fatal(err)
	}
	if nullSecrets != 0 {
		t.Errorf("found %d NULL-workspace signing secrets", nullSecrets)
	}

	// Idempotent returning login: status=false, no duplicate workspace/secret.
	u2, isNew, err := store.CreateOrGetUserWithStatus(ctx, "founder@example.com", "Founder", "gsub-founder")
	if err != nil {
		t.Fatalf("returning CreateOrGetUserWithStatus: %v", err)
	}
	if isNew {
		t.Error("expected isNew=false on returning login")
	}
	if u2.ID != u.ID {
		t.Errorf("returning login changed id: %q != %q", u2.ID, u.ID)
	}
	var wsCount, secretCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspaces WHERE id = $1`, wsID).Scan(&wsCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webhook_signing_secrets WHERE user_id = $1`, u.ID).Scan(&secretCount); err != nil {
		t.Fatal(err)
	}
	if wsCount != 1 || secretCount != 1 {
		t.Fatalf("double-provision: workspaces=%d secrets=%d, want 1/1", wsCount, secretCount)
	}

	// First call's discriminant should report new.
	u3, isNew3, err := store.CreateOrGetUserWithStatus(ctx, "fresh@example.com", "Fresh", "gsub-fresh")
	if err != nil {
		t.Fatalf("fresh user: %v", err)
	}
	if !isNew3 {
		t.Error("expected isNew=true for a brand-new user")
	}
	if _, err := store.GetWorkspace(ctx, identity.DefaultWorkspaceID(u3.ID)); err != nil {
		t.Errorf("fresh user has no workspace: %v", err)
	}
}

// TestPersonalWorkspaceName_EmailFallback asserts the local-part fallback when
// the user has no display name.
func TestPersonalWorkspaceName_EmailFallback(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	u := newUser(t, store, "Noname@Example.com", "", "gsub-noname")
	w, err := store.GetWorkspace(ctx, identity.DefaultWorkspaceID(u.ID))
	if err != nil {
		t.Fatal(err)
	}
	if w.Name != "noname's Workspace" {
		t.Errorf("name = %q, want %q", w.Name, "noname's Workspace")
	}
}

// TestBootstrapUser_ProvisionsWorkspace asserts the bootstrap path also
// provisions a personal workspace + admin membership (B3).
func TestBootstrapUser_ProvisionsWorkspace(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	u, err := store.BootstrapUser(ctx, "boot@example.com")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	role, err := store.ResolveMembership(ctx, u.ID, identity.DefaultWorkspaceID(u.ID))
	if err != nil {
		t.Fatalf("ResolveMembership: %v", err)
	}
	if role != identity.RoleAdmin {
		t.Errorf("role = %q, want admin", role)
	}
	// Re-bootstrap is idempotent.
	if _, err := store.BootstrapUser(ctx, "boot@example.com"); err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}
	var wsCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspaces WHERE id = $1`,
		identity.DefaultWorkspaceID(u.ID)).Scan(&wsCount); err != nil {
		t.Fatal(err)
	}
	if wsCount != 1 {
		t.Fatalf("bootstrap double-provisioned: %d workspaces", wsCount)
	}
}

// TestListWorkspacesForUser asserts a user sees their default workspace plus
// any workspace they've joined, each annotated with their role.
func TestListWorkspacesForUser(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	owner := newUser(t, store, "owner@example.com", "Owner", "gsub-lw-owner")
	joiner := newUser(t, store, "joiner@example.com", "Joiner", "gsub-lw-joiner")

	ownerWS := identity.DefaultWorkspaceID(owner.ID)
	if err := store.AddMember(ctx, ownerWS, joiner.ID, identity.RoleMember, owner.ID); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	wss, roles, err := store.ListWorkspacesForUser(ctx, joiner.ID)
	if err != nil {
		t.Fatalf("ListWorkspacesForUser: %v", err)
	}
	if len(wss) != 2 {
		t.Fatalf("joiner belongs to %d workspaces, want 2", len(wss))
	}
	// Find the owner's workspace and assert member role.
	found := false
	for i, w := range wss {
		if w.ID == ownerWS {
			found = true
			if roles[i] != identity.RoleMember {
				t.Errorf("joiner role in owner ws = %q, want member", roles[i])
			}
		}
	}
	if !found {
		t.Error("joiner does not see owner's workspace")
	}
}

// TestRenameWorkspace asserts rename + not-found.
func TestRenameWorkspace(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	u := newUser(t, store, "rn@example.com", "RN", "gsub-rn")
	wsID := identity.DefaultWorkspaceID(u.ID)
	if err := store.RenameWorkspace(ctx, wsID, "Acme"); err != nil {
		t.Fatalf("RenameWorkspace: %v", err)
	}
	w, _ := store.GetWorkspace(ctx, wsID)
	if w.Name != "Acme" {
		t.Errorf("name = %q, want Acme", w.Name)
	}
	if err := store.RenameWorkspace(ctx, "ws_nonexistent", "X"); !errors.Is(err, identity.ErrWorkspaceNotFound) {
		t.Errorf("rename missing ws err = %v, want ErrWorkspaceNotFound", err)
	}
}

// TestMembership_SetRoleAndRemove covers role changes, the idempotent no-op,
// remove/leave, and ErrNotMember.
func TestMembership_SetRoleAndRemove(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	admin := newUser(t, store, "a@example.com", "Admin", "gsub-mc-admin")
	member := newUser(t, store, "m@example.com", "Member", "gsub-mc-member")
	wsID := identity.DefaultWorkspaceID(admin.ID)
	if err := store.AddMember(ctx, wsID, member.ID, identity.RoleMember, admin.ID); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// Promote member → admin.
	if err := store.SetMemberRole(ctx, wsID, member.ID, identity.RoleAdmin); err != nil {
		t.Fatalf("promote: %v", err)
	}
	role, _ := store.ResolveMembership(ctx, member.ID, wsID)
	if role != identity.RoleAdmin {
		t.Fatalf("role = %q, want admin", role)
	}

	// Idempotent same-role no-op.
	if err := store.SetMemberRole(ctx, wsID, member.ID, identity.RoleAdmin); err != nil {
		t.Fatalf("idempotent set-role: %v", err)
	}

	// CountAdmins now 2; demote back to member is allowed.
	n, _ := store.CountAdmins(ctx, wsID)
	if n != 2 {
		t.Fatalf("admins = %d, want 2", n)
	}
	if err := store.SetMemberRole(ctx, wsID, member.ID, identity.RoleMember); err != nil {
		t.Fatalf("demote: %v", err)
	}

	// Remove the member (leave).
	if err := store.RemoveMember(ctx, wsID, member.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := store.ResolveMembership(ctx, member.ID, wsID); !errors.Is(err, identity.ErrNotMember) {
		t.Errorf("after remove err = %v, want ErrNotMember", err)
	}

	// SetRole / Remove on a non-member → ErrNotMember.
	if err := store.SetMemberRole(ctx, wsID, member.ID, identity.RoleAdmin); !errors.Is(err, identity.ErrNotMember) {
		t.Errorf("set-role non-member err = %v, want ErrNotMember", err)
	}
	if err := store.RemoveMember(ctx, wsID, member.ID); !errors.Is(err, identity.ErrNotMember) {
		t.Errorf("remove non-member err = %v, want ErrNotMember", err)
	}
}

// TestLastAdminGuard asserts the sole admin cannot be demoted or removed, and
// that two concurrent demotes (both orderings) can never strand the workspace
// adminless under the shared-row lock (§5, B1).
func TestLastAdminGuard(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	admin := newUser(t, store, "soleadmin@example.com", "Sole", "gsub-la-sole")
	wsID := identity.DefaultWorkspaceID(admin.ID)

	// Sole admin cannot demote self.
	if err := store.SetMemberRole(ctx, wsID, admin.ID, identity.RoleMember); !errors.Is(err, identity.ErrLastAdmin) {
		t.Errorf("demote sole admin err = %v, want ErrLastAdmin", err)
	}
	// Sole admin cannot be removed.
	if err := store.RemoveMember(ctx, wsID, admin.ID); !errors.Is(err, identity.ErrLastAdmin) {
		t.Errorf("remove sole admin err = %v, want ErrLastAdmin", err)
	}

	// Two admins, two concurrent demotes: at most one succeeds; at least one
	// admin always remains.
	admin2 := newUser(t, store, "admin2@example.com", "Two", "gsub-la-two")
	if err := store.AddMember(ctx, wsID, admin2.ID, identity.RoleAdmin, admin.ID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	targets := []string{admin.ID, admin2.ID}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = store.SetMemberRole(ctx, wsID, targets[i], identity.RoleMember)
		}(i)
	}
	wg.Wait()

	n, err := store.CountAdmins(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("workspace left adminless: %d admins after concurrent demotes", n)
	}
	// Exactly one demote should have succeeded (the other hits the guard).
	lastAdminErrs := 0
	for _, e := range errs {
		if errors.Is(e, identity.ErrLastAdmin) {
			lastAdminErrs++
		} else if e != nil {
			t.Fatalf("unexpected demote error: %v", e)
		}
	}
	if lastAdminErrs != 1 {
		t.Fatalf("expected exactly 1 ErrLastAdmin, got %d (admins remaining=%d)", lastAdminErrs, n)
	}
}

// TestInvitation_AcceptFlow covers create, get-by-token, accept (membership +
// status flip), idempotent double-accept, email mismatch, and torn-down/revoke
// → not-found.
func TestInvitation_AcceptFlow(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	admin := newUser(t, store, "inv-admin@example.com", "Admin", "gsub-inv-admin")
	wsID := identity.DefaultWorkspaceID(admin.ID)

	inv, err := store.CreateInvitation(ctx, wsID, identity.NormalizeEmail("Invitee@Example.com"), identity.RoleMember, admin.ID)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	if inv.PlaintextToken == "" {
		t.Fatal("expected a plaintext token on creation")
	}

	// Get-by-token resolves the live pending invite.
	got, err := store.GetInvitationByToken(ctx, inv.PlaintextToken)
	if err != nil {
		t.Fatalf("GetInvitationByToken: %v", err)
	}
	if got.ID != inv.ID || got.WorkspaceID != wsID {
		t.Fatalf("token resolved to wrong invite: %+v", got)
	}

	// List pending shows it.
	pending, err := store.ListPendingInvitations(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	// The invitee signs up (own workspace) and accepts.
	invitee := newUser(t, store, "invitee@example.com", "Invitee", "gsub-invitee")
	m, err := store.AcceptInvitation(ctx, inv.PlaintextToken, invitee.ID, "invitee@example.com")
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	if m.WorkspaceID != wsID || m.UserID != invitee.ID || m.Role != identity.RoleMember {
		t.Fatalf("membership = %+v", m)
	}
	// Status flipped → accepted; no longer pending.
	pending, _ = store.ListPendingInvitations(ctx, wsID)
	if len(pending) != 0 {
		t.Fatalf("after accept pending = %d, want 0", len(pending))
	}

	// Idempotent double-accept (same token, already joined) → 200, no error.
	m2, err := store.AcceptInvitation(ctx, inv.PlaintextToken, invitee.ID, "invitee@example.com")
	if err != nil {
		t.Fatalf("double-accept should be idempotent: %v", err)
	}
	if m2.UserID != invitee.ID {
		t.Fatalf("double-accept member = %+v", m2)
	}

	// Email mismatch → ErrInvitationEmailMismatch.
	inv2, _ := store.CreateInvitation(ctx, wsID, "target@example.com", identity.RoleMember, admin.ID)
	wrong := newUser(t, store, "wrong@example.com", "Wrong", "gsub-wrong")
	if _, err := store.AcceptInvitation(ctx, inv2.PlaintextToken, wrong.ID, "wrong@example.com"); !errors.Is(err, identity.ErrInvitationEmailMismatch) {
		t.Errorf("email mismatch err = %v, want ErrInvitationEmailMismatch", err)
	}

	// Revoke → token resolves to gone.
	if err := store.RevokeInvitation(ctx, wsID, inv2.ID); err != nil {
		t.Fatalf("RevokeInvitation: %v", err)
	}
	if _, err := store.GetInvitationByToken(ctx, inv2.PlaintextToken); !errors.Is(err, identity.ErrInvitationNotFound) {
		t.Errorf("revoked token get err = %v, want ErrInvitationNotFound", err)
	}
	target := newUser(t, store, "target@example.com", "Target", "gsub-target")
	if _, err := store.AcceptInvitation(ctx, inv2.PlaintextToken, target.ID, "target@example.com"); !errors.Is(err, identity.ErrInvitationNotFound) {
		t.Errorf("accept revoked err = %v, want ErrInvitationNotFound", err)
	}
	// Revoking an already-revoked invite → not-found (idempotent).
	if err := store.RevokeInvitation(ctx, wsID, inv2.ID); !errors.Is(err, identity.ErrInvitationNotFound) {
		t.Errorf("re-revoke err = %v, want ErrInvitationNotFound", err)
	}

	// Unknown token → not-found.
	if _, err := store.GetInvitationByToken(ctx, "e2a_inv_deadbeef"); !errors.Is(err, identity.ErrInvitationNotFound) {
		t.Errorf("unknown token err = %v, want ErrInvitationNotFound", err)
	}
}

// TestInvitation_ReinviteUpsertsPending asserts re-inviting the same email
// rotates the pending row (single pending invite per workspace+email) and the
// old token stops working.
func TestInvitation_ReinviteUpsertsPending(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	admin := newUser(t, store, "reinv-admin@example.com", "Admin", "gsub-reinv")
	wsID := identity.DefaultWorkspaceID(admin.ID)

	inv1, err := store.CreateInvitation(ctx, wsID, "x@example.com", identity.RoleMember, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	inv2, err := store.CreateInvitation(ctx, wsID, "x@example.com", identity.RoleAdmin, admin.ID)
	if err != nil {
		t.Fatalf("re-invite: %v", err)
	}
	if inv1.ID != inv2.ID {
		t.Errorf("re-invite created a new row (%q != %q); expected upsert", inv1.ID, inv2.ID)
	}

	pending, _ := store.ListPendingInvitations(ctx, wsID)
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1 after re-invite", len(pending))
	}
	if pending[0].Role != identity.RoleAdmin {
		t.Errorf("re-invite role = %q, want admin", pending[0].Role)
	}

	// The old token no longer resolves (rotated).
	if _, err := store.GetInvitationByToken(ctx, inv1.PlaintextToken); !errors.Is(err, identity.ErrInvitationNotFound) {
		t.Errorf("old token still valid after re-invite: %v", err)
	}
	if _, err := store.GetInvitationByToken(ctx, inv2.PlaintextToken); err != nil {
		t.Errorf("new token should resolve: %v", err)
	}
}

// TestGetPrincipalByAPIKey_ResolvesWorkspaceAndRole asserts a key resolves to a
// member-capped principal whose workspace is intrinsic (api_keys.workspace_id).
func TestGetPrincipalByAPIKey_ResolvesWorkspaceAndRole(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	u := newUser(t, store, "keyer@example.com", "Keyer", "gsub-keyer")
	key, err := store.CreateAPIKey(ctx, u.ID, "ci", nil)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	p, err := store.GetPrincipalByAPIKey(ctx, key.PlaintextKey)
	if err != nil {
		t.Fatalf("GetPrincipalByAPIKey: %v", err)
	}
	if p.Role != identity.RoleMember {
		t.Errorf("key principal role = %q, want member (member-capped)", p.Role)
	}
	if p.Workspace == nil || p.Workspace.ID != identity.DefaultWorkspaceID(u.ID) {
		t.Errorf("key workspace not resolved intrinsically: %+v", p.Workspace)
	}
}
