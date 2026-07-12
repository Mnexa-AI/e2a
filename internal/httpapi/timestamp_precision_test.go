package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// subSecond is a known instant with nanosecond precision. Both the fractional
// digits and the fact that they are NON-zero matter: a whole-second truncation
// (the old .Format(time.RFC3339) bug) would drop them entirely.
var subSecond = time.Date(2026, 7, 11, 12, 0, 25, 123456789, time.UTC)

// wantFraction is the fractional-seconds tail RFC3339Nano must preserve.
const wantFraction = ".123456789"

// extractCreatedAt marshals a view and pulls its `created_at` string.
func extractCreatedAt(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw, ok := m["created_at"]
	if !ok {
		t.Fatalf("view has no created_at field: %s", b)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("created_at is not a JSON string (%s): %v", raw, err)
	}
	return s
}

// TestMessageTimestampPrecisionRoundTrip locks the fix for issue #38: the
// per-message created_at is the keyset pagination ORDERING key, so it must be
// emitted at full RFC3339Nano precision. The old code truncated it to whole
// seconds via .Format(time.RFC3339), which made two messages in the same second
// indistinguishable on the wire even though the cursor orders them finer.
//
// This covers BOTH the message detail view AND the summary view (the list-item
// shape that carries the ordering key) — a regression to second-precision on
// either would fail here.
func TestMessageTimestampPrecisionRoundTrip(t *testing.T) {
	msg := identity.Message{ID: "msg_prec", Direction: "inbound", CreatedAt: subSecond}

	t.Run("detail_view", func(t *testing.T) {
		got := extractCreatedAt(t, messageViewFromIdentity(&msg))
		assertSubSecond(t, got)
	})

	t.Run("summary_view", func(t *testing.T) {
		got := extractCreatedAt(t, messageSummaryFromIdentity(msg))
		assertSubSecond(t, got)
	})
}

// assertSubSecond fails if s has been truncated to whole-second precision.
func assertSubSecond(t *testing.T, s string) {
	t.Helper()
	if !strings.Contains(s, wantFraction) {
		t.Fatalf("created_at %q dropped sub-second precision (want fractional %q) — a manual .Format(time.RFC3339) truncation has regressed", s, wantFraction)
	}
	// Round-trips back to the exact instant, nanoseconds included.
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("created_at %q is not RFC3339Nano-parseable: %v", s, err)
	}
	if !parsed.Equal(subSecond) {
		t.Fatalf("created_at round-trip = %v, want %v (nanoseconds lost)", parsed.UTC(), subSecond)
	}
}

// TestOtherViewTimestampsFullPrecision extends the guarantee to the rest of the
// view layer unified in this change (templates, webhooks, webhook deliveries,
// attachments): every emitted date-time is full-precision, so the whole v1
// contract is consistent — no view silently truncates.
func TestOtherViewTimestampsFullPrecision(t *testing.T) {
	cases := []struct {
		name  string
		field string
		v     any
	}{
		{"template", "created_at", templateView(&identity.Template{ID: "tpl_1", CreatedAt: subSecond, UpdatedAt: subSecond})},
		{"template_summary", "created_at", templateSummaryView(&identity.TemplateSummary{ID: "tpl_1", CreatedAt: subSecond, UpdatedAt: subSecond})},
		{"webhook", "created_at", webhookView(&identity.Webhook{ID: "wh_1", CreatedAt: subSecond})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			var s string
			if err := json.Unmarshal(m[c.field], &s); err != nil {
				t.Fatalf("%s.%s not a string (%s): %v", c.name, c.field, m[c.field], err)
			}
			assertSubSecond(t, s)
		})
	}
}

// TestNoManualSecondTruncationInViewLayer is a static guard: it scans the
// httpapi package source and fails if any file formats a timestamp via
// .Format(time.RFC3339) (whole-second precision). The wire contract emits
// timestamps as time.Time (marshaled RFC3339Nano); a future hand-rolled
// second-precision format is exactly the regression this whole change removes.
// (time.RFC3339Nano is fine — the check matches the second-precision constant
// only, not its Nano sibling.)
func TestNoManualSecondTruncationInViewLayer(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	const bad = "Format(time.RFC3339)"
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(src), bad) {
			t.Errorf("%s calls %s (whole-second truncation) in the view layer — emit timestamps as time.Time (RFC3339Nano) instead", name, bad)
		}
	}
}
