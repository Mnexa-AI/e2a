package messagelifecycle

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogIsExhaustive(t *testing.T) {
	tests := []struct {
		reason    ReasonCode
		stage     Stage
		outcome   Outcome
		retryable bool
	}{
		{ReasonAcceptanceInboundSMTP, StageAccepted, OutcomeAccepted, false},
		{ReasonAcceptanceOutboundAPI, StageAccepted, OutcomeAccepted, false},
		{ReasonAcceptanceLocalLoopback, StageAccepted, OutcomeAccepted, false},
		{ReasonAuthenticationDMARCPass, StageAuthentication, OutcomePassed, false},
		{ReasonAuthenticationDMARCFail, StageAuthentication, OutcomeFailed, false},
		{ReasonAuthenticationDMARCNone, StageAuthentication, OutcomeIndeterminate, false},
		{ReasonAuthenticationDMARCTemporaryError, StageAuthentication, OutcomeIndeterminate, true},
		{ReasonAuthenticationDMARCPermanentError, StageAuthentication, OutcomeIndeterminate, false},
		{ReasonReviewHoldCreated, StageReview, OutcomePending, false},
		{ReasonReviewApproved, StageReview, OutcomeApproved, false},
		{ReasonReviewRejected, StageReview, OutcomeRejected, false},
		{ReasonReviewExpiredApproved, StageReview, OutcomeApproved, false},
		{ReasonReviewExpiredRejected, StageReview, OutcomeRejected, false},
		{ReasonSuppressionRecipientBlocked, StageSuppression, OutcomeBlocked, false},
		{ReasonSuppressionHardBounceApplied, StageSuppression, OutcomeApplied, false},
		{ReasonSuppressionComplaintApplied, StageSuppression, OutcomeApplied, false},
		{ReasonQueueInboundProcessing, StageQueued, OutcomeEnqueued, false},
		{ReasonQueueOutboundSubmission, StageQueued, OutcomeEnqueued, false},
		{ReasonSubmissionUpstreamAccepted, StageSubmission, OutcomeAccepted, false},
		{ReasonSubmissionLocalLoopbackAccepted, StageSubmission, OutcomeAccepted, false},
		{ReasonSubmissionTemporaryFailure, StageSubmission, OutcomeDeferred, true},
		{ReasonSubmissionProviderRejected, StageSubmission, OutcomeFailed, false},
		{ReasonSubmissionLocalRetriesExhausted, StageSubmission, OutcomeFailed, true},
		{ReasonSubmissionCancelled, StageSubmission, OutcomeFailed, false},
		{ReasonDeliveryRecipientServerAccepted, StageDelivery, OutcomeDelivered, false},
		{ReasonDeliveryTemporaryDelay, StageDelivery, OutcomeDeferred, true},
		{ReasonDeliveryPermanentBounce, StageDelivery, OutcomeBounced, false},
		{ReasonDeliveryTransientBounce, StageDelivery, OutcomeBounced, true},
		{ReasonDeliveryUndeterminedBounce, StageDelivery, OutcomeBounced, false},
		{ReasonComplaintRecipientReported, StageComplaint, OutcomeReported, false},
	}

	catalog := Catalog()
	if got, want := len(catalog), 30; got != want {
		t.Fatalf("Catalog() length = %d, want %d", got, want)
	}
	seen := make(map[ReasonCode]bool, len(tests))
	for _, tt := range tests {
		if seen[tt.reason] {
			t.Fatalf("reason %q appears more than once in test catalog", tt.reason)
		}
		seen[tt.reason] = true
		got, ok := Lookup(tt.reason)
		if !ok {
			t.Errorf("Lookup(%q) was not found", tt.reason)
			continue
		}
		want := Definition{Stage: tt.stage, Outcome: tt.outcome, Retryable: tt.retryable}
		if got != want {
			t.Errorf("Lookup(%q) = %+v, want %+v", tt.reason, got, want)
		}
		if catalog[tt.reason] != want {
			t.Errorf("Catalog()[%q] = %+v, want %+v", tt.reason, catalog[tt.reason], want)
		}
	}
	for reason := range catalog {
		if !seen[reason] {
			t.Errorf("Catalog() contains unexpected reason %q", reason)
		}
	}
}

func TestCatalogRejectsUnknownAndCannotBeMutated(t *testing.T) {
	if _, ok := Lookup(ReasonCode("provider.free_form")); ok {
		t.Fatal("Lookup accepted an unknown provider reason")
	}

	copyOne := Catalog()
	copyOne[ReasonAcceptanceInboundSMTP] = Definition{Stage: StageComplaint, Outcome: OutcomeReported, Retryable: true}
	delete(copyOne, ReasonAcceptanceOutboundAPI)

	got, ok := Lookup(ReasonAcceptanceInboundSMTP)
	if !ok || got != (Definition{Stage: StageAccepted, Outcome: OutcomeAccepted}) {
		t.Fatalf("caller mutation changed canonical lookup: %+v, %v", got, ok)
	}
	if got := len(Catalog()); got != 30 {
		t.Fatalf("caller mutation changed canonical catalog length to %d", got)
	}
}

func TestCatalogProducerMappings(t *testing.T) {
	authTests := []struct {
		status string
		want   ReasonCode
	}{
		{"pass", ReasonAuthenticationDMARCPass},
		{"fail", ReasonAuthenticationDMARCFail},
		{"none", ReasonAuthenticationDMARCNone},
		{"temperror", ReasonAuthenticationDMARCTemporaryError},
		{"permerror", ReasonAuthenticationDMARCPermanentError},
	}
	for _, tt := range authTests {
		got, err := AuthenticationReason(tt.status)
		if err != nil || got != tt.want {
			t.Errorf("AuthenticationReason(%q) = %q, %v; want %q, nil", tt.status, got, err, tt.want)
		}
	}
	if _, err := AuthenticationReason("unknown"); err == nil {
		t.Fatal("AuthenticationReason accepted an unknown DMARC status")
	}

	bounceTests := []struct {
		bounceType string
		want       ReasonCode
	}{
		{"permanent", ReasonDeliveryPermanentBounce},
		{"transient", ReasonDeliveryTransientBounce},
		{"undetermined", ReasonDeliveryUndeterminedBounce},
		{"provider-new-value", ReasonDeliveryUndeterminedBounce},
	}
	for _, tt := range bounceTests {
		if got := BounceReason(tt.bounceType); got != tt.want {
			t.Errorf("BounceReason(%q) = %q, want %q", tt.bounceType, got, tt.want)
		}
	}
}

func TestNewTransitionDerivesCanonicalFieldsAndCopiesInput(t *testing.T) {
	when := time.Date(2026, 7, 21, 12, 30, 0, 123, time.FixedZone("test", -7*60*60))
	authentication := map[string]any{
		"dmarc": map[string]any{"status": "pass"},
		"dkim":  []any{map[string]any{"domain": "example.com"}},
	}
	evidence := map[string]any{"authentication": authentication, "source": "smtp"}
	correlations := map[string]string{"event_id": "evt_123", "email_message_id": "<mail@example.com>"}

	got, err := NewTransition(AppendInput{
		MessageID:      "msg_123",
		DedupeKey:      "acceptance",
		Direction:      "inbound",
		ReasonCode:     ReasonAuthenticationDMARCPass,
		Evidence:       evidence,
		CorrelationIDs: correlations,
		OccurredAt:     when,
	})
	if err != nil {
		t.Fatalf("NewTransition() error = %v", err)
	}
	if got.ID != "" {
		t.Errorf("unpersisted transition ID = %q, want empty", got.ID)
	}
	if got.Stage != StageAuthentication || got.Outcome != OutcomePassed || got.Retryable {
		t.Errorf("derived tuple = %q/%q/%v", got.Stage, got.Outcome, got.Retryable)
	}
	if got.MessageID != "msg_123" || got.Direction != "inbound" || got.ReasonCode != ReasonAuthenticationDMARCPass {
		t.Errorf("transition identity fields = %+v", got)
	}
	if got.Reconstructed {
		t.Error("new transition was marked reconstructed")
	}
	if got.OccurredAt.Location() != time.UTC || !got.OccurredAt.Equal(when) {
		t.Errorf("occurred_at = %v (%v), want same instant in UTC", got.OccurredAt, got.OccurredAt.Location())
	}

	authentication["dmarc"].(map[string]any)["status"] = "fail"
	evidence["source"] = "mutated"
	correlations["event_id"] = "evt_mutated"
	gotAuth := got.Evidence["authentication"].(map[string]any)
	if gotAuth["dmarc"].(map[string]any)["status"] != "pass" || got.Evidence["source"] != "smtp" {
		t.Fatalf("evidence was not deep-copied: %#v", got.Evidence)
	}
	if got.CorrelationIDs["event_id"] != "evt_123" {
		t.Fatalf("correlation IDs were not copied: %#v", got.CorrelationIDs)
	}
}

func TestNewTransitionNormalizesNilMaps(t *testing.T) {
	got, err := NewTransition(validAppendInput())
	if err != nil {
		t.Fatalf("NewTransition() error = %v", err)
	}
	if got.Evidence == nil || got.CorrelationIDs == nil {
		t.Fatalf("nil maps were not normalized: evidence=%#v correlations=%#v", got.Evidence, got.CorrelationIDs)
	}
}

func TestNewTransitionRejectsInvalidRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AppendInput)
	}{
		{"missing message ID", func(in *AppendInput) { in.MessageID = "" }},
		{"blank message ID", func(in *AppendInput) { in.MessageID = "  " }},
		{"missing dedupe key", func(in *AppendInput) { in.DedupeKey = "" }},
		{"blank dedupe key", func(in *AppendInput) { in.DedupeKey = "\t" }},
		{"unknown direction", func(in *AppendInput) { in.Direction = "sideways" }},
		{"missing direction", func(in *AppendInput) { in.Direction = "" }},
		{"unknown reason", func(in *AppendInput) { in.ReasonCode = "provider.free_form" }},
		{"missing reason", func(in *AppendInput) { in.ReasonCode = "" }},
		{"zero timestamp", func(in *AppendInput) { in.OccurredAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validAppendInput()
			tt.mutate(&in)
			if _, err := NewTransition(in); err == nil {
				t.Fatal("NewTransition() succeeded, want validation error")
			}
		})
	}
}

func TestNewTransitionEnforcesRecipientRules(t *testing.T) {
	required := []ReasonCode{
		ReasonSuppressionRecipientBlocked,
		ReasonSuppressionHardBounceApplied,
		ReasonSuppressionComplaintApplied,
		ReasonDeliveryRecipientServerAccepted,
		ReasonDeliveryTemporaryDelay,
		ReasonDeliveryPermanentBounce,
		ReasonDeliveryTransientBounce,
		ReasonDeliveryUndeterminedBounce,
		ReasonComplaintRecipientReported,
	}
	for _, reason := range required {
		t.Run(string(reason)+" requires recipient", func(t *testing.T) {
			in := validAppendInput()
			in.ReasonCode = reason
			if _, err := NewTransition(in); err == nil {
				t.Fatal("NewTransition() accepted missing recipient")
			}
			in.Recipient = "person@example.com"
			if _, err := NewTransition(in); err != nil {
				t.Fatalf("NewTransition() rejected recipient: %v", err)
			}
		})
	}

	for _, reason := range []ReasonCode{ReasonAcceptanceInboundSMTP, ReasonReviewHoldCreated, ReasonQueueOutboundSubmission, ReasonSubmissionUpstreamAccepted} {
		t.Run(string(reason)+" forbids recipient", func(t *testing.T) {
			in := validAppendInput()
			in.ReasonCode = reason
			in.Recipient = "person@example.com"
			if _, err := NewTransition(in); err == nil {
				t.Fatal("NewTransition() accepted forbidden recipient")
			}
		})
	}
}

func TestEvidenceRejectsForbiddenUnknownAndInvalidValues(t *testing.T) {
	for _, key := range []string{"body", "raw_mime", "headers", "credentials", "secret", "webhook_secret", "provider_response", "unknown"} {
		t.Run(key, func(t *testing.T) {
			in := validAppendInput()
			in.Evidence = map[string]any{key: "unsafe"}
			if _, err := NewTransition(in); err == nil {
				t.Fatal("NewTransition() accepted forbidden or unknown evidence key")
			}
		})
	}

	for name, evidence := range map[string]map[string]any{
		"non-string diagnostic":  {"smtp_detail": true},
		"unsupported JSON value": {"authentication": map[string]any{"bad": func() {}}},
	} {
		t.Run(name, func(t *testing.T) {
			in := validAppendInput()
			in.Evidence = evidence
			if _, err := NewTransition(in); err == nil {
				t.Fatal("NewTransition() accepted invalid evidence value")
			}
		})
	}
}

func TestEvidenceEnforcesStringAndSerializedLimits(t *testing.T) {
	tooLong := strings.Repeat("x", 2*1024+1)
	for name, evidence := range map[string]map[string]any{
		"top-level string":             {"smtp_detail": tooLong},
		"nested authentication string": {"authentication": map[string]any{"dmarc": map[string]any{"detail": tooLong}}},
	} {
		t.Run(name, func(t *testing.T) {
			in := validAppendInput()
			in.Evidence = evidence
			if _, err := NewTransition(in); err == nil {
				t.Fatal("NewTransition() accepted string over 2 KiB")
			}
		})
	}

	large := strings.Repeat("x", 2*1024)
	in := validAppendInput()
	in.Evidence = map[string]any{
		"smtp_detail": large, "bounce_type": large, "bounce_sub_type": large,
		"failure_reason": large, "failure_code": large, "review_resolution": large,
		"suppression_scope": large, "suppression_source": large, "source": large,
	}
	if _, err := NewTransition(in); err == nil {
		t.Fatal("NewTransition() accepted serialized evidence over 16 KiB")
	}
}

func TestEvidenceAcceptsBoundedStructuredAuthentication(t *testing.T) {
	in := validAppendInput()
	in.Evidence = map[string]any{
		"authentication": map[string]any{
			"spf":   map[string]any{"status": "pass", "domain": "example.com"},
			"dkim":  []any{map[string]any{"status": "pass", "selector": "s1"}},
			"dmarc": map[string]any{"status": "pass"},
		},
		"smtp_detail": "250 2.0.0 accepted",
	}
	if _, err := NewTransition(in); err != nil {
		t.Fatalf("NewTransition() rejected safe evidence: %v", err)
	}
}

func TestEvidenceValidatesCorrelationIDs(t *testing.T) {
	allowed := []string{"event_id", "job_id", "provider_message_id", "provider_event_id", "email_message_id"}
	for _, key := range allowed {
		in := validAppendInput()
		in.CorrelationIDs = map[string]string{key: "value"}
		if _, err := NewTransition(in); err != nil {
			t.Errorf("NewTransition() rejected correlation key %q: %v", key, err)
		}
	}

	in := validAppendInput()
	in.CorrelationIDs = map[string]string{"request_id": "value"}
	if _, err := NewTransition(in); err == nil {
		t.Fatal("NewTransition() accepted unknown correlation key")
	}
	in = validAppendInput()
	in.CorrelationIDs = map[string]string{"event_id": strings.Repeat("x", 2*1024+1)}
	if _, err := NewTransition(in); err == nil {
		t.Fatal("NewTransition() accepted correlation value over 2 KiB")
	}
}

func TestNewTransitionSchemaEnumTags(t *testing.T) {
	typ := reflect.TypeOf(MessageLifecycleTransition{})
	assertTag := func(field, key, want string) {
		t.Helper()
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("field %s not found", field)
		}
		if got := f.Tag.Get(key); got != want {
			t.Errorf("%s %s tag = %q, want %q", field, key, got, want)
		}
	}
	assertTag("Direction", "enum", "inbound,outbound")
	assertTag("Stage", "enum", "accepted,authentication,review,suppression,queued,submission,delivery,complaint")
	assertTag("Outcome", "enum", "accepted,passed,failed,indeterminate,pending,approved,rejected,blocked,applied,enqueued,deferred,delivered,bounced,reported")
	assertTag("ReasonCode", "enum", "acceptance.inbound_smtp,acceptance.outbound_api,acceptance.local_loopback,authentication.dmarc_pass,authentication.dmarc_fail,authentication.dmarc_none,authentication.dmarc_temporary_error,authentication.dmarc_permanent_error,review.hold_created,review.approved,review.rejected,review.expired_approved,review.expired_rejected,suppression.recipient_blocked,suppression.hard_bounce_applied,suppression.complaint_applied,queue.inbound_processing,queue.outbound_submission,submission.upstream_accepted,submission.local_loopback_accepted,submission.temporary_failure,submission.provider_rejected,submission.local_retries_exhausted,submission.cancelled,delivery.recipient_server_accepted,delivery.temporary_delay,delivery.permanent_bounce,delivery.transient_bounce,delivery.undetermined_bounce,complaint.recipient_reported")
}

func validAppendInput() AppendInput {
	return AppendInput{
		MessageID:  "msg_123",
		DedupeKey:  "acceptance",
		Direction:  "outbound",
		ReasonCode: ReasonAcceptanceOutboundAPI,
		OccurredAt: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
	}
}
