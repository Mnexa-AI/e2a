package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSpecGeneratedFromHandlers is the spec↔server check (api-v1-redesign §6):
// the OpenAPI document is emitted from the live, registered handlers — never
// hand-authored — so it cannot drift from what the server actually serves.
// Every registered operation must appear in the generated spec.
func TestSpecGeneratedFromHandlers(t *testing.T) {
	s := New(Deps{}) // no deps needed to render the spec
	yaml, err := s.OpenAPIYAML()
	if err != nil {
		t.Fatalf("render spec: %v", err)
	}
	spec := string(yaml)

	mustContain := []string{
		"openapi: 3.1.0",
		"operationId: getInfo",
		"operationId: listAgents",
		"operationId: getAgent",
		"operationId: createAgent",
		"operationId: updateAgent",
		"operationId: deleteAgent",
		"operationId: getMessage",
		"operationId: listMessages",
		"operationId: listConversations",
		"operationId: getConversation",
		"operationId: listDomains",
		"operationId: registerDomain",
		"operationId: deleteDomain",
		"operationId: verifyDomain",
		"operationId: createWebhook",
		"operationId: listWebhooks",
		"operationId: deleteWebhook",
		"operationId: updateWebhook",
		"operationId: rotateWebhookSecret",
		"operationId: testWebhook",
		"operationId: listWebhookDeliveries",
		"operationId: listEvents",
		"operationId: getEvent",
		"operationId: redeliverEvent",
		"operationId: getMyLimits",
		"operationId: exportUserData",
		"operationId: deleteAccount",
		"operationId: sendMessage",
		"operationId: replyToMessage",
		"operationId: forwardMessage",
		"operationId: testAgent",
		"/v1/send",
		"/v1/users/me/limits",
		"/v1/events",
		"/v1/events/{id}",
		"/v1/webhooks",
		"/v1/webhooks/{id}",
		"/v1/domains/{domain}/verify",
		"/v1/domains",
		"/v1/domains/{domain}",
		"/v1/info",
		"/v1/agents",
		"/v1/agents/{address}",
		"/v1/agents/{address}/messages",
		"/v1/agents/{address}/messages/{id}",
		// The single Bearer security scheme is declared.
		"securitySchemes",
		"bearer",
	}
	for _, want := range mustContain {
		if !strings.Contains(spec, want) {
			t.Errorf("generated spec missing %q", want)
		}
	}
}

// TestSpecServedOverHTTP confirms the spec is reachable at the versioned
// path so SDK/MCP codegen and the docs renderer can fetch it from a running
// instance.
func TestSpecServedOverHTTP(t *testing.T) {
	srv := httptest.NewServer(New(Deps{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "openapi: 3.1.0") {
		t.Fatalf("served spec is not OpenAPI 3.1: %.80s", b)
	}
}
