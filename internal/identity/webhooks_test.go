package identity_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/testutil"
)

func webhookTestUser(t *testing.T, store *identity.Store, prefix string) string {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	return u.ID
}

func TestCreateWebhook_RoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-create")

	filters := identity.WebhookFilters{
		AgentIDs: []string{"bot@example.com"},
		Labels:   []string{"urgent"},
	}
	got, err := store.CreateWebhook(ctx, userID, "https://example.com/hook", "main", []string{"email.received"}, filters)
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	if got.ID == "" || got.SigningSecret == "" {
		t.Errorf("CreateWebhook returned empty id/secret: %+v", got)
	}
	if got.URL != "https://example.com/hook" {
		t.Errorf("URL = %q", got.URL)
	}
	if !got.Enabled {
		t.Error("Enabled = false on fresh webhook; want true")
	}

	// Round-trip via GetWebhookByID
	round, err := store.GetWebhookByID(ctx, got.ID, userID)
	if err != nil {
		t.Fatalf("GetWebhookByID: %v", err)
	}
	if round.SigningSecret != got.SigningSecret {
		t.Errorf("signing_secret round-trip diverged")
	}
	if !reflect.DeepEqual(round.Filters.AgentIDs, []string{"bot@example.com"}) {
		t.Errorf("filters.AgentIDs round-trip = %v", round.Filters.AgentIDs)
	}
	if !reflect.DeepEqual(round.Events, []string{"email.received"}) {
		t.Errorf("events round-trip = %v", round.Events)
	}
}

func TestGetWebhookByID_CrossUserNotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := webhookTestUser(t, store, "wh-iso-a")
	userB := webhookTestUser(t, store, "wh-iso-b")

	w, _ := store.CreateWebhook(ctx, userA, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})

	_, err := store.GetWebhookByID(ctx, w.ID, userB)
	if !errors.Is(err, identity.ErrWebhookNotFound) {
		t.Errorf("cross-user read err = %v, want ErrWebhookNotFound", err)
	}

	// And the owner still sees it.
	if _, err := store.GetWebhookByID(ctx, w.ID, userA); err != nil {
		t.Errorf("owner read err = %v, want nil", err)
	}
}

func TestListWebhooksByUser_ScopesByOwner(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := webhookTestUser(t, store, "wh-list-a")
	userB := webhookTestUser(t, store, "wh-list-b")

	store.CreateWebhook(ctx, userA, "https://a1.example.com", "", []string{"email.received"}, identity.WebhookFilters{})
	store.CreateWebhook(ctx, userA, "https://a2.example.com", "", []string{"email.received"}, identity.WebhookFilters{})
	store.CreateWebhook(ctx, userB, "https://b1.example.com", "", []string{"email.received"}, identity.WebhookFilters{})

	listA, err := store.ListWebhooksByUser(ctx, userA, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("ListWebhooksByUser A: %v", err)
	}
	if len(listA) != 2 {
		t.Errorf("user A sees %d webhooks, want 2", len(listA))
	}
	listB, _ := store.ListWebhooksByUser(ctx, userB, 0, time.Time{}, "")
	if len(listB) != 1 {
		t.Errorf("user B sees %d webhooks, want 1", len(listB))
	}
}

func TestListEnabledWebhooksForRouting_MatchesEventType(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-route")

	store.CreateWebhook(ctx, userID, "https://recv.example.com", "", []string{"email.received"}, identity.WebhookFilters{})
	store.CreateWebhook(ctx, userID, "https://sent.example.com", "", []string{"email.sent"}, identity.WebhookFilters{})
	store.CreateWebhook(ctx, userID, "https://both.example.com", "", []string{"email.received", "email.sent"}, identity.WebhookFilters{})

	matches, err := store.ListEnabledWebhooksForRouting(ctx, userID, "email.received")
	if err != nil {
		t.Fatalf("ListEnabledWebhooksForRouting: %v", err)
	}
	urls := []string{}
	for _, w := range matches {
		urls = append(urls, w.URL)
	}
	sort.Strings(urls)
	want := []string{"https://both.example.com", "https://recv.example.com"}
	if !reflect.DeepEqual(urls, want) {
		t.Errorf("matched urls = %v, want %v", urls, want)
	}
}

func TestListEnabledWebhooksForRouting_SkipsDisabled(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-route-disabled")

	w1, _ := store.CreateWebhook(ctx, userID, "https://enabled.example.com", "", []string{"email.received"}, identity.WebhookFilters{})
	w2, _ := store.CreateWebhook(ctx, userID, "https://disabled.example.com", "", []string{"email.received"}, identity.WebhookFilters{})

	// Disable w2 by direct UPDATE (slice 2 will add the proper PATCH path).
	_, err := pool.Exec(ctx, `UPDATE webhooks SET enabled = false WHERE id = $1`, w2.ID)
	if err != nil {
		t.Fatalf("disable w2: %v", err)
	}

	matches, _ := store.ListEnabledWebhooksForRouting(ctx, userID, "email.received")
	if len(matches) != 1 || matches[0].ID != w1.ID {
		t.Errorf("expected only enabled webhook, got %d matches", len(matches))
	}
}

func TestListEnabledWebhooksForRouting_CrossUserIsolation(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := webhookTestUser(t, store, "wh-route-iso-a")
	userB := webhookTestUser(t, store, "wh-route-iso-b")

	store.CreateWebhook(ctx, userA, "https://a.example.com", "", []string{"email.received"}, identity.WebhookFilters{})

	// User B asking for the same event type sees none of A's webhooks.
	matchesB, _ := store.ListEnabledWebhooksForRouting(ctx, userB, "email.received")
	if len(matchesB) != 0 {
		t.Errorf("user B sees %d of A's webhooks; must be 0", len(matchesB))
	}
}

func TestCreateWebhook_EnforcesPerUserCap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-cap")

	// Lower the cap for this user so we don't have to create 50 rows.
	// account_limits has several NOT NULL columns without defaults
	// (max_agents, max_domains, max_messages_month, max_storage_bytes)
	// — seed reasonable values so the row passes constraints.
	_, err := pool.Exec(ctx,
		`INSERT INTO account_limits (user_id, plan_code, max_agents, max_domains, max_messages_month, max_storage_bytes, max_webhooks)
		 VALUES ($1, 'test', 10, 10, 100000, 1073741824, 2)
		 ON CONFLICT (user_id) DO UPDATE SET max_webhooks = 2`, userID)
	if err != nil {
		t.Fatalf("seed account_limits: %v", err)
	}

	if _, err := store.CreateWebhook(ctx, userID, "https://a.example.com", "", []string{"email.received"}, identity.WebhookFilters{}); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := store.CreateWebhook(ctx, userID, "https://b.example.com", "", []string{"email.received"}, identity.WebhookFilters{}); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	_, err = store.CreateWebhook(ctx, userID, "https://c.example.com", "", []string{"email.received"}, identity.WebhookFilters{})
	if !errors.Is(err, identity.ErrWebhookCapReached) {
		t.Errorf("create at cap+1 err = %v, want ErrWebhookCapReached", err)
	}
}

func TestCreateWebhook_DefaultCapWhenNoAccountLimits(t *testing.T) {
	// Users without an account_limits row should default to
	// DefaultMaxWebhooks. Tests on a fresh DB hit this path.
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-cap-default")

	max, err := store.MaxWebhooksForUser(ctx, userID)
	if err != nil {
		t.Fatalf("MaxWebhooksForUser: %v", err)
	}
	if max != identity.DefaultMaxWebhooks {
		t.Errorf("MaxWebhooksForUser without account_limits row = %d, want %d", max, identity.DefaultMaxWebhooks)
	}
}

func TestUpdateWebhook_PartialFields(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-upd")
	w, _ := store.CreateWebhook(ctx, userID, "https://a.example.com", "first", []string{"email.received"}, identity.WebhookFilters{})

	newURL := "https://b.example.com"
	newDesc := "updated"
	got, err := store.UpdateWebhook(ctx, w.ID, userID, identity.WebhookUpdate{URL: &newURL, Description: &newDesc})
	if err != nil {
		t.Fatalf("UpdateWebhook: %v", err)
	}
	if got.URL != newURL || got.Description != newDesc {
		t.Errorf("after update url=%q desc=%q", got.URL, got.Description)
	}
	// Events and filters unchanged.
	if len(got.Events) != 1 || got.Events[0] != "email.received" {
		t.Errorf("events drifted: %v", got.Events)
	}
}

func TestUpdateWebhook_ReEnableCooldown(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-cooldown")
	w, _ := store.CreateWebhook(ctx, userID, "https://a.example.com", "", []string{"email.received"}, identity.WebhookFilters{})

	// Simulate auto-disable that just happened.
	_, _ = pool.Exec(ctx, `UPDATE webhooks SET enabled = false, auto_disabled_at = now() WHERE id = $1`, w.ID)

	tval := true
	_, err := store.UpdateWebhook(ctx, w.ID, userID, identity.WebhookUpdate{Enabled: &tval})
	if !errors.Is(err, identity.ErrWebhookCooldown) {
		t.Errorf("expected ErrWebhookCooldown, got %v", err)
	}

	// Simulate an auto_disabled_at older than 5 min — re-enable should succeed.
	_, _ = pool.Exec(ctx, `UPDATE webhooks SET auto_disabled_at = now() - interval '10 minutes' WHERE id = $1`, w.ID)
	got, err := store.UpdateWebhook(ctx, w.ID, userID, identity.WebhookUpdate{Enabled: &tval})
	if err != nil {
		t.Fatalf("re-enable after cooldown: %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled = false after successful re-enable")
	}
	if got.AutoDisabledAt != nil {
		t.Error("auto_disabled_at not cleared after re-enable")
	}
}

func TestRotateSecret_ReturnsNewPlaintext(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-rotate")
	w, _ := store.CreateWebhook(ctx, userID, "https://a.example.com", "", []string{"email.received"}, identity.WebhookFilters{})
	originalSecret := w.SigningSecret

	newSecret, expiresAt, err := store.RotateSecret(ctx, w.ID, userID)
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	if newSecret == "" || newSecret == originalSecret {
		t.Errorf("new secret should differ from original: new=%q orig=%q", newSecret, originalSecret)
	}
	if expiresAt.IsZero() {
		t.Error("prev_expires_at is zero")
	}

	// Read back: signing_secret should be new, signing_secret_prev should be old.
	round, _ := store.GetWebhookByID(ctx, w.ID, userID)
	if round.SigningSecret != newSecret {
		t.Errorf("read-back current = %q, want %q", round.SigningSecret, newSecret)
	}
	if round.SigningSecretPrev != originalSecret {
		t.Errorf("read-back prev = %q, want %q (original)", round.SigningSecretPrev, originalSecret)
	}
}

func TestRotateSecret_CrossUserNotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := webhookTestUser(t, store, "wh-rot-a")
	userB := webhookTestUser(t, store, "wh-rot-b")
	w, _ := store.CreateWebhook(ctx, userA, "https://a.example.com", "", []string{"email.received"}, identity.WebhookFilters{})

	_, _, err := store.RotateSecret(ctx, w.ID, userB)
	if !errors.Is(err, identity.ErrWebhookNotFound) {
		t.Errorf("cross-user rotate err = %v, want ErrWebhookNotFound", err)
	}
}

func TestDeleteWebhook_OwnerOnly(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := webhookTestUser(t, store, "wh-del-a")
	userB := webhookTestUser(t, store, "wh-del-b")
	w, _ := store.CreateWebhook(ctx, userA, "https://a.example.com", "", []string{"email.received"}, identity.WebhookFilters{})

	// User B can't delete.
	if err := store.DeleteWebhook(ctx, w.ID, userB); !errors.Is(err, identity.ErrWebhookNotFound) {
		t.Errorf("cross-user delete err = %v, want ErrWebhookNotFound", err)
	}
	// Owner can.
	if err := store.DeleteWebhook(ctx, w.ID, userA); err != nil {
		t.Errorf("owner delete err = %v, want nil", err)
	}
	// Second delete is idempotent: not-found, not silent success.
	if err := store.DeleteWebhook(ctx, w.ID, userA); !errors.Is(err, identity.ErrWebhookNotFound) {
		t.Errorf("delete-already-gone err = %v, want ErrWebhookNotFound", err)
	}
}

// CreateWebhookIdem must run the idempotency completer INSIDE the insert
// transaction: the completer's tx observes the not-yet-committed webhook row,
// and the row + the completion commit together (the createWebhook retry-safety
// requirement — no window where a webhook exists without a replayable cached
// response).
func TestCreateWebhookIdem_CompleterRunsInInsertTx(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-idem-tx")

	var sawInsertInTx bool
	wh, err := store.CreateWebhookIdem(ctx, userID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{},
		func(ctx context.Context, tx pgx.Tx, w *identity.Webhook) error {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM webhooks WHERE id = $1`, w.ID).Scan(&n); err != nil {
				return err
			}
			sawInsertInTx = n == 1
			return nil
		})
	if err != nil {
		t.Fatalf("CreateWebhookIdem: %v", err)
	}
	if !sawInsertInTx {
		t.Error("completer must observe the webhook insert within its own transaction")
	}
	if _, err := store.GetWebhookByID(ctx, wh.ID, userID); err != nil {
		t.Errorf("webhook must be committed after CreateWebhookIdem: %v", err)
	}
}

// A completer failure must abort the whole create: no webhook row may commit
// without its idempotency completion (the atomic pairing is the point).
func TestCreateWebhookIdem_CompleterErrorRollsBackInsert(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-idem-rb")

	sentinel := errors.New("idempotency completion failed")
	var attemptedID string
	_, err := store.CreateWebhookIdem(ctx, userID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{},
		func(_ context.Context, _ pgx.Tx, w *identity.Webhook) error {
			attemptedID = w.ID
			return sentinel
		})
	if !errors.Is(err, sentinel) {
		t.Fatalf("CreateWebhookIdem err = %v, want the completer's error", err)
	}
	if attemptedID == "" {
		t.Fatal("completer was never invoked")
	}
	if _, gerr := store.GetWebhookByID(ctx, attemptedID, userID); !errors.Is(gerr, identity.ErrWebhookNotFound) {
		t.Errorf("completer failure must roll back the webhook insert; GetWebhookByID err = %v, want ErrWebhookNotFound", gerr)
	}
}

// A nil completer keeps the plain single-statement create path byte-identical
// in behavior (CreateWebhook delegates with nil).
func TestCreateWebhookIdem_NilCompleter(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := webhookTestUser(t, store, "wh-idem-nil")

	wh, err := store.CreateWebhookIdem(ctx, userID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{}, nil)
	if err != nil {
		t.Fatalf("CreateWebhookIdem(nil completer): %v", err)
	}
	if wh.ID == "" || wh.SigningSecret == "" {
		t.Errorf("empty id/secret: %+v", wh)
	}
	if _, err := store.GetWebhookByID(ctx, wh.ID, userID); err != nil {
		t.Errorf("GetWebhookByID: %v", err)
	}
}
