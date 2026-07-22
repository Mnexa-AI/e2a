package contract

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type cleanupRoundTripper func(*http.Request) (*http.Response, error)

func (f cleanupRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRunnerCleanupRequestsExecuteInOrderAndBestEffort(t *testing.T) {
	var paths []string
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: cleanupRoundTripper(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		status := http.StatusOK
		body := ""
		if req.URL.Path == "/cleanup-agent" {
			status = http.StatusInternalServerError
			body = "cleanup failure"
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	r := newRunner(&testEnv{baseURL: "https://contract.test", apiKey: "key"}, scenario{Cleanup: []step{
		{ID: "cleanup_agent", Action: "request", Method: http.MethodDelete, Path: "/cleanup-agent", Expect: &expectation{Status: http.StatusOK}},
		{ID: "cleanup_domain", Action: "request", Method: http.MethodDelete, Path: "/cleanup-domain", Expect: &expectation{Status: http.StatusOK}},
	}})
	errs := r.cleanupErrors()

	if !reflect.DeepEqual(paths, []string{"/cleanup-agent", "/cleanup-domain"}) {
		t.Fatalf("cleanup paths = %v", paths)
	}
	if len(errs) != 1 {
		t.Fatalf("cleanup errors = %v, want one without stopping later requests", errs)
	}
}

func TestManagedUnsubscribeUsesFailureSafeCleanupBlock(t *testing.T) {
	var target *scenario
	for _, candidate := range loadScenarios(t) {
		if candidate.Name == "agent_suppression_and_managed_unsubscribe" {
			copy := candidate
			target = &copy
			break
		}
	}
	if target == nil {
		t.Fatal("managed unsubscribe scenario not found")
	}
	for _, ordinary := range target.Steps {
		if ordinary.ID == "delete_agent_permanently" || ordinary.ID == "delete_domain" {
			t.Fatalf("cleanup step %q remains in ordinary steps", ordinary.ID)
		}
	}
	got := make([]string, len(target.Cleanup))
	for i := range target.Cleanup {
		got[i] = target.Cleanup[i].ID
	}
	if !reflect.DeepEqual(got, []string{"delete_agent_permanently", "delete_domain"}) {
		t.Fatalf("cleanup order = %v", got)
	}
}
