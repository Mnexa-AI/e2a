package webhook_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// TestListDeliveriesByWebhook_KeysetTieOnCreatedAtWalksFullSet is the DB-backed
// tiebreak regression for the webhook delivery log — the third generic-keyset
// store, and one of the two lists that grew unbounded in production. It seeds a
// THREE-row group sharing an identical created_at (distinct delivery ids),
// flanked by a newer and two older singletons, then walks the log one/two rows
// per page following ONLY the (created_at, id) after-key — so the real SQL
// predicate `(created_at < $1 OR (created_at = $1 AND id < $2))` is what runs.
//
// The naive `created_at < $1 AND id < $2` form would drop every tied row after
// the first at a page boundary; the exact-set assertion catches that.
func TestListDeliveriesByWebhook_KeysetTieOnCreatedAtWalksFullSet(t *testing.T) {
	pool := testutil.TestDB(t)
	istore := identity.NewStore(pool)
	ss := webhook.NewSubscriberStore(pool)
	ctx := context.Background()

	user, err := istore.CreateOrGetUser(ctx, "whd-tie@example.com", "Owner", "google-whd-tie")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	wh, err := istore.CreateWebhook(ctx, user.ID, "https://example.com/hook", "", []string{"email.received"}, identity.WebhookFilters{})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	env := []byte(`{"type":"email.received"}`)

	// Fixed, microsecond-clean instants (Postgres timestamps are µs precision).
	base := time.Unix(1700000000, 0).UTC()
	newer := base.Add(2 * time.Minute)
	older1 := base.Add(-1 * time.Minute)
	older2 := base.Add(-2 * time.Minute)

	// InsertPendingForTest lets created_at default to now(); pin it explicitly
	// so the three-row tie is exact rather than near-equal.
	seed := func(at time.Time) string {
		id, err := ss.InsertPendingForTest(ctx, wh.ID, "email.received", env)
		if err != nil {
			t.Fatalf("InsertPendingForTest: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE webhook_subscriber_deliveries SET created_at=$1 WHERE id=$2`, at, id); err != nil {
			t.Fatalf("pin created_at: %v", err)
		}
		return id
	}

	newerID := seed(newer)
	tie := []string{seed(base), seed(base), seed(base)}
	older1ID := seed(older1)
	older2ID := seed(older2)

	// Canonical order: created_at DESC, then id DESC within the tie group.
	sortedTie := append([]string(nil), tie...)
	sort.Sort(sort.Reverse(sort.StringSlice(sortedTie)))
	want := append([]string{newerID}, sortedTie...)
	want = append(want, older1ID, older2ID)

	walk := func(limit int) []string {
		var got []string
		var afterC time.Time
		var afterID string
		for page := 0; page < 200; page++ {
			rows, err := ss.ListDeliveriesByWebhook(ctx, wh.ID, "", limit, afterC, afterID)
			if err != nil {
				t.Fatalf("ListDeliveriesByWebhook page %d: %v", page, err)
			}
			if len(rows) == 0 {
				return got
			}
			if len(rows) > limit {
				t.Fatalf("page %d returned %d rows, want <= limit %d", page, len(rows), limit)
			}
			for _, d := range rows {
				got = append(got, d.ID)
			}
			last := rows[len(rows)-1]
			afterC, afterID = last.CreatedAt, last.ID
			if len(rows) < limit {
				return got
			}
		}
		t.Fatalf("walk did not terminate within page cap")
		return nil
	}

	// limit=1 puts every tie member on its own page (maximal straddle);
	// limit=2 lands a page boundary in the MIDDLE of the tie group.
	for _, limit := range []int{1, 2} {
		got := walk(limit)
		if len(got) != len(want) {
			t.Fatalf("limit=%d: walked %d ids, want %d\n got=%v\nwant=%v", limit, len(got), len(want), got, want)
		}
		seen := map[string]bool{}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("limit=%d: order mismatch at index %d\n got=%v\nwant=%v", limit, i, got, want)
			}
			if seen[got[i]] {
				t.Fatalf("limit=%d: duplicate id %q across pages: %v", limit, got[i], got)
			}
			seen[got[i]] = true
		}
	}
}
