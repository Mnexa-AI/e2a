package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EventKind is the normalized SES event category.
type EventKind string

const (
	KindDelivery      EventKind = "Delivery"
	KindBounce        EventKind = "Bounce"
	KindComplaint     EventKind = "Complaint"
	KindDeliveryDelay EventKind = "DeliveryDelay"
	KindSend          EventKind = "Send"
	KindReject        EventKind = "Reject"
	KindOther         EventKind = "Other"
)

// RecipientOutcome is the delivery result for one address from one SES event.
type RecipientOutcome struct {
	Address  string
	Status   Status
	Detail   string
	Suppress bool // hard bounce or complaint → add to the suppression list
}

// Event is a normalized SES notification: which message, which event, and the
// per-recipient outcomes. The message rollup is the worst status across
// Recipients by Merge precedence.
type Event struct {
	Kind         EventKind
	SESMessageID string // the mail.messageId — correlates to messages.provider_message_id
	Recipients   []RecipientOutcome
	// BounceType / BounceSubType carry the SES bounce classification (Bounce
	// events only; empty otherwise). BounceType is normalized to the stable
	// event vocabulary — permanent | transient | undetermined — the value
	// email.bounced's bounce_type field emits; BounceSubType is the raw SES
	// bounceSubType (e.g. General, NoEmail, MailboxFull).
	BounceType    string
	BounceSubType string
}

// sesNotification is the SES event JSON carried in the SNS Message field.
// Config-set event publishing uses `eventType`; legacy identity notifications
// use `notificationType` — accept either.
type sesNotification struct {
	EventType        string `json:"eventType"`
	NotificationType string `json:"notificationType"`
	Mail             struct {
		MessageID   string   `json:"messageId"`
		Destination []string `json:"destination"`
	} `json:"mail"`
	Bounce *struct {
		BounceType        string `json:"bounceType"` // Permanent | Transient | Undetermined
		BounceSubType     string `json:"bounceSubType"`
		BouncedRecipients []struct {
			EmailAddress   string `json:"emailAddress"`
			DiagnosticCode string `json:"diagnosticCode"`
		} `json:"bouncedRecipients"`
	} `json:"bounce"`
	Complaint *struct {
		ComplainedRecipients []struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"complainedRecipients"`
		ComplaintFeedbackType string `json:"complaintFeedbackType"`
	} `json:"complaint"`
	Delivery *struct {
		Recipients []string `json:"recipients"`
	} `json:"delivery"`
	DeliveryDelay *struct {
		DelayedRecipients []struct {
			EmailAddress   string `json:"emailAddress"`
			DiagnosticCode string `json:"diagnosticCode"`
		} `json:"delayedRecipients"`
	} `json:"deliveryDelay"`
}

// ParseSESNotification parses the SES event JSON (the decoded SNS Message
// body) into a normalized Event. Returns an error for malformed JSON or a
// missing message id; an unrecognized event kind yields KindOther with no
// recipient outcomes (caller no-ops).
func ParseSESNotification(messageBody []byte) (*Event, error) {
	var n sesNotification
	if err := json.Unmarshal(messageBody, &n); err != nil {
		return nil, fmt.Errorf("parse SES notification: %w", err)
	}
	typ := n.EventType
	if typ == "" {
		typ = n.NotificationType
	}
	if n.Mail.MessageID == "" {
		return nil, fmt.Errorf("SES notification missing mail.messageId")
	}
	ev := &Event{SESMessageID: n.Mail.MessageID}

	switch typ {
	case "Delivery":
		ev.Kind = KindDelivery
		recips := n.Mail.Destination
		if n.Delivery != nil && len(n.Delivery.Recipients) > 0 {
			recips = n.Delivery.Recipients
		}
		for _, a := range recips {
			ev.Recipients = append(ev.Recipients, RecipientOutcome{Address: norm(a), Status: StatusDelivered})
		}
	case "Bounce":
		ev.Kind = KindBounce
		if n.Bounce != nil {
			ev.BounceType = normalizeBounceType(n.Bounce.BounceType)
			ev.BounceSubType = n.Bounce.BounceSubType
			// Only a Permanent (hard) bounce suppresses; Transient/Undetermined
			// are recorded as bounced but not auto-suppressed (decision 9: never
			// suppress on a single unverified/soft signal).
			hard := ev.BounceType == "permanent"
			for _, r := range n.Bounce.BouncedRecipients {
				ev.Recipients = append(ev.Recipients, RecipientOutcome{
					Address: norm(r.EmailAddress), Status: StatusBounced,
					Detail: r.DiagnosticCode, Suppress: hard,
				})
			}
		}
	case "Complaint":
		ev.Kind = KindComplaint
		if n.Complaint != nil {
			for _, r := range n.Complaint.ComplainedRecipients {
				ev.Recipients = append(ev.Recipients, RecipientOutcome{
					Address: norm(r.EmailAddress), Status: StatusComplained,
					Detail: n.Complaint.ComplaintFeedbackType, Suppress: true,
				})
			}
		}
	case "DeliveryDelay":
		ev.Kind = KindDeliveryDelay
		if n.DeliveryDelay != nil {
			for _, r := range n.DeliveryDelay.DelayedRecipients {
				ev.Recipients = append(ev.Recipients, RecipientOutcome{
					Address: norm(r.EmailAddress), Status: StatusDeferred, Detail: r.DiagnosticCode,
				})
			}
		}
	case "Send":
		ev.Kind = KindSend
	case "Reject":
		ev.Kind = KindReject
		for _, a := range n.Mail.Destination {
			ev.Recipients = append(ev.Recipients, RecipientOutcome{Address: norm(a), Status: StatusFailed})
		}
	default:
		ev.Kind = KindOther
	}
	return ev, nil
}

func norm(addr string) string { return strings.ToLower(strings.TrimSpace(addr)) }

// normalizeBounceType maps SES's bounceType (Permanent | Transient |
// Undetermined, case per SES docs) to the stable event vocabulary. Anything
// unrecognized — including a missing value — is "undetermined", so
// email.bounced's required bounce_type is always one of the three enums.
func normalizeBounceType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "permanent":
		return "permanent"
	case "transient":
		return "transient"
	default:
		return "undetermined"
	}
}
