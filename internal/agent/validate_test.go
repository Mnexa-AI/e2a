package agent

import "testing"

func TestValidateConversationID(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"empty", "", false},
		{"normal", "conv_abc123", false},
		{"with hyphens and dots", "task.2026-04-19.7f3a", false},
		{"contains LF — header injection attempt", "abc\nBcc: leak@evil.com", true},
		{"contains CRLF — header injection attempt", "abc\r\nBcc: leak@evil.com", true},
		{"lone CR", "abc\rdef", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConversationID(tc.id)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateConversationID(%q) err=%v, wantErr=%v", tc.id, err, tc.wantErr)
			}
		})
	}
}
