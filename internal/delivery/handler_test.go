package delivery

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// failing verifier path: a TopicArn not in the allow-list is rejected before
// any cert fetch, so the handler returns 403 without network.
func TestHandlerRejectsUnverified(t *testing.T) {
	v := NewVerifier([]string{"arn:aws:sns:us-east-2:1:allowed"}, nil)
	h := Handler(v, NewConsumer(nil, nil))
	body := `{"Type":"Notification","TopicArn":"arn:aws:sns:us-east-2:1:EVIL","MessageId":"m","Message":"{}","Timestamp":"t","SignatureVersion":"1","Signature":"x","SigningCertURL":"https://sns.us-east-2.amazonaws.com/c.pem"}`
	r := httptest.NewRequest(http.MethodPost, "/api/internal/ses/notifications", strings.NewReader(body))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unverified message: status=%d, want 403", w.Code)
	}
}

func TestHandlerRejectsBadJSON(t *testing.T) {
	h := Handler(NewVerifier(nil, nil), NewConsumer(nil, nil))
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad json: status=%d, want 400", w.Code)
	}
}

func TestHandlerRejectsGET(t *testing.T) {
	h := Handler(NewVerifier(nil, nil), NewConsumer(nil, nil))
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status=%d, want 405", w.Code)
	}
}
