package httpapi

import (
	"testing"
)

func TestListConversationsEnvelope(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/conversations", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("want 1 conversation, got %d (%v)", len(items), body)
	}
	c := items[0].(map[string]any)
	if c["conversation_id"] != "conv_1" || c["message_count"].(float64) != 2 || c["has_unread"] != true {
		t.Fatalf("unexpected conversation summary: %+v", c)
	}
	// next_cursor is always null for conversations (legacy single-page parity).
	if body["next_cursor"] != nil {
		t.Fatalf("expected null next_cursor, got %v", body["next_cursor"])
	}
}

func TestGetConversationShape(t *testing.T) {
	srv := testServer(t)
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/conversations/conv_1", "good")
	if code != 200 {
		t.Fatalf("status %d body %v", code, body)
	}
	// Summary fields must be flattened to the top level (embedding), with
	// participants/labels/messages alongside.
	if body["conversation_id"] != "conv_1" {
		t.Fatalf("missing flattened summary fields: %v", body)
	}
	parts, _ := body["participants"].([]any)
	labels, _ := body["labels"].([]any)
	msgs, _ := body["messages"].([]any)
	if len(parts) != 2 || len(labels) != 1 || len(msgs) != 1 {
		t.Fatalf("unexpected detail: participants=%v labels=%v messages=%v", parts, labels, msgs)
	}
	if msgs[0].(map[string]any)["message_id"] != "msg_1" {
		t.Fatalf("unexpected member message: %v", msgs[0])
	}
}

func TestGetConversationNotFound(t *testing.T) {
	srv := testServer(t)
	code, _ := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/conversations/conv_missing", "good")
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}

func TestGetConversationRejectsCRLF(t *testing.T) {
	srv := testServer(t)
	// %0A is a newline — must be rejected before the store lookup.
	code, body := getJSON(t, srv.URL+"/v1/agents/support%40acme.com/conversations/conv%0Abad", "good")
	if code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
	if errObj, _ := body["error"].(map[string]any); errObj["code"] != "invalid_request" {
		t.Fatalf("want invalid_request, got %v", body)
	}
}
