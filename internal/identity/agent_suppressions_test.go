package identity_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
)

func suppressionUserAndAgent(t *testing.T, store *identity.Store, label string) (*identity.User, *identity.AgentIdentity) {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateOrGetUser(ctx, label+"@example.com", label, "google-"+label)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	a, err := store.CreateAgent(ctx, label+"@agents.e2a.dev", "agents.e2a.dev", label, "", "", u.ID)
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return u, a
}

func TestAgentSuppressionDuplicateNormalizesAndCallsHookOnce(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	u, a := suppressionUserAndAgent(t, store, "supp-duplicate")

	var calls atomic.Int32
	hook := func(_ context.Context, tx pgx.Tx, got identity.AgentSuppressionHookScope) error {
		calls.Add(1)
		var inTransaction bool
		if err := tx.QueryRow(ctx, `SELECT txid_current_if_assigned() IS NOT NULL`).Scan(&inTransaction); err != nil {
			return err
		}
		if !inTransaction || got.UserID != u.ID || got.AgentID != a.ID || got.Address != "recipient@example.com" || got.Source != "unsubscribe" {
			return fmt.Errorf("hook got %+v, inTransaction=%v", got, inTransaction)
		}
		return nil
	}

	first, added, err := store.AddAgentSuppression(ctx, u.ID, " SUPP-DUPLICATE@AGENTS.E2A.DEV ", " Recipient@Example.COM ", "asked", "unsubscribe", hook)
	if err != nil || !added {
		t.Fatalf("first AddAgentSuppression = (%+v, %v, %v), want added", first, added, err)
	}
	second, added, err := store.AddAgentSuppression(ctx, u.ID, a.ID, "recipient@example.com", "changed", "manual", hook)
	if err != nil || added {
		t.Fatalf("duplicate AddAgentSuppression = (%+v, %v, %v), want existing", second, added, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("hook calls = %d, want 1", calls.Load())
	}
	if second.Address != first.Address || second.Reason != first.Reason || second.Source != first.Source {
		t.Errorf("duplicate returned %+v, want original %+v", second, first)
	}
}

func TestAgentSuppressionRejectsAgentNotLiveAndOwnedByUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	owner, ownedAgent := suppressionUserAndAgent(t, store, "supp-owned")
	other, otherAgent := suppressionUserAndAgent(t, store, "supp-foreign")

	var hookCalls atomic.Int32
	hook := func(context.Context, pgx.Tx, identity.AgentSuppressionHookScope) error {
		hookCalls.Add(1)
		return nil
	}
	for _, tc := range []struct {
		name    string
		userID  string
		agentID string
	}{
		{name: "foreign", userID: owner.ID, agentID: " SUPP-FOREIGN@AGENTS.E2A.DEV "},
		{name: "nonexistent", userID: owner.ID, agentID: "missing@agents.e2a.dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, added, err := store.AddAgentSuppression(ctx, tc.userID, tc.agentID, tc.name+"@example.com", "", "manual", hook)
			if !errors.Is(err, identity.ErrAgentNotFound) || added {
				t.Fatalf("AddAgentSuppression = added %v, error %v; want false, ErrAgentNotFound", added, err)
			}
		})
	}

	if err := store.SoftDeleteAgent(ctx, ownedAgent.ID, owner.ID); err != nil {
		t.Fatalf("SoftDeleteAgent: %v", err)
	}
	_, added, err := store.AddAgentSuppression(ctx, owner.ID, ownedAgent.ID, "trashed@example.com", "", "manual", hook)
	if !errors.Is(err, identity.ErrAgentNotFound) || added {
		t.Fatalf("AddAgentSuppression(trashed) = added %v, error %v; want false, ErrAgentNotFound", added, err)
	}
	if hookCalls.Load() != 0 {
		t.Fatalf("hook called %d times for rejected agents", hookCalls.Load())
	}

	// Keep the foreign fixture live so the rejection specifically proves
	// ownership, rather than merely nonexistence.
	if got, err := store.GetAgentByID(ctx, otherAgent.ID); err != nil || got.UserID != other.ID {
		t.Fatalf("foreign fixture is not live: %+v, %v", got, err)
	}
}

func TestAgentSuppressionHookFailureRollsBackInsert(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	u, a := suppressionUserAndAgent(t, store, "supp-rollback")
	wantErr := errors.New("outbox unavailable")

	_, _, err := store.AddAgentSuppression(ctx, u.ID, a.ID, "rollback@example.com", "", "manual",
		func(context.Context, pgx.Tx, identity.AgentSuppressionHookScope) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("AddAgentSuppression error = %v, want %v", err, wantErr)
	}
	rows, err := store.ListAgentSuppressions(ctx, u.ID, a.ID, 10, identity.AgentSuppression{}.CreatedAt, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rolled-back suppression persisted: %+v", rows)
	}
}

func TestAgentSuppressionTenantAgentIsolationAndEffectiveDedup(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	u1, a1 := suppressionUserAndAgent(t, store, "supp-scope-one")
	u2, a2 := suppressionUserAndAgent(t, store, "supp-scope-two")
	a1b, err := store.CreateAgent(ctx, "other@agents.e2a.dev", "agents.e2a.dev", "other", "", "", u1.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ user, agent, address string }{
		{u1.ID, a1.ID, "shared@example.com"},
		{u1.ID, a1b.ID, "agent-b@example.com"},
		{u2.ID, a2.ID, "tenant-two@example.com"},
	} {
		if _, _, err := store.AddAgentSuppression(ctx, tc.user, tc.agent, tc.address, "", "manual", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AddSuppression(ctx, u1.ID, "shared@example.com", "bounce", "manual", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddSuppression(ctx, u1.ID, "account@example.com", "bounce", "manual", ""); err != nil {
		t.Fatal(err)
	}

	got, err := store.EffectiveSuppressions(ctx, u1.ID, " SUPP-SCOPE-ONE@AGENTS.E2A.DEV ", []string{
		" SHARED@EXAMPLE.COM ", "account@example.com", "agent-b@example.com", "tenant-two@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"shared@example.com": true, "account@example.com": true}
	if len(got) != len(want) {
		t.Fatalf("EffectiveSuppressions = %v, want exactly %v", got, want)
	}
	for _, address := range got {
		if !want[address] {
			t.Errorf("unexpected effective suppression %q", address)
		}
	}

	rows, err := store.ListAgentSuppressions(ctx, u1.ID, " SUPP-SCOPE-ONE@AGENTS.E2A.DEV ", 10, identity.AgentSuppression{}.CreatedAt, "")
	if err != nil || len(rows) != 1 || rows[0].Address != "shared@example.com" {
		t.Fatalf("scoped ListAgentSuppressions = %+v, %v", rows, err)
	}
}

func TestAgentSuppressionKeysetDeletionAndAgentRecreationPersistence(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	u, a := suppressionUserAndAgent(t, store, "supp-page")
	for _, address := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		if _, _, err := store.AddAgentSuppression(ctx, u.ID, a.ID, address, "", "manual", nil); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := store.ListAgentSuppressions(ctx, u.ID, a.ID, 2, identity.AgentSuppression{}.CreatedAt, "")
	if err != nil || len(page1) != 2 {
		t.Fatalf("page 1 = %+v, %v", page1, err)
	}
	last := page1[len(page1)-1]
	page2, err := store.ListAgentSuppressions(ctx, u.ID, a.ID, 2, last.CreatedAt, last.Address)
	if err != nil || len(page2) != 1 {
		t.Fatalf("page 2 = %+v, %v", page2, err)
	}
	seen := map[string]bool{}
	for _, row := range append(page1, page2...) {
		seen[row.Address] = true
	}
	if len(seen) != 3 {
		t.Fatalf("pagination repeated/skipped rows: pages=%+v / %+v", page1, page2)
	}

	removed, err := store.RemoveAgentSuppression(ctx, u.ID, " SUPP-PAGE@AGENTS.E2A.DEV ", " A@EXAMPLE.COM ")
	if err != nil || !removed {
		t.Fatalf("RemoveAgentSuppression = %v, %v", removed, err)
	}
	removed, err = store.RemoveAgentSuppression(ctx, u.ID, a.ID, "a@example.com")
	if err != nil || removed {
		t.Fatalf("second RemoveAgentSuppression = %v, %v", removed, err)
	}

	if _, err := store.DeleteAgent(ctx, a.ID, u.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if _, err := store.CreateAgent(ctx, a.ID, "agents.e2a.dev", "recreated", "", "", u.ID); err != nil {
		t.Fatalf("recreate agent: %v", err)
	}
	effective, err := store.EffectiveSuppressions(ctx, u.ID, a.ID, []string{"b@example.com", "c@example.com"})
	if err != nil || len(effective) != 2 {
		t.Fatalf("suppressions after agent recreation = %v, %v; want 2", effective, err)
	}
}

func TestUnsubscribeTokenHashLookupIdempotenceAndConcurrency(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	u, a := suppressionUserAndAgent(t, store, "unsubscribe-token")
	hash := bytes.Repeat([]byte{0x5a}, 32)

	const workers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- store.PutUnsubscribeToken(ctx, hash, u.ID, " UNSUBSCRIBE-TOKEN@AGENTS.E2A.DEV ", " Recipient@Example.COM ")
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent PutUnsubscribeToken: %v", err)
		}
	}
	// A hash already mapped to one scope must remain mapped there.
	if err := store.PutUnsubscribeToken(ctx, hash, u.ID, "different@agents.e2a.dev", "other@example.com"); err != nil {
		t.Fatalf("idempotent conflicting PutUnsubscribeToken: %v", err)
	}

	got, err := store.ResolveUnsubscribeToken(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	want := identity.UnsubscribeScope{UserID: u.ID, AgentID: a.ID, Address: "recipient@example.com"}
	if got == nil || *got != want {
		t.Fatalf("ResolveUnsubscribeToken = %+v, want %+v", got, want)
	}
	unknown, err := store.ResolveUnsubscribeToken(ctx, bytes.Repeat([]byte{0x6b}, 32))
	if err != nil || unknown != nil {
		t.Fatalf("unknown ResolveUnsubscribeToken = %+v, %v; want nil, nil", unknown, err)
	}
}

func TestAgentSuppressionFromTokenScopeSurvivesHardDeletedAgent(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	u, a := suppressionUserAndAgent(t, store, "unsubscribe-deleted-agent")
	hash := bytes.Repeat([]byte{0x7c}, 32)
	if err := store.PutUnsubscribeToken(ctx, hash, u.ID, " UNSUBSCRIBE-DELETED-AGENT@AGENTS.E2A.DEV ", " Person@Example.COM "); err != nil {
		t.Fatal(err)
	}
	scope, err := store.ResolveUnsubscribeToken(ctx, hash)
	if err != nil || scope == nil {
		t.Fatalf("ResolveUnsubscribeToken = %+v, %v", scope, err)
	}
	if _, err := store.DeleteAgent(ctx, a.ID, u.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	sp, added, err := store.AddAgentSuppressionFromTokenScope(ctx, *scope, nil)
	if err != nil || !added {
		t.Fatalf("AddAgentSuppressionFromTokenScope = %+v, %v, %v; want added", sp, added, err)
	}
	if sp.AgentEmail != a.ID || sp.Address != "person@example.com" || sp.Source != "unsubscribe" {
		t.Fatalf("token-authorized suppression = %+v", sp)
	}
	effective, err := store.EffectiveSuppressions(ctx, u.ID, a.ID, []string{"person@example.com"})
	if err != nil || len(effective) != 1 || effective[0] != "person@example.com" {
		t.Fatalf("EffectiveSuppressions after deleted-agent token = %v, %v", effective, err)
	}
}
