package outbound

import (
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"testing"
)

// smtpErr mirrors PRODUCTION's error shape: net/smtp returns a *textproto.Error for
// a non-2xx reply, which sendOnce wraps with a command prefix via %w. The
// classifiers must key on the wrapped code, not the string — a bare
// errors.New("550 ...") is NOT what the relay produces.
func smtpErr(code int, msg string) error {
	return fmt.Errorf("rcpt to nobody@example.com: %w", &textproto.Error{Code: code, Msg: msg})
}

// connErr mirrors a wrapped network failure (dial timeout / refused).
func connErr(inner string) error {
	return fmt.Errorf("dial: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(inner)})
}

func TestIsPermanentSMTPError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{smtpErr(550, "5.1.1 User unknown"), true},
		{smtpErr(554, "Message rejected"), true},
		{smtpErr(550, "5.7.1 TLS required"), true}, // 5xx even though text has "tls"
		{smtpErr(421, "too many connections"), false},
		{smtpErr(450, "mailbox busy"), false},
		{connErr("connection refused"), false}, // connection — MUST retry
		{errors.New("outbound SMTP relay not configured"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsPermanentSMTPError(c.err); got != c.want {
			t.Errorf("IsPermanentSMTPError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestIsTransientSMTPError_4xx(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{smtpErr(421, "Too many connections"), true},
		{smtpErr(450, "Mailbox busy"), true},
		{smtpErr(451, "Temporary failure"), true},
		{smtpErr(550, "Mailbox not found"), false},
		{connErr("connection reset"), false},
		{errors.New("throttling: too many requests"), true}, // codeless throttle phrasing
		{nil, false},
	}
	for _, c := range cases {
		if got := isTransientSMTPError(c.err); got != c.want {
			t.Errorf("isTransientSMTPError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// TestIsConnectionError is the adversarial-review guard: a per-message SMTP verdict
// (has a code) is NEVER an outage, even if its text contains "tls"/"timeout";
// only codeless network failures + the not-configured relay are outages.
func TestIsConnectionError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{connErr("connect: connection refused"), true},
		{connErr("i/o timeout"), true},
		{errors.New("outbound SMTP relay not configured"), true},
		{smtpErr(550, "5.7.1 TLS required"), false}, // has "tls" but it's a 5xx verdict, NOT an outage
		{smtpErr(450, "try again"), false},          // 4xx verdict, not an outage
		{smtpErr(421, "connection timeout on our end"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsConnectionError(c.err); got != c.want {
			t.Errorf("IsConnectionError(%v) = %v, want %v", c.err, got, c.want)
		}
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
