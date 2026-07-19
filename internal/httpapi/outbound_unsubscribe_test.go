package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

type countingByteReader struct {
	remaining int64
	read      int64
}

func (r *countingByteReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 'x'
	}
	r.remaining -= int64(len(p))
	r.read += int64(len(p))
	return len(p), nil
}

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

func TestOutboundUnsubscribePreflightBoundsChunkedBodiesOnEveryMatchedRoute(t *testing.T) {
	paths := []string{
		"/v1/agents/support%40acme.com/messages",
		"/v1/agents/support%40acme.com/messages/msg_in1/reply",
		"/v1/agents/support%40acme.com/messages/msg_in1/forward",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			body := &countingByteReader{remaining: maxOutboundBytes + 1024}
			req := httptest.NewRequest(http.MethodPost, path, io.NopCloser(body))
			req.ContentLength = -1 // exercise the chunked/no-Content-Length path
			recorder := httptest.NewRecorder()
			var downstreamCalls atomic.Int32
			handler := outboundUnsubscribeBadRequest(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				downstreamCalls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))

			handler.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if downstreamCalls.Load() != 0 {
				t.Fatalf("downstream called %d times", downstreamCalls.Load())
			}
			if body.read != maxOutboundBytes+1 {
				t.Fatalf("preflight read %d bytes, want exactly %d", body.read, maxOutboundBytes+1)
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Err.Code != "payload_too_large" {
				t.Fatalf("code=%q", envelope.Err.Code)
			}
			details, ok := envelope.Err.Details.(map[string]any)
			if !ok || details["scope"] != "request_body" || details["actual_bytes"] != float64(maxOutboundBytes+1) || details["max_bytes"] != float64(maxOutboundBytes) {
				t.Fatalf("details=%#v", envelope.Err.Details)
			}
		})
	}
}

func TestOutboundUnsubscribePreflightRejectsKnownOversizeWithoutReading(t *testing.T) {
	body := &countingByteReader{remaining: maxOutboundBytes + 1}
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/support%40acme.com/messages", io.NopCloser(body))
	req.ContentLength = maxOutboundBytes + 1
	recorder := httptest.NewRecorder()
	called := false

	outboundUnsubscribeBadRequest(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("status=%d downstream_called=%v", recorder.Code, called)
	}
	if body.read != 0 {
		t.Fatalf("read %d bytes despite oversized Content-Length", body.read)
	}
}

func TestOutboundUnsubscribePreflightRestoresNormalBodyByteIdentically(t *testing.T) {
	want := []byte(" \n{\"text\":\"hello\",\"unsubscribe\":{\"mode\":\"managed\"}}\t")
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/support%40acme.com/messages/msg_in1/reply", bytes.NewReader(want))
	recorder := httptest.NewRecorder()

	outboundUnsubscribeBadRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("restored body=%q, want %q", got, want)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOutboundUnsubscribePreflightLeavesNonMatchedRoutesUntouched(t *testing.T) {
	want := []byte("route body")
	body := &countingByteReader{remaining: int64(len(want))}
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/support%40acme.com/test", io.NopCloser(body))
	recorder := httptest.NewRecorder()

	outboundUnsubscribeBadRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body.read != 0 {
			t.Fatalf("middleware consumed %d bytes from an unmatched route", body.read)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d", recorder.Code)
	}
}
