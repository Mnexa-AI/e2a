package httpapi

import (
	"encoding/json"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	type cur struct {
		After string `json:"after"`
		Dir   string `json:"dir"`
	}
	in := cur{After: "msg_123", Dir: "inbound"}
	enc, err := EncodeCursor(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc == "" {
		t.Fatal("expected non-empty cursor")
	}
	var out cur
	if err := DecodeCursor(enc, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v want %+v", out, in)
	}
}

func TestDecodeCursorEmptyIsNoop(t *testing.T) {
	var out struct{ A string }
	if err := DecodeCursor("", &out); err != nil {
		t.Fatalf("empty cursor should be a no-op, got %v", err)
	}
}

func TestDecodeCursorMalformed(t *testing.T) {
	var out struct{ A string }
	// Not valid base64url.
	if err := DecodeCursor("!!!not-base64!!!", &out); err != ErrInvalidCursor {
		t.Fatalf("want ErrInvalidCursor, got %v", err)
	}
	// Valid base64url, but not valid JSON for dst.
	bad := EncodeMust(t, "a string, not the struct")
	if err := DecodeCursor(bad, &out); err != ErrInvalidCursor {
		t.Fatalf("want ErrInvalidCursor on bad json, got %v", err)
	}
}

func TestNewPageNullVsEmpty(t *testing.T) {
	// No next cursor -> next_cursor is JSON null; nil items -> [].
	p := NewPage[int](nil, "")
	raw, _ := json.Marshal(p)
	if string(raw) != `{"items":[],"next_cursor":null}` {
		t.Fatalf("unexpected empty page json: %s", raw)
	}

	p2 := NewPage([]int{1, 2}, "next")
	raw2, _ := json.Marshal(p2)
	if string(raw2) != `{"items":[1,2],"next_cursor":"next"}` {
		t.Fatalf("unexpected page json: %s", raw2)
	}
}

// EncodeMust is a test helper that encodes a cursor or fails the test.
func EncodeMust(t *testing.T, payload any) string {
	t.Helper()
	s, err := EncodeCursor(payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return s
}
