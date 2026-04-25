package outbound

import (
	"testing"
)

func TestIsTransientSMTPError_4xx(t *testing.T) {
	tests := []struct {
		err    string
		want   bool
	}{
		{"421 Too many connections", true},
		{"450 Rate limit exceeded", true},
		{"451 Temporary service failure", true},
		{"550 Mailbox not found", false},
		{"553 Invalid address", false},
		{"connection reset by peer", false},
		{"throttling: too many requests", true},
		{"Rate limit exceeded, try again later", true},
		{"please try again later", true},
	}

	for _, tt := range tests {
		got := isTransientSMTPError(errString(tt.err))
		if got != tt.want {
			t.Errorf("isTransientSMTPError(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestIsTransientSMTPError_Nil(t *testing.T) {
	if isTransientSMTPError(nil) {
		t.Error("expected false for nil error")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestParseMessageIDFromResponse(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Ok <010f019d2bd82cd5-49c4925c@us-east-2.amazonses.com>", "<010f019d2bd82cd5-49c4925c@us-east-2.amazonses.com>"},
		{"<simple@example.com>", "<simple@example.com>"},
		{"Ok no-angle-brackets", "<no-angle-brackets>"},
		{"Ok 010f019d4b3843be-53882e6f-46de-4221-a56a-ba993e8f83e8-000000", "<010f019d4b3843be-53882e6f-46de-4221-a56a-ba993e8f83e8-000000>"},
		{"", ""},
		{"  whitespace  ", "<whitespace>"},
	}

	for _, tt := range tests {
		got := parseMessageIDFromResponse(tt.input)
		if got != tt.want {
			t.Errorf("parseMessageIDFromResponse(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
