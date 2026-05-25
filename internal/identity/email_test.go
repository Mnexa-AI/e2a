package identity

import "testing"

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"already lowercase", "alice@example.com", "alice@example.com"},
		{"mixed case", "Alice@Example.COM", "alice@example.com"},
		{"all uppercase", "ALICE@EXAMPLE.COM", "alice@example.com"},
		{"leading whitespace", "  alice@example.com", "alice@example.com"},
		{"trailing whitespace", "alice@example.com  ", "alice@example.com"},
		{"surrounding whitespace + case", "  Alice@Example.COM  ", "alice@example.com"},
		{"tab whitespace", "\talice@example.com\t", "alice@example.com"},
		{"local-part with plus", "Alice+Filter@Example.com", "alice+filter@example.com"},
		// Inner whitespace is NOT a valid email anyway; we just confirm we
		// don't strip inside the address — the validator catches it later.
		{"inner space preserved", "alice @example.com", "alice @example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEmail(tt.in); got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeEmailIdempotent guards against silent regressions where a
// future change adds non-idempotent behavior (e.g. URL-decoding, IDN
// punycode conversion) without updating the contract: calling
// NormalizeEmail on its own output must return the same string.
func TestNormalizeEmailIdempotent(t *testing.T) {
	for _, in := range []string{
		"alice@example.com",
		"Alice@Example.COM",
		"  Alice@Example.COM  ",
		"",
	} {
		once := NormalizeEmail(in)
		twice := NormalizeEmail(once)
		if once != twice {
			t.Errorf("not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}
