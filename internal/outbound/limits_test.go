package outbound

import (
	"encoding/base64"
	"strings"
	"testing"
)

// ComposedSize is the single source of truth for the composed-message ceiling,
// shared by the httpapi send path and the agent HITL approve-override path.

// The total is subject + text + html + DECODED attachment bytes.
func TestComposedSize_SumsAllParts(t *testing.T) {
	att := Attachment{
		Filename:    "a.bin",
		ContentType: "application/octet-stream",
		Data:        base64.StdEncoding.EncodeToString(make([]byte, 1000)),
	}
	got := ComposedSize("subj", "text", "html", []Attachment{att})
	want := len("subj") + len("text") + len("html") + 1000
	if got != want {
		t.Fatalf("ComposedSize = %d, want %d", got, want)
	}
}

// Attachment size is counted on DECODED bytes, not the larger base64 wire size:
// N decoded bytes base64-encode to ~4/3·N on the wire, but only N must count.
func TestComposedSize_CountsDecodedNotWire(t *testing.T) {
	decoded := 3000
	att := Attachment{Data: base64.StdEncoding.EncodeToString(make([]byte, decoded))}
	got := ComposedSize("", "", "", []Attachment{att})
	if got != decoded {
		t.Fatalf("ComposedSize = %d, want %d (decoded, not wire)", got, decoded)
	}
	if wire := len(att.Data); got >= wire {
		t.Fatalf("expected decoded (%d) < wire (%d)", got, wire)
	}
}

// Embedded whitespace in the base64 payload is stripped before decoding, so a
// line-wrapped attachment still counts its true decoded size.
func TestComposedSize_StripsWhitespaceBeforeDecode(t *testing.T) {
	decoded := 900
	b64 := base64.StdEncoding.EncodeToString(make([]byte, decoded))
	wrapped := strings.Join(chunk(b64, 76), "\r\n") // MIME-style 76-col wrap
	att := Attachment{Data: wrapped}
	if got := ComposedSize("", "", "", []Attachment{att}); got != decoded {
		t.Fatalf("ComposedSize (wrapped) = %d, want %d", got, decoded)
	}
}

// A non-decodable payload falls back to the raw wire length so the total is
// never under-counted (fail-safe toward rejecting, not admitting).
func TestComposedSize_UndecodableFallsBackToWireLength(t *testing.T) {
	junk := "!!!not-base64!!!"
	att := Attachment{Data: junk}
	if got := ComposedSize("", "", "", []Attachment{att}); got != len(junk) {
		t.Fatalf("ComposedSize (junk) = %d, want fallback %d", got, len(junk))
	}
}

func chunk(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}
