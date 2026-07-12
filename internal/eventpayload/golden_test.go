package eventpayload_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/eventpayload"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// update regenerates the committed golden fixtures instead of asserting
// against them: `go test ./internal/eventpayload -run TestGoldenFixtures -update`.
var update = flag.Bool("update", false, "regenerate the golden payload fixtures under testdata/")

// fixtureCreatedAt is the canonical envelope timestamp shared by every
// fixture. Nanosecond precision on purpose — it locks the RFC3339Nano wire
// format of created_at/received_at.
var fixtureCreatedAt = time.Date(2026, 7, 1, 10, 30, 0, 123456789, time.UTC)

// canonicalEvents returns one fully-populated canonical event per STABLE
// event type — the single source of truth the fixtures are generated from.
// The per-builder tests (relay, agent, delivery, ws) and the TS/Python SDK
// payload tests all assert against the generated files, so a change here (or
// in any struct) that alters the wire bytes fails every side until the
// fixtures are consciously regenerated and reviewed.
func canonicalEvents() []struct {
	fixture string
	event   webhookpub.Event
} {
	return []struct {
		fixture string
		event   webhookpub.Event
	}{
		{
			fixture: "email.received.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("msg_01h2xcejqtf2nbrexx3vqjhp41", webhookpub.EventEmailReceived),
				Type:      webhookpub.EventEmailReceived,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailReceivedData{
					MessageID:         "msg_01h2xcejqtf2nbrexx3vqjhp41",
					AgentEmail:        "support@agents.example.com",
					Direction:         "inbound",
					ConversationID:    "conv_9f8e7d6c",
					From:              "reply@customer.example.com",
					AuthenticatedFrom: "alice@customer.example.com",
					To:                []string{"support@agents.example.com"},
					CC:                []string{"ops@customer.example.com"},
					ReplyTo:           []string{"reply@customer.example.com"},
					DeliveredTo:       "support@agents.example.com",
					Subject:           "Order #1234 delayed",
					AuthHeaders: map[string]string{
						"X-E2A-Auth-Sender":   "alice@customer.example.com",
						"X-E2A-Auth-Verified": "true",
					},
					ReceivedAt: fixtureCreatedAt,
					Attachments: []eventpayload.AttachmentMeta{
						{Filename: "invoice.pdf", ContentType: "application/pdf", SizeBytes: 12345, Index: 0},
					},
				},
			},
		},
		{
			fixture: "email.sent.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("msg_01h2xcejqtf2nbrexx3vqjhp42", webhookpub.EventEmailSent),
				Type:      webhookpub.EventEmailSent,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailSentData{
					MessageID:         "msg_01h2xcejqtf2nbrexx3vqjhp42",
					AgentEmail:        "support@agents.example.com",
					Direction:         "outbound",
					ConversationID:    "conv_9f8e7d6c",
					ProviderMessageID: "0100019283abcdef-1a2b3c4d-0000",
					Method:            "smtp",
					From:              "support@agents.example.com",
					To:                []string{"alice@customer.example.com"},
					CC:                []string{"ops@customer.example.com"},
					BCC:               []string{"audit@agents.example.com"},
					Subject:           "Re: Order #1234 delayed",
					MessageType:       "reply",
				},
			},
		},
		{
			fixture: "email.failed.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("msg_01h2xcejqtf2nbrexx3vqjhp43", webhookpub.EventEmailFailed),
				Type:      webhookpub.EventEmailFailed,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailFailedData{
					MessageID:      "msg_01h2xcejqtf2nbrexx3vqjhp43",
					AgentEmail:     "support@agents.example.com",
					Direction:      "outbound",
					ConversationID: "conv_9f8e7d6c",
					Method:         "smtp",
					From:           "support@agents.example.com",
					To:             []string{"alice@customer.example.com"},
					CC:             []string{"ops@customer.example.com"},
					BCC:            []string{"audit@agents.example.com"},
					Subject:        "Re: Order #1234 delayed",
					MessageType:    "send",
					Reason:         "550 5.1.1 user unknown",
					// reason_code / retryable are omitted: the async send worker
					// (today's only email.failed emitter) has no classification
					// beyond the diagnostic string. The schema keeps both fields.
				},
			},
		},
		{
			fixture: "email.delivered.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("email.delivered|msg_01h2xcejqtf2nbrexx3vqjhp44|alice@customer.example.com"),
				Type:      webhookpub.EventEmailDelivered,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailDeliveredData{
					MessageID:   "msg_01h2xcejqtf2nbrexx3vqjhp44",
					AgentEmail:  "support@agents.example.com",
					Direction:   "outbound",
					DeliveredTo: "alice@customer.example.com",
					Subject:     "Re: Order #1234 delayed",
					// smtp_detail omitted: SES Delivery notifications carry no
					// per-recipient diagnostic.
				},
			},
		},
		{
			fixture: "email.bounced.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("email.bounced|msg_01h2xcejqtf2nbrexx3vqjhp44|bob@customer.example.com"),
				Type:      webhookpub.EventEmailBounced,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailBouncedData{
					MessageID:     "msg_01h2xcejqtf2nbrexx3vqjhp44",
					AgentEmail:    "support@agents.example.com",
					Direction:     "outbound",
					DeliveredTo:   "bob@customer.example.com",
					Subject:       "Re: Order #1234 delayed",
					SMTPDetail:    "550 5.1.1 no such user",
					BounceType:    "permanent",
					BounceSubType: "General",
				},
			},
		},
		{
			fixture: "email.complained.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("email.complained|msg_01h2xcejqtf2nbrexx3vqjhp44|carol@customer.example.com"),
				Type:      webhookpub.EventEmailComplained,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailComplainedData{
					MessageID:   "msg_01h2xcejqtf2nbrexx3vqjhp44",
					AgentEmail:  "support@agents.example.com",
					Direction:   "outbound",
					DeliveredTo: "carol@customer.example.com",
					Subject:     "Re: Order #1234 delayed",
					SMTPDetail:  "abuse",
				},
			},
		},
		{
			fixture: "domain.sending_verified.json",
			event: webhookpub.Event{
				// domain.* sender-identity events mint random ids (no natural
				// dedup key); the fixture pins a representative literal.
				ID:        "evt_0123456789abcdef0123456789abcdef",
				Type:      webhookpub.EventDomainSendingVerified,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.DomainSendingVerifiedData{
					Domain:        "mail.customer.example.com",
					SendingStatus: "verified",
				},
			},
		},
		{
			fixture: "domain.sending_failed.json",
			event: webhookpub.Event{
				ID:        "evt_fedcba9876543210fedcba9876543210",
				Type:      webhookpub.EventDomainSendingFailed,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.DomainSendingFailedData{
					Domain:        "mail.customer.example.com",
					SendingStatus: "failed",
					Reason:        "DKIM tokens not found in DNS",
				},
			},
		},
		{
			fixture: "domain.suppression_added.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("domain.suppression_added|user_7a6b5c4d|bob@customer.example.com"),
				Type:      webhookpub.EventDomainSuppressionAdded,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.DomainSuppressionAddedData{
					Address:   "bob@customer.example.com",
					Source:    "bounce",
					Reason:    "550 5.1.1 no such user",
					MessageID: "msg_01h2xcejqtf2nbrexx3vqjhp44",
				},
			},
		},
	}
}

// minimalEvents returns the REQUIRED-FIELDS-ONLY variant for every stable
// event that has optional fields — the presence-semantics lock the maximal
// fixtures above cannot provide: with only fully-populated fixtures, a
// flipped/added `omitempty` (or a required field accidentally made
// omittable) passes every byte-level test. Each minimal fixture pins that
// the optional fields are genuinely ABSENT from the wire when unset and the
// required fields still serialize (present-but-empty where applicable).
// domain.sending_verified has no optional fields, so no minimal variant.
func minimalEvents() []struct {
	fixture string
	event   webhookpub.Event
} {
	return []struct {
		fixture string
		event   webhookpub.Event
	}{
		{
			// received without conversation_id/cc/reply_to/attachments.
			// auth_headers/to/authenticated_from are REQUIRED: an
			// unauthenticated minimal intake serializes them present-but-empty,
			// never absent.
			fixture: "email.received.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("msg_01h2xcejqtf2nbrexx3vqjhp41", webhookpub.EventEmailReceived),
				Type:      webhookpub.EventEmailReceived,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailReceivedData{
					MessageID:         "msg_01h2xcejqtf2nbrexx3vqjhp41",
					AgentEmail:        "support@agents.example.com",
					Direction:         "inbound",
					From:              "reply@customer.example.com",
					AuthenticatedFrom: "",
					To:                []string{"support@agents.example.com"},
					DeliveredTo:       "support@agents.example.com",
					Subject:           "Order #1234 delayed",
					AuthHeaders:       map[string]string{},
					ReceivedAt:        fixtureCreatedAt,
				},
			},
		},
		{
			// sent without conversation_id/cc/bcc.
			fixture: "email.sent.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("msg_01h2xcejqtf2nbrexx3vqjhp42", webhookpub.EventEmailSent),
				Type:      webhookpub.EventEmailSent,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailSentData{
					MessageID:         "msg_01h2xcejqtf2nbrexx3vqjhp42",
					AgentEmail:        "support@agents.example.com",
					Direction:         "outbound",
					ProviderMessageID: "0100019283abcdef-1a2b3c4d-0000",
					Method:            "smtp",
					From:              "support@agents.example.com",
					To:                []string{"alice@customer.example.com"},
					Subject:           "Re: Order #1234 delayed",
					MessageType:       "reply",
				},
			},
		},
		{
			// failed without conversation_id/cc/bcc/reason_code/retryable.
			fixture: "email.failed.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("msg_01h2xcejqtf2nbrexx3vqjhp43", webhookpub.EventEmailFailed),
				Type:      webhookpub.EventEmailFailed,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailFailedData{
					MessageID:   "msg_01h2xcejqtf2nbrexx3vqjhp43",
					AgentEmail:  "support@agents.example.com",
					Direction:   "outbound",
					Method:      "smtp",
					From:        "support@agents.example.com",
					To:          []string{"alice@customer.example.com"},
					Subject:     "Re: Order #1234 delayed",
					MessageType: "send",
					Reason:      "550 5.1.1 user unknown",
				},
			},
		},
		{
			// delivered without subject/smtp_detail.
			fixture: "email.delivered.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("email.delivered|msg_01h2xcejqtf2nbrexx3vqjhp44|alice@customer.example.com"),
				Type:      webhookpub.EventEmailDelivered,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailDeliveredData{
					MessageID:   "msg_01h2xcejqtf2nbrexx3vqjhp44",
					AgentEmail:  "support@agents.example.com",
					Direction:   "outbound",
					DeliveredTo: "alice@customer.example.com",
				},
			},
		},
		{
			// bounced without subject/smtp_detail/bounce_sub_type — the
			// required bounce_type stays.
			fixture: "email.bounced.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("email.bounced|msg_01h2xcejqtf2nbrexx3vqjhp44|bob@customer.example.com"),
				Type:      webhookpub.EventEmailBounced,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailBouncedData{
					MessageID:   "msg_01h2xcejqtf2nbrexx3vqjhp44",
					AgentEmail:  "support@agents.example.com",
					Direction:   "outbound",
					DeliveredTo: "bob@customer.example.com",
					BounceType:  "permanent",
				},
			},
		},
		{
			// complained without subject/smtp_detail.
			fixture: "email.complained.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("email.complained|msg_01h2xcejqtf2nbrexx3vqjhp44|carol@customer.example.com"),
				Type:      webhookpub.EventEmailComplained,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.EmailComplainedData{
					MessageID:   "msg_01h2xcejqtf2nbrexx3vqjhp44",
					AgentEmail:  "support@agents.example.com",
					Direction:   "outbound",
					DeliveredTo: "carol@customer.example.com",
				},
			},
		},
		{
			// sending_failed without reason.
			fixture: "domain.sending_failed.min.json",
			event: webhookpub.Event{
				ID:        "evt_fedcba9876543210fedcba9876543210",
				Type:      webhookpub.EventDomainSendingFailed,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.DomainSendingFailedData{
					Domain:        "mail.customer.example.com",
					SendingStatus: "failed",
				},
			},
		},
		{
			// suppression_added without reason/message_id.
			fixture: "domain.suppression_added.min.json",
			event: webhookpub.Event{
				ID:        webhookpub.DeterministicEventID("domain.suppression_added|user_7a6b5c4d|bob@customer.example.com"),
				Type:      webhookpub.EventDomainSuppressionAdded,
				CreatedAt: fixtureCreatedAt,
				Data: eventpayload.DomainSuppressionAddedData{
					Address: "bob@customer.example.com",
					Source:  "bounce",
				},
			},
		},
	}
}

// TestGoldenFixtures is the canonical envelope-level golden lock: the wire
// envelope of each stable event, marshaled from the typed payload structs,
// must byte-for-byte equal the committed fixture — for BOTH the maximal
// (fully-populated) fixtures and the required-fields-only `.min.json`
// variants (which lock the omitempty presence semantics). Everything asserts
// against these files — the per-builder server tests AND the TS/Python SDK
// tests — so a payload change that isn't a conscious, reviewed fixture
// regeneration fails on every surface at once.
func TestGoldenFixtures(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range append(canonicalEvents(), minimalEvents()...) {
		c := c
		seen[c.event.Type] = true
		t.Run(c.fixture, func(t *testing.T) {
			got, err := json.MarshalIndent(c.event.AsEnvelope(), "", "  ")
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", c.fixture)
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("regenerated %s (%d bytes)", path, len(got))
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s (first time? run with -update): %v", path, err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("fixture %s drifted from the typed payload structs — if the change is intentional, regenerate with -update and update the SDK types + docs\n got: %s\nwant: %s", path, got, want)
			}
		})
	}

	// Coverage gate: every STABLE event type must have a fixture; the beta
	// events must NOT (their payloads are open/unstable maps by design).
	stable := []string{
		webhookpub.EventEmailReceived,
		webhookpub.EventEmailSent,
		webhookpub.EventEmailFailed,
		webhookpub.EventEmailDelivered,
		webhookpub.EventEmailBounced,
		webhookpub.EventEmailComplained,
		webhookpub.EventDomainSendingVerified,
		webhookpub.EventDomainSendingFailed,
		webhookpub.EventDomainSuppressionAdded,
	}
	for _, typ := range stable {
		if !seen[typ] {
			t.Errorf("stable event %s has no golden fixture", typ)
		}
	}
	beta := []string{
		webhookpub.EventEmailFlagged,
		webhookpub.EventEmailBlocked,
		webhookpub.EventEmailReviewRequested,
		webhookpub.EventEmailReviewApproved,
		webhookpub.EventEmailReviewRejected,
	}
	for _, typ := range beta {
		if seen[typ] {
			t.Errorf("beta event %s must not have a golden fixture (its payload is explicitly unstable)", typ)
		}
	}
	if len(stable)+len(beta) != len(webhookpub.AllEventTypes) {
		t.Errorf("event catalog changed (%d types) — classify the new type as stable (add a struct + fixture) or beta (map payload) here", len(webhookpub.AllEventTypes))
	}
}

// TestSchemaVersionOnEnvelope pins that the fixtures carry the envelope
// schema_version the SDKs parse.
func TestSchemaVersionOnEnvelope(t *testing.T) {
	env := canonicalEvents()[0].event.AsEnvelope()
	if env.SchemaVersion != "1" {
		t.Fatalf("envelope schema_version = %q, want \"1\"", env.SchemaVersion)
	}
}
