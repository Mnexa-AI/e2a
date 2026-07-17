package outbound

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/config"
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
		// Parse alone only wraps a bare id — sendOnce then appends the provider
		// domain via qualifyMessageIDDomain. The composed capture path is pinned
		// in TestCapturedMessageIDMatchesOnWireHeader.
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

func TestQualifyMessageIDDomain(t *testing.T) {
	sesCfg := &config.OutboundSMTPConfig{Host: "email-smtp.us-east-2.amazonaws.com"}
	tests := []struct {
		name string
		id   string
		cfg  *config.OutboundSMTPConfig
		want string
	}{
		{"bare id gains the region domain derived from the SES host",
			"<010f-abc-000000>", sesCfg, "<010f-abc-000000@us-east-2.amazonses.com>"},
		{"already-qualified id passes through untouched",
			"<010f-abc-000000@us-east-2.amazonses.com>", sesCfg, "<010f-abc-000000@us-east-2.amazonses.com>"},
		{"explicit message_id_domain wins over host derivation",
			"<010f-abc-000000>",
			&config.OutboundSMTPConfig{Host: "email-smtp.us-east-2.amazonaws.com", MessageIDDomain: "eu-west-1.amazonses.com"},
			"<010f-abc-000000@eu-west-1.amazonses.com>"},
		{"non-SES host with no override leaves the id alone (never fabricate)",
			"<010f-abc-000000>", &config.OutboundSMTPConfig{Host: "smtp.example.com"}, "<010f-abc-000000>"},
		{"empty id stays empty", "", sesCfg, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualifyMessageIDDomain(tt.id, tt.cfg); got != tt.want {
				t.Errorf("qualifyMessageIDDomain(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestSESRegionFromHost(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"email-smtp.us-east-2.amazonaws.com", "us-east-2"},
		{"email-smtp.eu-west-1.amazonaws.com", "eu-west-1"},
		{"smtp.example.com", ""},
		{"email-smtp.amazonaws.com", ""},     // no region segment
		{"email-smtp.a.b.amazonaws.com", ""}, // region can't contain dots
		{"localhost", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sesRegionFromHost(tt.host); got != tt.want {
			t.Errorf("sesRegionFromHost(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}

// TestCapturedMessageIDMatchesOnWireHeader pins the full capture path
// (parse → qualify) on the exact 250 shape production SES SMTP returns: the
// id arrives BARE (no brackets, no domain), while the Message-ID SES stamps
// on the delivered message is <id@<region>.amazonses.com>. The captured value
// is stored as provider_message_id and copied verbatim into a reply's
// In-Reply-To/References — so anything other than the on-wire form forks the
// recipient's thread in every RFC 5322 client.
func TestCapturedMessageIDMatchesOnWireHeader(t *testing.T) {
	cfg := &config.OutboundSMTPConfig{Host: "email-smtp.us-east-2.amazonaws.com"}
	resp := "Ok 010f019d4b3843be-53882e6f-46de-4221-a56a-ba993e8f83e8-000000"
	want := "<010f019d4b3843be-53882e6f-46de-4221-a56a-ba993e8f83e8-000000@us-east-2.amazonses.com>"
	if got := qualifyMessageIDDomain(parseMessageIDFromResponse(resp), cfg); got != want {
		t.Errorf("captured id = %q, want the on-wire Message-ID %q", got, want)
	}
}
