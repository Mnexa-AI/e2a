package agent

import "testing"

// TestClassifyDKIM covers the verify-probe DKIM states, especially the
// "mismatch" state that distinguishes a truncated/clipped published record
// from an absent one (the silent failure mode that left domains "pending
// forever" after an agent published a short DKIM TXT).
func TestClassifyDKIM(t *testing.T) {
	const want = "MIIBIjANBgkqfullkeyAQAB"

	cases := []struct {
		name     string
		txts     []string
		expected string
	}{
		{
			name:     "exact match is found",
			txts:     []string{"v=DKIM1; k=rsa; p=" + want},
			expected: "found",
		},
		{
			name: "match wins over a stale mismatching key (rotation)",
			txts: []string{
				"v=DKIM1; k=rsa; p=MIIBIjANBgkqOLDkey",
				"v=DKIM1; k=rsa; p=" + want,
			},
			expected: "found",
		},
		{
			name:     "published but truncated key is a mismatch, not missing",
			txts:     []string{"v=DKIM1; k=rsa; p=MIIBIjANBgkqfullke"}, // tail clipped
			expected: "mismatch",
		},
		{
			name:     "published unrelated key is a mismatch",
			txts:     []string{"v=DKIM1; k=rsa; p=SOMEOTHERKEYAQAB"},
			expected: "mismatch",
		},
		{
			name:     "no records is missing",
			txts:     nil,
			expected: "missing",
		},
		{
			name:     "record without a p= payload is missing",
			txts:     []string{"v=spf1 include:amazonses.com ~all"},
			expected: "missing",
		},
		{
			name:     "chunk-split record (stray spaces) still matches",
			txts:     []string{"v=DKIM1; k=rsa; p=MIIBIjANBgkq fullkeyAQAB"}, // split mid-key
			expected: "found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDKIM(tc.txts, want); got != tc.expected {
				t.Fatalf("classifyDKIM(%q) = %q, want %q", tc.txts, got, tc.expected)
			}
		})
	}
}
