package identity_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResolveActiveWorkspace_HeaderPath asserts the §4.2 header branch:
// a header naming a workspace the user is a live member of resolves to it (with
// the right role); a header naming a workspace the user is NOT a member of
// returns ErrNotMember (the handler maps that to 403 — never a silent
// fallback).
func TestResolveActiveWorkspace_HeaderPath(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	owner := newUser(t, store, "owner-hp@example.com", "Owner", "gsub-hp-owner")
	other := newUser(t, store, "other-hp@example.com", "Other", "gsub-hp-other")
	ownerWS := identity.DefaultWorkspaceID(owner.ID)
	otherWS := identity.DefaultWorkspaceID(other.ID)

	// owner joins other's workspace as a plain member (multi-membership).
	if err := store.AddMember(ctx, otherWS, owner.ID, identity.RoleMember, other.ID); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// Header → own default workspace: admin role.
	ws, role, err := store.ResolveActiveWorkspace(ctx, owner.ID, ownerWS, "")
	if err != nil {
		t.Fatalf("header own ws: %v", err)
	}
	if ws.ID != ownerWS || role != identity.RoleAdmin {
		t.Fatalf("header own ws = (%s,%s), want (%s,admin)", ws.ID, role, ownerWS)
	}

	// Header → other's workspace (member): resolves with member role.
	ws, role, err = store.ResolveActiveWorkspace(ctx, owner.ID, otherWS, "")
	if err != nil {
		t.Fatalf("header other ws: %v", err)
	}
	if ws.ID != otherWS || role != identity.RoleMember {
		t.Fatalf("header other ws = (%s,%s), want (%s,member)", ws.ID, role, otherWS)
	}

	// Header → a workspace the user is NOT a member of: fail closed.
	stranger := newUser(t, store, "stranger-hp@example.com", "Stranger", "gsub-hp-stranger")
	strangerWS := identity.DefaultWorkspaceID(stranger.ID)
	if _, _, err := store.ResolveActiveWorkspace(ctx, owner.ID, strangerWS, ""); !errors.Is(err, identity.ErrNotMember) {
		t.Fatalf("header non-member ws err = %v, want ErrNotMember", err)
	}
}

// TestResolveActiveWorkspace_NoHeaderNeverFails asserts the no-header path
// always resolves to the user's default workspace (never 403s), advances
// last_active conditionally, and that a stale last_active (the user was removed
// from it) falls through to the default rather than erroring.
func TestResolveActiveWorkspace_NoHeaderNeverFails(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	owner := newUser(t, store, "owner-nh@example.com", "Owner", "gsub-nh-owner")
	other := newUser(t, store, "other-nh@example.com", "Other", "gsub-nh-other")
	ownerWS := identity.DefaultWorkspaceID(owner.ID)
	otherWS := identity.DefaultWorkspaceID(other.ID)

	token, err := store.CreateUserSession(ctx, owner.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}

	// No header, fresh session (no last_active) → default workspace, and
	// last_active gets stamped to it.
	ws, role, err := store.ResolveActiveWorkspace(ctx, owner.ID, "", token)
	if err != nil {
		t.Fatalf("no-header default: %v", err)
	}
	if ws.ID != ownerWS || role != identity.RoleAdmin {
		t.Fatalf("no-header default = (%s,%s), want (%s,admin)", ws.ID, role, ownerWS)
	}
	if got := sessionLastActive(t, pool, token); got != ownerWS {
		t.Fatalf("last_active after default resolve = %q, want %q", got, ownerWS)
	}

	// Stale last_active: point the session at other's workspace, then remove
	// owner from it. The no-header path must re-verify membership and fall
	// through to the default rather than honoring the dead pointer.
	if err := store.AddMember(ctx, otherWS, owner.ID, identity.RoleMember, other.ID); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	setSessionLastActive(t, pool, token, otherWS)
	if err := store.RemoveMember(ctx, otherWS, owner.ID); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	ws, role, err = store.ResolveActiveWorkspace(ctx, owner.ID, "", token)
	if err != nil {
		t.Fatalf("stale last_active fall-through: %v", err)
	}
	if ws.ID != ownerWS || role != identity.RoleAdmin {
		t.Fatalf("stale last_active should fall through to default, got (%s,%s)", ws.ID, role)
	}
}

// TestResolveActiveWorkspace_LastActiveHonoredWhenLive asserts a live
// last_active is honored when no header is present (UI convenience), with the
// caller's role in that workspace.
func TestResolveActiveWorkspace_LastActiveHonoredWhenLive(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	owner := newUser(t, store, "owner-la@example.com", "Owner", "gsub-la2-owner")
	other := newUser(t, store, "other-la@example.com", "Other", "gsub-la2-other")
	otherWS := identity.DefaultWorkspaceID(other.ID)

	if err := store.AddMember(ctx, otherWS, owner.ID, identity.RoleMember, other.ID); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	token, err := store.CreateUserSession(ctx, owner.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	setSessionLastActive(t, pool, token, otherWS)

	ws, role, err := store.ResolveActiveWorkspace(ctx, owner.ID, "", token)
	if err != nil {
		t.Fatalf("live last_active: %v", err)
	}
	if ws.ID != otherWS || role != identity.RoleMember {
		t.Fatalf("live last_active = (%s,%s), want (%s,member)", ws.ID, role, otherWS)
	}
}

// TestConcurrentLeave_LastAdminRace exercises the other ordering called out by
// the slice: two concurrent *leaves* (RemoveMember of self) by the two admins.
// The shared-row lock must let at most one succeed so the workspace never goes
// adminless.
func TestConcurrentLeave_LastAdminRace(t *testing.T) {
	ctx := context.Background()
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)

	a1 := newUser(t, store, "leave1@example.com", "One", "gsub-lv-one")
	a2 := newUser(t, store, "leave2@example.com", "Two", "gsub-lv-two")
	wsID := identity.DefaultWorkspaceID(a1.ID)
	if err := store.AddMember(ctx, wsID, a2.ID, identity.RoleAdmin, a1.ID); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	targets := []string{a1.ID, a2.ID}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = store.RemoveMember(ctx, wsID, targets[i])
		}(i)
	}
	wg.Wait()

	n, err := store.CountAdmins(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("workspace left adminless after concurrent leaves: %d admins", n)
	}
	lastAdminErrs := 0
	for _, e := range errs {
		if errors.Is(e, identity.ErrLastAdmin) {
			lastAdminErrs++
		} else if e != nil {
			t.Fatalf("unexpected leave error: %v", e)
		}
	}
	if lastAdminErrs != 1 {
		t.Fatalf("expected exactly 1 ErrLastAdmin under concurrent leave, got %d (admins=%d)", lastAdminErrs, n)
	}
}

// --- test-only direct session helpers (the conditional-write last_active path
// is exercised through ResolveActiveWorkspace; these poke the column directly
// to set up stale/live last_active scenarios). ---

func sessionLastActive(t *testing.T, pool *pgxpool.Pool, token string) string {
	t.Helper()
	var ws *string
	if err := pool.QueryRow(context.Background(),
		`SELECT last_active_workspace_id FROM user_sessions WHERE token = $1`, token,
	).Scan(&ws); err != nil {
		t.Fatalf("read last_active: %v", err)
	}
	if ws == nil {
		return ""
	}
	return *ws
}

func setSessionLastActive(t *testing.T, pool *pgxpool.Pool, token, wsID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE user_sessions SET last_active_workspace_id = $2 WHERE token = $1`, token, wsID,
	); err != nil {
		t.Fatalf("set last_active: %v", err)
	}
}
