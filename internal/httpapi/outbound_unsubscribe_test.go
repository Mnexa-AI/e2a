package httpapi

import (
	"encoding/json"
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
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		if env := validateUnsubscribeOptions(req.Unsubscribe); env == nil || env.GetStatus() != 400 {
			t.Errorf("%s env=%v, want 400", raw, env)
		}
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

func TestSendInvalidUnsubscribeReturns400(t *testing.T) {
	for _, unsubscribe := range []any{nil, map[string]any{}, map[string]any{"mode": "other"}, map[string]any{"mode": "managed", "extra": true}} {
		srv := testServer(t)
		code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
			"to": []string{"a@example.net"}, "subject": "s", "text": "b", "unsubscribe": unsubscribe,
		})
		srv.Close()
		if code != 400 || errCode(body) != "invalid_request" {
			t.Fatalf("unsubscribe=%v got %d %v", unsubscribe, code, body)
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
