package messagelifecycle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tokencanopy/e2a/internal/emailauth"
)

const (
	maxDiagnosticStringBytes = 2 * 1024
	maxEvidenceBytes         = 16 * 1024
)

var allowedEvidenceKeys = map[string]bool{
	"authentication":     true,
	"smtp_detail":        true,
	"bounce_type":        true,
	"bounce_sub_type":    true,
	"failure_reason":     true,
	"failure_code":       true,
	"review_resolution":  true,
	"suppression_scope":  true,
	"suppression_source": true,
	"source":             true,
}

var allowedCorrelationKeys = map[string]bool{
	"event_id":            true,
	"job_id":              true,
	"provider_message_id": true,
	"provider_event_id":   true,
	"email_message_id":    true,
}

// MessageLifecycleTransition is one validated canonical lifecycle observation.
type MessageLifecycleTransition struct {
	ID             string            `json:"id"`
	MessageID      string            `json:"message_id"`
	Direction      string            `json:"direction" enum:"inbound,outbound"`
	Recipient      string            `json:"recipient,omitempty"`
	Stage          Stage             `json:"stage" enum:"accepted,authentication,review,suppression,queued,submission,delivery,complaint"`
	Outcome        Outcome           `json:"outcome" enum:"accepted,passed,failed,indeterminate,pending,approved,rejected,blocked,applied,enqueued,deferred,delivered,bounced,reported"`
	ReasonCode     ReasonCode        `json:"reason_code" enum:"acceptance.inbound_smtp,acceptance.outbound_api,acceptance.local_loopback,authentication.dmarc_pass,authentication.dmarc_fail,authentication.dmarc_none,authentication.dmarc_temporary_error,authentication.dmarc_permanent_error,review.hold_created,review.approved,review.rejected,review.expired_approved,review.expired_rejected,suppression.recipient_blocked,suppression.hard_bounce_applied,suppression.complaint_applied,queue.inbound_processing,queue.outbound_submission,submission.upstream_accepted,submission.local_loopback_accepted,submission.temporary_failure,submission.provider_rejected,submission.local_retries_exhausted,submission.cancelled,delivery.recipient_server_accepted,delivery.temporary_delay,delivery.permanent_bounce,delivery.transient_bounce,delivery.undetermined_bounce,complaint.recipient_reported"`
	Retryable      bool              `json:"retryable"`
	Evidence       map[string]any    `json:"evidence"`
	CorrelationIDs map[string]string `json:"correlation_ids"`
	OccurredAt     time.Time         `json:"occurred_at"`
	Reconstructed  bool              `json:"reconstructed"`
}

// AppendInput contains the producer-controlled fields used to construct a
// transition. DedupeKey is validated here and retained by the persistence
// operation introduced with the lifecycle store.
type AppendInput struct {
	MessageID      string
	DedupeKey      string
	Direction      string
	Recipient      string
	ReasonCode     ReasonCode
	Evidence       map[string]any
	CorrelationIDs map[string]string
	OccurredAt     time.Time
}

// NewTransition validates input and derives the semantic tuple from the closed
// reason catalog. The returned unpersisted transition has an empty ID; the
// lifecycle store assigns its stable mlt_ identifier when appending it.
func NewTransition(input AppendInput) (MessageLifecycleTransition, error) {
	if strings.TrimSpace(input.MessageID) == "" {
		return MessageLifecycleTransition{}, fmt.Errorf("message ID is required")
	}
	if strings.TrimSpace(input.DedupeKey) == "" {
		return MessageLifecycleTransition{}, fmt.Errorf("dedupe key is required")
	}
	if input.Direction != "inbound" && input.Direction != "outbound" {
		return MessageLifecycleTransition{}, fmt.Errorf("invalid direction %q", input.Direction)
	}
	definition, ok := Lookup(input.ReasonCode)
	if !ok {
		return MessageLifecycleTransition{}, fmt.Errorf("unknown reason code %q", input.ReasonCode)
	}
	if input.OccurredAt.IsZero() {
		return MessageLifecycleTransition{}, fmt.Errorf("occurred at is required")
	}
	if err := validateRecipient(input.ReasonCode, input.Recipient); err != nil {
		return MessageLifecycleTransition{}, err
	}

	evidence, err := validatedEvidenceCopy(input.Evidence)
	if err != nil {
		return MessageLifecycleTransition{}, err
	}
	correlationIDs, err := validatedCorrelationCopy(input.CorrelationIDs)
	if err != nil {
		return MessageLifecycleTransition{}, err
	}

	return MessageLifecycleTransition{
		MessageID:      input.MessageID,
		Direction:      input.Direction,
		Recipient:      input.Recipient,
		Stage:          definition.Stage,
		Outcome:        definition.Outcome,
		ReasonCode:     input.ReasonCode,
		Retryable:      definition.Retryable,
		Evidence:       evidence,
		CorrelationIDs: correlationIDs,
		OccurredAt:     input.OccurredAt.UTC(),
		Reconstructed:  false,
	}, nil
}

func validateRecipient(reason ReasonCode, recipient string) error {
	if recipientRequired(reason) {
		if strings.TrimSpace(recipient) == "" {
			return fmt.Errorf("recipient is required for reason %q", reason)
		}
		return nil
	}
	if recipient != "" {
		return fmt.Errorf("recipient is not allowed for reason %q", reason)
	}
	return nil
}

func recipientRequired(reason ReasonCode) bool {
	switch reason {
	case ReasonSuppressionRecipientBlocked,
		ReasonSuppressionHardBounceApplied,
		ReasonSuppressionComplaintApplied,
		ReasonDeliveryRecipientServerAccepted,
		ReasonDeliveryTemporaryDelay,
		ReasonDeliveryPermanentBounce,
		ReasonDeliveryTransientBounce,
		ReasonDeliveryUndeterminedBounce,
		ReasonComplaintRecipientReported:
		return true
	default:
		return false
	}
}

func validatedEvidenceCopy(evidence map[string]any) (map[string]any, error) {
	if evidence == nil {
		return map[string]any{}, nil
	}
	for key, value := range evidence {
		if !allowedEvidenceKeys[key] {
			return nil, fmt.Errorf("evidence key %q is not allowed", key)
		}
		if key == "authentication" {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("evidence %q must be a string", key)
		}
		if len(text) > maxDiagnosticStringBytes {
			return nil, fmt.Errorf("evidence %q exceeds 2 KiB", key)
		}
	}

	encoded, err := json.Marshal(evidence)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence: %w", err)
	}
	if len(encoded) > maxEvidenceBytes {
		return nil, fmt.Errorf("serialized evidence exceeds 16 KiB")
	}

	var copied map[string]any
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return nil, fmt.Errorf("copy evidence: %w", err)
	}
	if authentication, ok := copied["authentication"]; ok {
		encodedAuthentication, err := json.Marshal(authentication)
		if err != nil {
			return nil, fmt.Errorf("marshal evidence authentication: %w", err)
		}
		if err := validateAuthentication(encodedAuthentication); err != nil {
			return nil, fmt.Errorf("evidence authentication: %w", err)
		}
		if err := validateNestedStrings(authentication); err != nil {
			return nil, fmt.Errorf("evidence authentication: %w", err)
		}
	}
	return copied, nil
}

func validateAuthentication(encoded []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil || root == nil {
		return fmt.Errorf("must be a structured object")
	}
	allowedRoot := map[string]bool{"spf": true, "dkim": true, "dmarc": true}
	for key := range root {
		if !allowedRoot[key] {
			return fmt.Errorf("key %q is not allowed", key)
		}
	}
	for _, key := range []string{"spf", "dkim", "dmarc"} {
		if _, ok := root[key]; !ok {
			return fmt.Errorf("key %q is required", key)
		}
	}

	if err := validateAuthenticationObject(root["spf"], "spf", map[string]bool{
		"status": true, "domain": true, "aligned": true, "detail": true,
	}); err != nil {
		return err
	}

	var dkimItems []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(root["dkim"]), []byte("null")) {
		return fmt.Errorf("dkim must be an array")
	}
	if err := json.Unmarshal(root["dkim"], &dkimItems); err != nil {
		return fmt.Errorf("dkim must be an array")
	}
	for index, item := range dkimItems {
		if err := validateAuthenticationObject(item, fmt.Sprintf("dkim[%d]", index), map[string]bool{
			"status": true, "domain": true, "selector": true, "aligned": true, "detail": true,
		}); err != nil {
			return err
		}
	}

	if err := validateAuthenticationObject(root["dmarc"], "dmarc", map[string]bool{
		"status": true, "domain": true, "policy": true, "aligned_by": true, "detail": true,
	}); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var authentication emailauth.Authentication
	if err := decoder.Decode(&authentication); err != nil {
		return fmt.Errorf("invalid structure: %w", err)
	}
	return nil
}

func validateAuthenticationObject(encoded json.RawMessage, name string, allowed map[string]bool) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil || object == nil {
		return fmt.Errorf("%s must be an object", name)
	}
	for key := range object {
		if !allowed[key] {
			return fmt.Errorf("%s key %q is not allowed", name, key)
		}
	}
	return nil
}

func validateNestedStrings(value any) error {
	switch value := value.(type) {
	case string:
		if len(value) > maxDiagnosticStringBytes {
			return fmt.Errorf("string exceeds 2 KiB")
		}
	case []any:
		for _, item := range value {
			if err := validateNestedStrings(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range value {
			if err := validateNestedStrings(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatedCorrelationCopy(correlationIDs map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(correlationIDs))
	for key, value := range correlationIDs {
		if !allowedCorrelationKeys[key] {
			return nil, fmt.Errorf("correlation key %q is not allowed", key)
		}
		if len(value) > maxDiagnosticStringBytes {
			return nil, fmt.Errorf("correlation value %q exceeds 2 KiB", key)
		}
		if value == "" {
			continue
		}
		result[key] = value
	}
	return result, nil
}
