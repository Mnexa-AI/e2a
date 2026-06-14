package httpapi

import "testing"

func TestGetMyLimits(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/users/me/limits", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	if body["plan_code"] != "pro" {
		t.Fatalf("unexpected plan: %v", body)
	}
	caps, _ := body["limits"].(map[string]any)
	if caps["max_agents"].(float64) != 10 {
		t.Fatalf("unexpected caps: %v", caps)
	}
	usage, _ := body["usage"].(map[string]any)
	if usage["agents"].(float64) != 2 || usage["messages_month"].(float64) != 42 {
		t.Fatalf("unexpected usage: %v", usage)
	}
}

func TestGetMyLimitsUnauthorized(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/users/me/limits", "")
	if code != 401 {
		t.Fatalf("want 401, got %d", code)
	}
}
