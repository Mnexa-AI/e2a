package httpapi

import "testing"

// A send whose attachment data is not valid base64 must be rejected with
// 400 invalid_attachment at the boundary. Without this the malformed data is
// passed verbatim into the MIME body and only fails downstream at the SMTP
// relay, surfacing to the caller as a generic 500.
func TestSendRejectsBadBase64Attachment(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"r@x.com"}, "subject": "Hi", "body": "hello",
		"attachments": []map[string]any{
			{"filename": "bad.bin", "content_type": "application/octet-stream", "data": "!!!not-base64!!!"},
		},
	})
	if code != 400 || errCode(body) != "invalid_attachment" {
		t.Fatalf("want 400 invalid_attachment, got %d %v", code, body)
	}
}

// A valid base64 attachment passes validation. The embedded newline exercises
// the whitespace tolerance (callers may pre-wrap their base64 per RFC 2045).
func TestSendAcceptsValidBase64Attachment(t *testing.T) {
	srv := testServer(t)
	code, _ := postJSON(t, srv.URL+sendURL, "good", map[string]any{
		"to": []string{"r@x.com"}, "subject": "Hi", "body": "hello",
		"attachments": []map[string]any{
			{"filename": "ok.txt", "content_type": "text/plain", "data": "aGVsbG8K\n"},
		},
	})
	if code != 200 {
		t.Fatalf("valid base64 attachment: want 200, got %d", code)
	}
}

// The reply path shares the same funnel, so it enforces the same check.
func TestReplyRejectsBadBase64Attachment(t *testing.T) {
	srv := testServer(t)
	code, body := postJSON(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_in1/reply", "good", map[string]any{
		"body": "re",
		"attachments": []map[string]any{
			{"filename": "bad.bin", "content_type": "application/octet-stream", "data": "@@notbase64@@"},
		},
	})
	if code != 400 || errCode(body) != "invalid_attachment" {
		t.Fatalf("reply: want 400 invalid_attachment, got %d %v", code, body)
	}
}
