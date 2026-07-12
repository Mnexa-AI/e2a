package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/webhook"
)

// These tests verify the newly-standardized keyset pagination on the list
// endpoints that previously stubbed next_cursor: seed >1 page, walk the cursor,
// and assert the concatenated pages equal the full set with no dupes/gaps and a
// stable order — plus that a full final page does NOT emit a spurious cursor and
// that a tampered cursor is rejected.

// afterKey is the generic keyset predicate shared by the fakes: newest-first on
// (createdAt, id), returning true when row (c,id) sorts strictly AFTER the
// cursor position — i.e. should appear on a later page.
func afterKey(c time.Time, id string, afterC time.Time, afterID string) bool {
	if afterC.IsZero() {
		return true // first page
	}
	if c.Before(afterC) {
		return true
	}
	if c.Equal(afterC) && id < afterID {
		return true
	}
	return false
}

// walkPages drives a paginated endpoint from the first page to the last,
// following next_cursor, and returns the concatenated item ids in order. It
// fails if a cursor loops, if more than maxPages are needed, or on a non-200.
func walkPages(t *testing.T, srv *httptest.Server, path, idField string) []string {
	t.Helper()
	var ids []string
	cursor := ""
	seenCursors := map[string]bool{}
	for page := 0; page < 50; page++ {
		u := srv.URL + path
		if cursor != "" {
			sep := "&"
			u += sep + "cursor=" + cursor
		}
		code, body := getJSON(t, u, "good")
		if code != 200 {
			t.Fatalf("page %d: status %d body %v", page, code, body)
		}
		items, _ := body["items"].([]any)
		for _, it := range items {
			m := it.(map[string]any)
			ids = append(ids, m[idField].(string))
		}
		next, ok := body["next_cursor"].(string)
		if !ok || next == "" {
			return ids // last page
		}
		if seenCursors[next] {
			t.Fatalf("cursor loop detected at page %d", page)
		}
		seenCursors[next] = true
		cursor = next
	}
	t.Fatalf("did not terminate within 50 pages")
	return ids
}

func assertNoDupes(t *testing.T, ids []string, want int) {
	t.Helper()
	if len(ids) != want {
		t.Fatalf("want %d items across all pages, got %d: %v", want, len(ids), ids)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %q across pages: %v", id, ids)
		}
		seen[id] = true
	}
}

// --- reviews (PRIORITY 1: unbounded review backlog) ---

func paginatedReviewsServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	// Newest-first canonical order: r0 (newest) .. r{n-1} (oldest).
	//
	// Rows 1..3 (when present) deliberately SHARE one created_at so the fake's
	// afterKey tiebreak branch (Equal && id < afterID) actually executes as the
	// walk crosses the tie — otherwise, with strictly-distinct timestamps, that
	// branch is dead code and the handler test would only cover cursor plumbing.
	// Because idFor is lexically descending in step with recency (idFor(1) >
	// idFor(2) > idFor(3)), the id-DESC tiebreak reproduces the same order the
	// distinct-timestamp seed had, so the order assertions below are unchanged.
	// The real SQL keyset predicate under a tie is covered separately by the
	// DB-backed store tests (internal/identity, internal/webhook).
	all := make([]identity.ReviewListItem, n)
	base := time.Unix(1700000000, 0).UTC()
	tieAt := base.Add(-2 * time.Minute)
	for i := 0; i < n; i++ {
		at := base.Add(-time.Duration(i) * time.Minute)
		if i >= 1 && i <= 3 {
			at = tieAt
		}
		all[i] = identity.ReviewListItem{
			ID:        idFor(i),
			AgentID:   "support@acme.dev",
			Direction: "inbound",
			Sender:    "s@x.com",
			To:        []string{"support@acme.dev"},
			Subject:   "held",
			Status:    "pending_review",
			CreatedAt: at,
		}
	}
	srv := httptest.NewServer(New(Deps{
		Authenticator: bearerGood,
		ListReviews: func(_ context.Context, userID string, limit int, afterC time.Time, afterID string) ([]identity.ReviewListItem, error) {
			out := []identity.ReviewListItem{}
			for _, r := range all {
				if !afterKey(r.CreatedAt, r.ID, afterC, afterID) {
					continue
				}
				out = append(out, r)
				if limit > 0 && len(out) == limit {
					break
				}
			}
			return out, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReviews_KeysetPaginationWalksFullSet(t *testing.T) {
	srv := paginatedReviewsServer(t, 5)
	ids := walkPages(t, srv, "/v1/reviews?limit=2", "id")
	assertNoDupes(t, ids, 5)
	// Stable newest-first order across pages.
	want := []string{idFor(0), idFor(1), idFor(2), idFor(3), idFor(4)}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("order mismatch at %d: want %v got %v", i, want, ids)
		}
	}
}

func TestReviews_FullFinalPageNoSpuriousCursor(t *testing.T) {
	srv := paginatedReviewsServer(t, 4)
	// limit=2 over exactly 4 => page1 (2 + cursor), page2 (2, NO cursor).
	code, body := getJSON(t, srv.URL+"/v1/reviews?limit=2", "good")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	cursor := body["next_cursor"].(string)
	code, body2 := getJSON(t, srv.URL+"/v1/reviews?limit=2&cursor="+cursor, "good")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if nc, ok := body2["next_cursor"]; ok && nc != nil {
		t.Fatalf("full final page must not emit a cursor, got %v", nc)
	}
}

func TestReviews_TamperedCursorRejected(t *testing.T) {
	srv := paginatedReviewsServer(t, 3)
	code, body := getJSON(t, srv.URL+"/v1/reviews?limit=1&cursor=not-a-valid-cursor", "good")
	if code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad cursor, got %d body %v", code, body)
	}
	if errCode(body) != "invalid_cursor" {
		t.Fatalf("want code invalid_cursor, got %v", body)
	}
}

// --- webhook deliveries (PRIORITY 1: unbounded delivery log) ---

func paginatedDeliveriesServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	all := make([]webhook.SubscriberDelivery, n)
	base := time.Unix(1700000000, 0).UTC()
	for i := 0; i < n; i++ {
		all[i] = webhook.SubscriberDelivery{
			ID:          idFor(i),
			WebhookID:   "wh_1",
			EventType:   "email.received",
			Status:      "delivered",
			NextRetryAt: base,
			CreatedAt:   base.Add(-time.Duration(i) * time.Minute),
		}
	}
	srv := httptest.NewServer(New(Deps{
		Authenticator: bearerGood,
		GetWebhook: func(_ context.Context, id, userID string) (*identity.Webhook, error) {
			return &identity.Webhook{ID: id, UserID: userID}, nil
		},
		ListDeliveries: func(_ context.Context, webhookID, status string, limit int, afterC time.Time, afterID string) ([]webhook.SubscriberDelivery, error) {
			out := []webhook.SubscriberDelivery{}
			for _, d := range all {
				if status != "" && d.Status != status {
					continue
				}
				if !afterKey(d.CreatedAt, d.ID, afterC, afterID) {
					continue
				}
				out = append(out, d)
				if limit > 0 && len(out) == limit {
					break
				}
			}
			return out, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebhookDeliveries_KeysetPaginationWalksFullSet(t *testing.T) {
	srv := paginatedDeliveriesServer(t, 7)
	ids := walkPages(t, srv, "/v1/webhooks/wh_1/deliveries?limit=3", "id")
	assertNoDupes(t, ids, 7)
	for i := 0; i < 7; i++ {
		if ids[i] != idFor(i) {
			t.Fatalf("order mismatch: want %s at %d, got %v", idFor(i), i, ids)
		}
	}
}

func TestWebhookDeliveries_CursorPinsStatusFilter(t *testing.T) {
	srv := paginatedDeliveriesServer(t, 5)
	// Mint a cursor with no status filter, then replay it WITH a status filter.
	code, body := getJSON(t, srv.URL+"/v1/webhooks/wh_1/deliveries?limit=2", "good")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	cursor := body["next_cursor"].(string)
	code, body = getJSON(t, srv.URL+"/v1/webhooks/wh_1/deliveries?limit=2&status=failed&cursor="+cursor, "good")
	if code != http.StatusBadRequest || errCode(body) != "invalid_cursor" {
		t.Fatalf("want 400 invalid_cursor for status change under a cursor, got %d %v", code, body)
	}
}

// --- agents (PRIORITY 2: bulk list previously capped) ---

func paginatedAgentsServer(t *testing.T, n int) *httptest.Server {
	t.Helper()
	all := make([]identity.AgentIdentity, n)
	base := time.Unix(1700000000, 0).UTC()
	for i := 0; i < n; i++ {
		all[i] = identity.AgentIdentity{
			ID:        idFor(i),
			Domain:    "acme.com",
			UserID:    "u_1",
			Name:      "agent",
			CreatedAt: base.Add(-time.Duration(i) * time.Minute),
		}
	}
	srv := httptest.NewServer(New(Deps{
		Authenticator: bearerGood,
		ListAgents: func(_ context.Context, userID string, limit int, afterC time.Time, afterID string) ([]identity.AgentIdentity, error) {
			out := []identity.AgentIdentity{}
			for _, a := range all {
				if !afterKey(a.CreatedAt, a.ID, afterC, afterID) {
					continue
				}
				out = append(out, a)
				if limit > 0 && len(out) == limit {
					break
				}
			}
			return out, nil
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAgents_KeysetPaginationWalksFullSet(t *testing.T) {
	srv := paginatedAgentsServer(t, 6)
	// #436 dropped AgentView.id (id == email); the agent list keys on email.
	ids := walkPages(t, srv, "/v1/agents?limit=2", "email")
	assertNoDupes(t, ids, 6)
}

// idFor builds a lexically-descending id: newer rows (smaller i) get
// lexically-larger ids. Combined with the equal-created_at subset that
// paginatedReviewsServer seeds, this drives the fake's id-DESC tiebreak so the
// order is stable across a tie. NOTE: this handler-level fake only exercises the
// cursor plumbing and the fake's own afterKey tiebreak — the real store SQL
// predicate under a created_at tie is covered by the DB-backed store tests in
// internal/identity (reviews, agents) and internal/webhook (deliveries).
func idFor(i int) string {
	return string(rune('a'+(20-i))) + "_id"
}

func bearerGood(r *http.Request) (*identity.User, error) {
	if r.Header.Get("Authorization") == "Bearer good" {
		return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
	}
	return nil, errors.New("unauthorized")
}
