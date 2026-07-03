package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

func templateTestUser(t *testing.T, store *identity.Store, prefix string) string {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateOrGetUser(ctx, "owner-"+prefix+"@example.com", "Owner", "google-"+prefix)
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	return u.ID
}

func TestCreateTemplate_RoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-create")

	got, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Welcome", Alias: "welcome", Subject: "Hi {{name}}", Body: "Hello {{name}}!", HTMLBody: "<p>Hello {{name}}!</p>"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if got.ID == "" || got.ID[:5] != "tmpl_" {
		t.Errorf("CreateTemplate id = %q, want tmpl_ prefix", got.ID)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set: %+v", got)
	}

	round, err := store.GetTemplateByID(ctx, got.ID, userID)
	if err != nil {
		t.Fatalf("GetTemplateByID: %v", err)
	}
	if round.Name != "Welcome" || round.Alias != "welcome" || round.Subject != "Hi {{name}}" ||
		round.Body != "Hello {{name}}!" || round.HTMLBody != "<p>Hello {{name}}!</p>" {
		t.Errorf("round-trip diverged: %+v", round)
	}
}

func TestCreateTemplate_NoAliasNoHTML(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-minimal")

	got, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Bare", Subject: "S", Body: "B"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	round, err := store.GetTemplateByID(ctx, got.ID, userID)
	if err != nil {
		t.Fatalf("GetTemplateByID: %v", err)
	}
	if round.Alias != "" || round.HTMLBody != "" {
		t.Errorf("empty alias/html should round-trip as empty, got alias=%q html=%q", round.Alias, round.HTMLBody)
	}

	// The alias NULL storage means two alias-less templates never collide.
	if _, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Bare2", Subject: "S", Body: "B"}); err != nil {
		t.Errorf("second alias-less template err = %v, want nil (NULL aliases must not collide)", err)
	}
}

func TestCreateTemplate_StarterProvenanceRoundTrip(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-prov")

	got, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{
		Name: "From starter", Subject: "S", Body: "B",
		FromStarterAlias: "receipt", FromStarterVersion: "1.2.0",
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	round, err := store.GetTemplateByID(ctx, got.ID, userID)
	if err != nil {
		t.Fatalf("GetTemplateByID: %v", err)
	}
	if round.FromStarterAlias != "receipt" || round.FromStarterVersion != "1.2.0" {
		t.Errorf("provenance did not round-trip: %+v", round)
	}

	// Literal creates round-trip empty provenance (SQL NULL).
	lit, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Literal", Subject: "S", Body: "B"})
	if err != nil {
		t.Fatalf("CreateTemplate literal: %v", err)
	}
	round, err = store.GetTemplateByID(ctx, lit.ID, userID)
	if err != nil {
		t.Fatalf("GetTemplateByID literal: %v", err)
	}
	if round.FromStarterAlias != "" || round.FromStarterVersion != "" {
		t.Errorf("literal create must have empty provenance, got %+v", round)
	}
}

func TestCreateTemplate_AliasTakenSameUser(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-alias-dup")

	if _, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "One", Alias: "onboarding", Subject: "S", Body: "B"}); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	_, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Two", Alias: "onboarding", Subject: "S", Body: "B"})
	if !errors.Is(err, identity.ErrTemplateAliasTaken) {
		t.Errorf("duplicate alias err = %v, want ErrTemplateAliasTaken", err)
	}
}

func TestCreateTemplate_SameAliasDifferentUsersOK(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := templateTestUser(t, store, "tmpl-alias-a")
	userB := templateTestUser(t, store, "tmpl-alias-b")

	if _, err := store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "A", Alias: "shared", Subject: "S", Body: "B"}); err != nil {
		t.Fatalf("user A create: %v", err)
	}
	if _, err := store.CreateTemplate(ctx, userB, identity.TemplateCreate{Name: "B", Alias: "shared", Subject: "S", Body: "B"}); err != nil {
		t.Errorf("user B same alias err = %v, want nil (alias is per-user)", err)
	}
}

func TestGetTemplateByID_CrossUserNotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := templateTestUser(t, store, "tmpl-iso-a")
	userB := templateTestUser(t, store, "tmpl-iso-b")

	tp, _ := store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "Mine", Subject: "S", Body: "B"})

	if _, err := store.GetTemplateByID(ctx, tp.ID, userB); !errors.Is(err, identity.ErrTemplateNotFound) {
		t.Errorf("cross-user read err = %v, want ErrTemplateNotFound", err)
	}
	if _, err := store.GetTemplateByID(ctx, tp.ID, userA); err != nil {
		t.Errorf("owner read err = %v, want nil", err)
	}
}

func TestGetTemplateByAlias(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := templateTestUser(t, store, "tmpl-byalias-a")
	userB := templateTestUser(t, store, "tmpl-byalias-b")

	tp, _ := store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "Aliased", Alias: "invoice", Subject: "S", Body: "B"})

	got, err := store.GetTemplateByAlias(ctx, "invoice", userA)
	if err != nil {
		t.Fatalf("GetTemplateByAlias: %v", err)
	}
	if got.ID != tp.ID {
		t.Errorf("resolved id = %q, want %q", got.ID, tp.ID)
	}
	// Cross-user alias resolution behaves as not-found.
	if _, err := store.GetTemplateByAlias(ctx, "invoice", userB); !errors.Is(err, identity.ErrTemplateNotFound) {
		t.Errorf("cross-user alias err = %v, want ErrTemplateNotFound", err)
	}
	if _, err := store.GetTemplateByAlias(ctx, "missing", userA); !errors.Is(err, identity.ErrTemplateNotFound) {
		t.Errorf("missing alias err = %v, want ErrTemplateNotFound", err)
	}
}

func TestListTemplatesByUser_ScopesByOwner(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := templateTestUser(t, store, "tmpl-list-a")
	userB := templateTestUser(t, store, "tmpl-list-b")

	store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "A1", Subject: "S", Body: "B"})
	store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "A2", Subject: "S", Body: "B"})
	store.CreateTemplate(ctx, userB, identity.TemplateCreate{Name: "B1", Subject: "S", Body: "B"})

	listA, err := store.ListTemplatesByUser(ctx, userA)
	if err != nil {
		t.Fatalf("ListTemplatesByUser A: %v", err)
	}
	if len(listA) != 2 {
		t.Errorf("user A sees %d templates, want 2", len(listA))
	}
	listB, _ := store.ListTemplatesByUser(ctx, userB)
	if len(listB) != 1 {
		t.Errorf("user B sees %d templates, want 1", len(listB))
	}
}

func TestListTemplatesByUser_SummaryShape(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-list-shape")

	created, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Shaped", Alias: "shaped", Subject: "Subject {{x}}", Body: "Body {{x}}", HTMLBody: "<p>{{x}}</p>"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	list, err := store.ListTemplatesByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListTemplatesByUser: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 summary, got %d", len(list))
	}
	got := list[0]
	// TemplateSummary carries metadata only (no Body/HTMLBody fields at
	// all — the SELECT skips the source columns); everything it does carry
	// must round-trip.
	if got.ID != created.ID || got.UserID != userID || got.Name != "Shaped" ||
		got.Alias != "shaped" || got.Subject != "Subject {{x}}" {
		t.Errorf("summary diverged from created row: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("summary timestamps not set: %+v", got)
	}
}

func TestCreateTemplate_EnforcesPerUserCap(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-cap")

	// Lower the cap for this user so we don't create 10 rows. account_limits
	// has NOT NULL columns without defaults — seed reasonable values.
	_, err := pool.Exec(ctx,
		`INSERT INTO account_limits (user_id, plan_code, max_agents, max_domains, max_messages_month, max_storage_bytes, max_templates)
		 VALUES ($1, 'test', 10, 10, 100000, 1073741824, 2)
		 ON CONFLICT (user_id) DO UPDATE SET max_templates = 2`, userID)
	if err != nil {
		t.Fatalf("seed account_limits: %v", err)
	}

	if _, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "One", Subject: "S", Body: "B"}); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Two", Subject: "S", Body: "B"}); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	_, err = store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Three", Subject: "S", Body: "B"})
	if !errors.Is(err, identity.ErrTemplateLimitReached) {
		t.Errorf("create at cap+1 err = %v, want ErrTemplateLimitReached", err)
	}
}

func TestMaxTemplatesForUser_DefaultWithoutRow(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-cap-default")

	max, err := store.MaxTemplatesForUser(ctx, userID)
	if err != nil {
		t.Fatalf("MaxTemplatesForUser: %v", err)
	}
	if max != identity.DefaultMaxTemplates {
		t.Errorf("MaxTemplatesForUser without account_limits row = %d, want %d", max, identity.DefaultMaxTemplates)
	}
}

func TestUpdateTemplate_PartialFields(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-upd")
	tp, _ := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Old", Alias: "old-alias", Subject: "Old subject", Body: "Old body", HTMLBody: "<p>old</p>"})

	newName := "New"
	newSubject := "New subject"
	got, err := store.UpdateTemplate(ctx, tp.ID, userID, identity.TemplateUpdate{Name: &newName, Subject: &newSubject})
	if err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	if got.Name != newName || got.Subject != newSubject {
		t.Errorf("after update name=%q subject=%q", got.Name, got.Subject)
	}
	// Untouched fields unchanged.
	if got.Alias != "old-alias" || got.Body != "Old body" || got.HTMLBody != "<p>old</p>" {
		t.Errorf("untouched fields drifted: %+v", got)
	}
	if !got.UpdatedAt.After(tp.UpdatedAt) {
		t.Errorf("updated_at not bumped: was %v now %v", tp.UpdatedAt, got.UpdatedAt)
	}
}

func TestUpdateTemplate_ClearAliasAndHTML(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-clear")
	tp, _ := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "T", Alias: "clearing", Subject: "S", Body: "B", HTMLBody: "<p>x</p>"})

	empty := ""
	got, err := store.UpdateTemplate(ctx, tp.ID, userID, identity.TemplateUpdate{Alias: &empty, HTMLBody: &empty})
	if err != nil {
		t.Fatalf("UpdateTemplate clear: %v", err)
	}
	if got.Alias != "" || got.HTMLBody != "" {
		t.Errorf("clear failed: alias=%q html=%q", got.Alias, got.HTMLBody)
	}
	// The alias is freed for reuse.
	if _, err := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "T2", Alias: "clearing", Subject: "S", Body: "B"}); err != nil {
		t.Errorf("reusing cleared alias err = %v, want nil", err)
	}
}

func TestUpdateTemplate_AliasCollision(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID := templateTestUser(t, store, "tmpl-upd-collide")
	store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "First", Alias: "taken", Subject: "S", Body: "B"})
	tp2, _ := store.CreateTemplate(ctx, userID, identity.TemplateCreate{Name: "Second", Alias: "free", Subject: "S", Body: "B"})

	taken := "taken"
	_, err := store.UpdateTemplate(ctx, tp2.ID, userID, identity.TemplateUpdate{Alias: &taken})
	if !errors.Is(err, identity.ErrTemplateAliasTaken) {
		t.Errorf("alias collision on update err = %v, want ErrTemplateAliasTaken", err)
	}
}

func TestUpdateTemplate_CrossUserNotFound(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := templateTestUser(t, store, "tmpl-upd-a")
	userB := templateTestUser(t, store, "tmpl-upd-b")
	tp, _ := store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "Mine", Subject: "S", Body: "B"})

	name := "Stolen"
	if _, err := store.UpdateTemplate(ctx, tp.ID, userB, identity.TemplateUpdate{Name: &name}); !errors.Is(err, identity.ErrTemplateNotFound) {
		t.Errorf("cross-user update err = %v, want ErrTemplateNotFound", err)
	}
}

func TestDeleteTemplate_OwnerOnly(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userA := templateTestUser(t, store, "tmpl-del-a")
	userB := templateTestUser(t, store, "tmpl-del-b")
	tp, _ := store.CreateTemplate(ctx, userA, identity.TemplateCreate{Name: "Mine", Subject: "S", Body: "B"})

	if err := store.DeleteTemplate(ctx, tp.ID, userB); !errors.Is(err, identity.ErrTemplateNotFound) {
		t.Errorf("cross-user delete err = %v, want ErrTemplateNotFound", err)
	}
	if err := store.DeleteTemplate(ctx, tp.ID, userA); err != nil {
		t.Errorf("owner delete err = %v, want nil", err)
	}
	if err := store.DeleteTemplate(ctx, tp.ID, userA); !errors.Is(err, identity.ErrTemplateNotFound) {
		t.Errorf("delete-already-gone err = %v, want ErrTemplateNotFound", err)
	}
}
