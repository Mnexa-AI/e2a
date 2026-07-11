package identity_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// These DB-backed tests are the regression coverage for the whole reason keyset
// pagination exists: the tiebreak when several rows share a created_at. The
// handler-level fakes in internal/httpapi drive the cursor plumbing, but only a
// real Postgres round-trip exercises the SQL predicate
//
//	(created_at < $1 OR (created_at = $1 AND id < $2))
//
// A refactor to the naive `created_at < $1 AND id < $2` form would silently skip
// every tied row after the first on each page boundary; these walks catch that
// by asserting the concatenation of every page is EXACTLY the full set — no
// duplicates, no gaps — in the canonical (created_at DESC, id DESC) order, with
// the tie group deliberately straddling page boundaries (limit < tie size).

// keyRow is the minimal (id, created_at) projection the generic walk needs; each
// store test maps its own row type onto it.
type keyRow struct {
	id        string
	createdAt time.Time
}

// walkKeyset drives a store list method from the first page to exhaustion using
// ONLY the (created_at, id) after-key from the previous page's last row — i.e.
// the raw keyset predicate, with no HMAC cursor in the loop, so the SQL is what
// is under test. It returns the concatenated ids in walked order and fails on a
// page larger than the limit or a walk that never terminates.
func walkKeyset(t *testing.T, limit int, fetch func(limit int, afterC time.Time, afterID string) ([]keyRow, error)) []string {
	t.Helper()
	var got []string
	var afterC time.Time
	var afterID string
	for page := 0; page < 200; page++ {
		rows, err := fetch(limit, afterC, afterID)
		if err != nil {
			t.Fatalf("fetch page %d: %v", page, err)
		}
		if len(rows) == 0 {
			return got
		}
		if len(rows) > limit {
			t.Fatalf("page %d returned %d rows, want <= limit %d", page, len(rows), limit)
		}
		for _, r := range rows {
			got = append(got, r.id)
		}
		last := rows[len(rows)-1]
		afterC, afterID = last.createdAt, last.id
		if len(rows) < limit {
			return got
		}
	}
	t.Fatalf("walk did not terminate within page cap")
	return nil
}

// assertExactSet fails unless got equals want element-for-element (order,
// membership, and no duplicates).
func assertExactSet(t *testing.T, limit int, got, want []string) {
	t.Helper()
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

// descByID returns a copy of ids sorted descending — the intra-tie order the
// ORDER BY ... id DESC clause produces among rows sharing a created_at.
func descByID(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// TestListReviews_KeysetTieOnCreatedAtWalksFullSet seeds a THREE-row group with
// an identical created_at (distinct message ids), flanked by a newer singleton
// and two older singletons, then walks the review queue one/two rows per page.
// The tie sits mid-list so it straddles page boundaries under limit<3.
func TestListReviews_KeysetTieOnCreatedAtWalksFullSet(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()
	userID, agentID := seedReviewAgent(t, store, ctx, "reviewtie.example.com")
	exp := time.Now().Add(time.Hour)

	// Fixed, microsecond-clean instants (Postgres timestamps are µs precision).
	base := time.Unix(1700000000, 0).UTC()
	newer := base.Add(2 * time.Minute)
	older1 := base.Add(-1 * time.Minute)
	older2 := base.Add(-2 * time.Minute)

	// CreateInboundMessage stamps created_at = now(); pin it explicitly so the
	// tie is real rather than three near-but-not-equal timestamps.
	seed := func(subj string, at time.Time) string {
		id := createInbound(t, store, ctx, agentID, "evil@x.com", subj, identity.InboundScreening{
			Status:            identity.MessageStatusPendingReview,
			ScanAction:        "review",
			ReviewReason:      identity.ReviewReasonInboundScan,
			ApprovalExpiresAt: &exp,
		})
		if _, err := pool.Exec(ctx, `UPDATE messages SET created_at=$1 WHERE id=$2`, at, id); err != nil {
			t.Fatalf("pin created_at for %s: %v", subj, err)
		}
		return id
	}

	newerID := seed("newer", newer)
	tie := []string{seed("tie-a", base), seed("tie-b", base), seed("tie-c", base)}
	older1ID := seed("older1", older1)
	older2ID := seed("older2", older2)

	// Canonical order: created_at DESC, then id DESC within the tie group.
	want := append([]string{newerID}, descByID(tie)...)
	want = append(want, older1ID, older2ID)

	fetch := func(limit int, afterC time.Time, afterID string) ([]keyRow, error) {
		rows, err := store.ListReviews(ctx, userID, limit, afterC, afterID)
		if err != nil {
			return nil, err
		}
		out := make([]keyRow, len(rows))
		for i, r := range rows {
			out[i] = keyRow{id: r.ID, createdAt: r.CreatedAt}
		}
		return out, nil
	}

	// limit=1 puts every tie member on its own page (maximal straddle);
	// limit=2 lands a page boundary in the MIDDLE of the tie group.
	for _, limit := range []int{1, 2} {
		got := walkKeyset(t, limit, fetch)
		assertExactSet(t, limit, got, want)
	}
}

// TestListAgentsByUser_KeysetTieOnCreatedAtWalksFullSet is the same tiebreak
// regression for the agents list (the other primary generic-keyset store).
func TestListAgentsByUser_KeysetTieOnCreatedAtWalksFullSet(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	user, err := store.CreateOrGetUser(ctx, "o@agenttie.example.com", "O", "g-agenttie")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, "agenttie.example.com", user.ID); err != nil {
		t.Fatalf("ClaimOrCreateDomain: %v", err)
	}

	base := time.Unix(1700000000, 0).UTC()
	newer := base.Add(2 * time.Minute)
	older1 := base.Add(-1 * time.Minute)
	older2 := base.Add(-2 * time.Minute)

	// CreateAgent stamps created_at = time.Now(); pin it so the tie is exact.
	seed := func(local string, at time.Time) string {
		email := local + "@agenttie.example.com"
		if _, err := store.CreateAgent(ctx, email, "agenttie.example.com", "", "", "", user.ID); err != nil {
			t.Fatalf("CreateAgent(%s): %v", email, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE agent_identities SET created_at=$1 WHERE id=$2`, at, email); err != nil {
			t.Fatalf("pin created_at for %s: %v", email, err)
		}
		return email
	}

	newerID := seed("z-newer", newer)
	tie := []string{seed("tie-c", base), seed("tie-b", base), seed("tie-a", base)}
	older1ID := seed("m-older1", older1)
	older2ID := seed("n-older2", older2)

	want := append([]string{newerID}, descByID(tie)...)
	want = append(want, older1ID, older2ID)

	fetch := func(limit int, afterC time.Time, afterID string) ([]keyRow, error) {
		rows, err := store.ListAgentsByUser(ctx, user.ID, limit, afterC, afterID)
		if err != nil {
			return nil, err
		}
		out := make([]keyRow, len(rows))
		for i, a := range rows {
			out[i] = keyRow{id: a.ID, createdAt: a.CreatedAt}
		}
		return out, nil
	}

	for _, limit := range []int{1, 2} {
		got := walkKeyset(t, limit, fetch)
		assertExactSet(t, limit, got, want)
	}
}
