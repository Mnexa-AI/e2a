package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestUnsubscribeOptionsJSON(t *testing.T) {
	var omitted SendEmailRequest
	if err := json.Unmarshal([]byte(`{"to":["a@example.com"],"subject":"s","text":"b"}`), &omitted); err != nil {
		t.Fatal(err)
	}
	if omitted.Unsubscribe.Present {
		t.Fatal("omitted unsubscribe must not be present")
	}

	var managed SendEmailRequest
	if err := json.Unmarshal([]byte(`{"unsubscribe":{"mode":"managed"}}`), &managed); err != nil {
		t.Fatal(err)
	}
	if !managed.Unsubscribe.Present || managed.Unsubscribe.Mode != "managed" {
		t.Fatalf("managed unsubscribe = %+v", managed.Unsubscribe)
	}

	for _, raw := range []string{
		`{"unsubscribe":null}`,
		`{"unsubscribe":{}}`,
		`{"unsubscribe":{"mode":""}}`,
		`{"unsubscribe":{"mode":"other"}}`,
		`{"unsubscribe":{"mode":"managed","extra":true}}`,
	} {
		var req SendEmailRequest
		if err := json.Unmarshal([]byte(raw), &req); err == nil {
			t.Errorf("decode %s succeeded, want rejection", raw)
		}
	}
}

func TestUnsubscribeOptionsLiveSchemaIsStrict(t *testing.T) {
	schema := New(Deps{}).API.OpenAPI().Components.Schemas.Map()["UnsubscribeOptions"]
	if schema == nil {
		t.Fatal("UnsubscribeOptions schema missing")
	}
	if schema.Type != "object" || schema.Nullable {
		t.Fatalf("type=%q nullable=%v", schema.Type, schema.Nullable)
	}
	if schema.AdditionalProperties != false {
		t.Fatalf("additionalProperties=%v, want false", schema.AdditionalProperties)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "mode" {
		t.Fatalf("required=%v", schema.Required)
	}
	mode := schema.Properties["mode"]
	if mode == nil || len(mode.Enum) != 1 || mode.Enum[0] != "managed" {
		t.Fatalf("mode schema=%+v", mode)
	}
}

func TestAllOutboundRequestsCarryUnsubscribeOptions(t *testing.T) {
	for name, dst := range map[string]any{
		"send":    &SendEmailRequest{},
		"reply":   &ReplyRequest{},
		"forward": &ForwardRequest{},
	} {
		if err := json.Unmarshal([]byte(`{"unsubscribe":{"mode":"managed"}}`), dst); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestInvalidUnsubscribeUsesNative422OnEveryOutboundRoute(t *testing.T) {
	routes := []struct {
		name, path string
		body       map[string]any
	}{
		{"send", sendURL, map[string]any{"to": []string{"a@example.net"}, "subject": "s", "text": "b"}},
		{"reply", "/v1/agents/support%40acme.com/messages/msg_in1/reply", map[string]any{"text": "reply"}},
		{"forward", "/v1/agents/support%40acme.com/messages/msg_in1/forward", map[string]any{"to": []string{"next@example.net"}, "text": "fyi"}},
	}
	invalid := []any{nil, map[string]any{}, map[string]any{"mode": "other"}, map[string]any{"mode": "managed", "extra": true}}
	for _, route := range routes {
		for _, unsubscribe := range invalid {
			t.Run(route.name, func(t *testing.T) {
				body := make(map[string]any, len(route.body)+1)
				for key, value := range route.body {
					body[key] = value
				}
				body["unsubscribe"] = unsubscribe
				srv := testServer(t)
				code, response := postJSON(t, srv.URL+route.path, "good", body)
				if code != http.StatusUnprocessableEntity || errCode(response) != "invalid_request" {
					t.Fatalf("unsubscribe=%v got %d %v", unsubscribe, code, response)
				}
			})
		}
	}
}

func TestOutboundUnsubscribeDecodeFailuresRemainNative422(t *testing.T) {
	for _, requestBody := range []map[string]any{
		{"to": "not-an-array", "subject": "s", "text": "b"},
		{"to": "not-an-array", "subject": "s", "text": "b", "unsubscribe": map[string]any{"mode": "other"}},
	} {
		srv := testServer(t)
		code, body := postJSON(t, srv.URL+sendURL, "good", requestBody)
		if code != 422 || errCode(body) != "invalid_request" {
			t.Fatalf("request=%v got %d %v", requestBody, code, body)
		}
	}
}

func TestOutboundUnsubscribeSiblingFieldPrefixesRemainNative422(t *testing.T) {
	routes := []struct {
		name, path string
		body       map[string]any
	}{
		{"send", sendURL, map[string]any{"to": []string{"a@example.net"}, "subject": "s", "text": "b"}},
		{"reply", "/v1/agents/support%40acme.com/messages/msg_in1/reply", map[string]any{"text": "reply"}},
		{"forward", "/v1/agents/support%40acme.com/messages/msg_in1/forward", map[string]any{"to": []string{"next@example.net"}, "text": "fyi"}},
	}
	for _, route := range routes {
		for _, sibling := range []string{"unsubscribe_extra", "unsubscribeMode"} {
			t.Run(route.name+"/"+sibling, func(t *testing.T) {
				body := make(map[string]any, len(route.body)+1)
				for key, value := range route.body {
					body[key] = value
				}
				body[sibling] = true
				srv := testServer(t)
				code, response := postJSON(t, srv.URL+route.path, "good", body)
				if code != 422 || errCode(response) != "invalid_request" {
					t.Fatalf("field=%s got %d %v", sibling, code, response)
				}
			})
		}
	}
}

func TestOutboundOperationsRetainHumaBodyCap(t *testing.T) {
	paths := []string{
		"/v1/agents/{email}/messages",
		"/v1/agents/{email}/messages/{id}/reply",
		"/v1/agents/{email}/messages/{id}/forward",
	}
	api := New(Deps{}).API.OpenAPI()
	for _, path := range paths {
		item := api.Paths[path]
		if item == nil || item.Post == nil || item.Post.MaxBodyBytes != maxOutboundBytes {
			t.Fatalf("%s MaxBodyBytes=%v, want %d", path, item, maxOutboundBytes)
		}
	}
}

func TestSendManagedUnsubscribeMapsToInternalRequest(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"a@example.net"}, "subject": "s", "text": "b", "unsubscribe": map[string]any{"mode": "managed"},
	})
	srv.Close()
	if code != 200 {
		t.Fatalf("got %d %v", code, body)
	}
	got := lastDeliveredReq()
	if got.Unsubscribe == nil || got.Unsubscribe.Mode != "managed" {
		t.Fatalf("delivered unsubscribe=%+v", got.Unsubscribe)
	}
}

func TestReplyAndForwardManagedUnsubscribeMapToInternalRequest(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		body       map[string]any
	}{
		{"reply", "/v1/agents/support%40acme.com/messages/msg_in1/reply", map[string]any{"text": "reply"}},
		{"forward", "/v1/agents/support%40acme.com/messages/msg_in1/forward", map[string]any{"to": []string{"next@example.net"}, "text": "fyi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.body["unsubscribe"] = map[string]any{"mode": "managed"}
			srv := testServer(t)
			code, body := postJSON(t, srv.URL+tc.path, "good", tc.body)
			srv.Close()
			if code != 200 {
				t.Fatalf("got %d %v", code, body)
			}
			got := lastDeliveredReq()
			if got.Unsubscribe == nil || got.Unsubscribe.Mode != "managed" {
				t.Fatalf("delivered=%+v", got.Unsubscribe)
			}
		})
	}
}

func TestTemplateManagedUnsubscribeMapsAfterRendering(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"a@example.net"}, "template_alias": "welcome", "template_data": map[string]any{"name": "Ada"},
		"unsubscribe": map[string]any{"mode": "managed"},
	})
	srv.Close()
	if code != 200 {
		t.Fatalf("got %d %v", code, body)
	}
	got := lastDeliveredReq()
	if got.Unsubscribe == nil || got.Subject == "" || got.Body == "" {
		t.Fatalf("rendered managed request=%+v", got)
	}
}
