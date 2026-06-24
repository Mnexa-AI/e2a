package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

func reviewsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(Deps{
		Authenticator: func(r *http.Request) (*identity.User, error) {
			if r.Header.Get("Authorization") == "Bearer good" {
				return &identity.User{ID: "u_1", Email: "owner@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		ListReviews: func(ctx context.Context, userID string) ([]identity.ReviewListItem, error) {
			if userID != "u_1" {
				return nil, errors.New("unexpected user")
			}
			return []identity.ReviewListItem{
				{ID: "in1", AgentID: "support@acme.dev", Direction: "inbound", Sender: "spam@evil.biz", To: []string{"support@acme.dev"}, Subject: "held inbound", Status: "pending_review", CreatedAt: time.Unix(1700000200, 0).UTC()},
				{ID: "out1", AgentID: "support@acme.dev", Direction: "outbound", Sender: "support@acme.dev", To: []string{"cust@x.com"}, Subject: "held draft", Status: "pending_review", CreatedAt: time.Unix(1700000100, 0).UTC()},
			}, nil
		},
		GetReviewWithContent: func(ctx context.Context, userID, id string) (*identity.Message, error) {
			if userID == "u_1" && id == "in1" {
				return &identity.Message{
					ID: "in1", AgentID: "support@acme.dev", Direction: "inbound",
					Sender: "spam@evil.biz", Recipient: "support@acme.dev",
					Subject: "held inbound", Status: "pending_review",
					RawMessage: []byte("From: spam@evil.biz\r\nSubject: held inbound\r\n\r\nbad link"),
					CreatedAt:  time.Unix(1700000200, 0).UTC(),
				}, nil
			}
			return nil, errors.New("not found")
		},
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReviews_ListReturnsBothDirections(t *testing.T) {
	srv := reviewsServer(t)
	code, body := getJSON(t, srv.URL+"/v1/reviews", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("want 2 reviews, got %v", body)
	}
	dirs := map[string]bool{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		dirs[m["direction"].(string)] = true
		if m["review_status"] != "pending_review" {
			t.Errorf("item missing review_status: %v", m)
		}
	}
	if !dirs["inbound"] || !dirs["outbound"] {
		t.Fatalf("expected both directions, got %v", dirs)
	}
}

// Regression: the inbound /reviews/{id} detail MUST report
// review_status=pending_review. messageViewFromIdentity only sets it for
// outbound, so without the handler override the dashboard read "" and showed a
// bogus "no longer pending" banner on a clearly-pending inbound hold.
func TestReviews_InboundDetailReportsReviewStatus(t *testing.T) {
	srv := reviewsServer(t)
	code, body := getJSON(t, srv.URL+"/v1/reviews/in1", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["direction"] != "inbound" {
		t.Fatalf("want inbound, got %v", body["direction"])
	}
	if body["review_status"] != "pending_review" {
		t.Fatalf("inbound review detail must report review_status=pending_review, got %v", body["review_status"])
	}
}

func TestReviews_DetailNotFound(t *testing.T) {
	srv := reviewsServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/reviews/nope", "good")
	if code != 404 {
		t.Fatalf("want 404 for unknown review, got %d", code)
	}
}

func TestReviews_Unauthorized(t *testing.T) {
	srv := reviewsServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/reviews", "")
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}
