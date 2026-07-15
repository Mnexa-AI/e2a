package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestOpenAPIResponseHeaderContract(t *testing.T) {
	oapi := New(Deps{}).API.OpenAPI()
	for _, name := range []string{
		"XRequestID",
		"RetryAfter",
	} {
		if oapi.Components.Headers[name] == nil {
			t.Errorf("missing components.headers.%s", name)
		}
	}

	forEachOperation(oapi, func(op *huma.Operation) {
		for status, response := range op.Responses {
			requestID := response.Headers["X-Request-Id"]
			if requestID == nil || requestID.Ref != "#/components/headers/XRequestID" {
				t.Errorf("%s %s missing X-Request-Id component reference", op.OperationID, status)
			}
			if status == "429" || status == "503" {
				retryAfter := response.Headers["Retry-After"]
				if retryAfter == nil || retryAfter.Ref != "#/components/headers/RetryAfter" {
					t.Errorf("%s %s missing Retry-After component reference", op.OperationID, status)
				}
			}
			for name := range response.Headers {
				if strings.HasPrefix(strings.ToLower(name), "x-ratelimit-") {
					t.Errorf("%s %s declares legacy header %s", op.OperationID, status, name)
				}
			}
		}
	})
}

func TestLimitsUnavailableRetryContract(t *testing.T) {
	srv := testServer(t, func(deps *Deps) {
		deps.GetLimits = nil
	})
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/account", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer good")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After = %q, want 5", got)
	}
	var body struct {
		Error struct {
			Details struct {
				RetryAfterSeconds int `json:"retry_after_seconds"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Details.RetryAfterSeconds != 5 {
		t.Fatalf("retry_after_seconds = %d, want 5", body.Error.Details.RetryAfterSeconds)
	}

	op := New(Deps{}).API.OpenAPI().Paths["/v1/account"].Get
	response := op.Responses["503"]
	if response == nil {
		t.Fatal("getAccount does not declare a 503 response")
	}
	retryAfter := response.Headers["Retry-After"]
	if retryAfter == nil || retryAfter.Ref != "#/components/headers/RetryAfter" {
		t.Fatalf("getAccount 503 Retry-After = %#v", retryAfter)
	}
}

func TestOpenAPIRateLimitHeaderContract(t *testing.T) {
	oapi := New(Deps{}).API.OpenAPI()
	components := map[string]string{
		"RateLimit-Limit":     "RateLimitLimit",
		"RateLimit-Remaining": "RateLimitRemaining",
		"RateLimit-Reset":     "RateLimitReset",
	}
	for _, component := range components {
		if oapi.Components.Headers[component] == nil {
			t.Errorf("missing components.headers.%s", component)
		}
	}

	forEachOperation(oapi, func(op *huma.Operation) {
		if !pollLimitedOps[op.OperationID] && op.OperationID != "createAgent" {
			return
		}
		for status, response := range op.Responses {
			for header, component := range components {
				got := response.Headers[header]
				want := "#/components/headers/" + component
				if got == nil || got.Ref != want {
					t.Errorf("%s %s missing %s component reference", op.OperationID, status, header)
				}
			}
		}
	})
}
