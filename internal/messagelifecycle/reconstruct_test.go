package messagelifecycle

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var reconstructBaseTime = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

func TestReconstructAcceptanceByDirectionAndMethod(t *testing.T) {
	tests := []struct {
		name, direction, method string
		want                    ReasonCode
	}{
		{"outbound API", "outbound", "smtp", ReasonAcceptanceOutboundAPI},
		{"inbound SMTP", "inbound", "smtp", ReasonAcceptanceInboundSMTP},
		{"inbound loopback", "inbound", "loopback", ReasonAcceptanceLocalLoopback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Reconstruct(baseSnapshot(tt.direction, tt.method))
			assertReasons(t, got, tt.want)
			if !got[0].OccurredAt.Equal(reconstructBaseTime) {
				t.Fatalf("acceptance occurred_at = %v", got[0].OccurredAt)
			}
		})
	}
}

func TestReconstructAuthenticationMappingsAndMalformedOmission(t *testing.T) {
	for status, want := range map[string]ReasonCode{
		"pass": ReasonAuthenticationDMARCPass, "fail": ReasonAuthenticationDMARCFail,
		"none": ReasonAuthenticationDMARCNone, "temperror": ReasonAuthenticationDMARCTemporaryError,
		"permerror": ReasonAuthenticationDMARCPermanentError,
	} {
		t.Run(status, func(t *testing.T) {
			snapshot := baseSnapshot("inbound", "smtp")
			snapshot.Authentication = authenticationJSON(status)
			got := Reconstruct(snapshot)
			assertReasons(t, got, ReasonAcceptanceInboundSMTP, want)
			if findReason(got, want).Evidence["authentication"] == nil {
				t.Fatal("authentication evidence missing")
			}
		})
	}
	for name, raw := range map[string]json.RawMessage{
		"malformed": []byte(`{"dmarc":`),
		"unknown":   authenticationJSON("future"),
		"outbound":  authenticationJSON("pass"),
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := baseSnapshot("inbound", "smtp")
			if name == "outbound" {
				snapshot.Direction = "outbound"
			}
			snapshot.Authentication = raw
			got := Reconstruct(snapshot)
			if hasStage(got, StageAuthentication) {
				t.Fatalf("unexpected authentication: %#v", got)
			}
		})
	}
}

func TestReconstructReviewRules(t *testing.T) {
	reviewed := reconstructBaseTime.Add(time.Minute)
	tests := []struct {
		status   string
		reviewed *time.Time
		want     []ReasonCode
	}{
		{"pending_review", nil, []ReasonCode{ReasonAcceptanceOutboundAPI, ReasonReviewHoldCreated}},
		{"sent", &reviewed, []ReasonCode{ReasonAcceptanceOutboundAPI, ReasonReviewApproved}},
		{"review_approved", &reviewed, []ReasonCode{ReasonAcceptanceOutboundAPI, ReasonReviewApproved}},
		{"review_rejected", &reviewed, []ReasonCode{ReasonAcceptanceOutboundAPI, ReasonReviewRejected}},
		{"review_expired_approved", &reviewed, []ReasonCode{ReasonAcceptanceOutboundAPI, ReasonReviewExpiredApproved}},
		{"review_expired_rejected", &reviewed, []ReasonCode{ReasonAcceptanceOutboundAPI, ReasonReviewExpiredRejected}},
		{"review_rejected", nil, []ReasonCode{ReasonAcceptanceOutboundAPI}},
	}
	for _, tt := range tests {
		t.Run(tt.status+boolName(tt.reviewed != nil), func(t *testing.T) {
			snapshot := baseSnapshot("outbound", "smtp")
			snapshot.Status, snapshot.ReviewedAt = tt.status, tt.reviewed
			assertReasons(t, Reconstruct(snapshot), tt.want...)
		})
	}
}

func TestReconstructQueueTimestampsAndDirection(t *testing.T) {
	jobID := int64(42)
	jobAt := reconstructBaseTime.Add(3 * time.Minute)
	reviewed := reconstructBaseTime.Add(2 * time.Minute)
	tests := []struct {
		name      string
		direction string
		status    string
		reviewed  *time.Time
		jobAt     *time.Time
		wantAt    *time.Time
	}{
		{"river timestamp", "outbound", "sent", &reviewed, &jobAt, &jobAt},
		{"approved fallback", "outbound", "sent", &reviewed, nil, &reviewed},
		{"created fallback", "outbound", "accepted", nil, nil, &reconstructBaseTime},
		{"no inbound inference", "inbound", "", nil, &jobAt, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := baseSnapshot(tt.direction, "smtp")
			s.Status, s.ReviewedAt, s.SendJobID, s.JobCreatedAt = tt.status, tt.reviewed, &jobID, tt.jobAt
			got := Reconstruct(s)
			queue := findReason(got, ReasonQueueOutboundSubmission)
			if tt.wantAt == nil {
				if queue != nil {
					t.Fatalf("invented queue: %#v", *queue)
				}
				return
			}
			if queue == nil || !queue.OccurredAt.Equal(*tt.wantAt) || queue.CorrelationIDs["job_id"] != "42" {
				t.Fatalf("queue = %#v, want at %v with job 42", queue, *tt.wantAt)
			}
		})
	}
}

func TestReconstructSubmissionEvidence(t *testing.T) {
	providerAt := reconstructBaseTime.Add(time.Minute)
	s := baseSnapshot("outbound", "smtp")
	s.ProviderAcceptedAt, s.ProviderMessageID = &providerAt, "provider-1"
	got := Reconstruct(s)
	transition := findReason(got, ReasonSubmissionUpstreamAccepted)
	if transition == nil || transition.CorrelationIDs["provider_message_id"] != "provider-1" {
		t.Fatalf("provider submission = %#v", transition)
	}

	loopback := baseSnapshot("outbound", "loopback")
	loopback.DeliveryStatus = "sent"
	assertReasons(t, Reconstruct(loopback), ReasonAcceptanceOutboundAPI, ReasonSubmissionLocalLoopbackAccepted)

	reviewed := reconstructBaseTime.Add(time.Minute)
	loopback.ReviewedAt = &reviewed
	if got := findReason(Reconstruct(loopback), ReasonSubmissionLocalLoopbackAccepted); got == nil || !got.OccurredAt.Equal(reviewed) {
		t.Fatalf("reviewed loopback = %#v", got)
	}

	sentOnly := baseSnapshot("outbound", "smtp")
	sentOnly.DeliveryStatus = "sent"
	if hasStage(Reconstruct(sentOnly), StageSubmission) {
		t.Fatal("sent-only SMTP state fabricated submission")
	}
}

func TestReconstructRecipientMappingsAndIgnoredStatuses(t *testing.T) {
	wants := map[string]ReasonCode{
		"delivered":  ReasonDeliveryRecipientServerAccepted,
		"deferred":   ReasonDeliveryTemporaryDelay,
		"bounced":    ReasonDeliveryUndeterminedBounce,
		"complained": ReasonComplaintRecipientReported,
	}
	for status, want := range wants {
		t.Run(status, func(t *testing.T) {
			s := baseSnapshot("outbound", "smtp")
			s.Recipients = []RecipientSnapshot{{ID: "rcp_1", Address: "a@example.com", Status: status, Detail: "detail", UpdatedAt: reconstructBaseTime.Add(time.Minute)}}
			got := findReason(Reconstruct(s), want)
			if got == nil || got.Recipient != "a@example.com" || got.Evidence["smtp_detail"] != "detail" {
				t.Fatalf("recipient transition = %#v", got)
			}
		})
	}
	for _, status := range []string{"queued", "accepted", "sending", "sent", "failed"} {
		t.Run("ignore "+status, func(t *testing.T) {
			s := baseSnapshot("outbound", "smtp")
			s.Recipients = []RecipientSnapshot{{ID: "rcp_1", Address: "a@example.com", Status: status, UpdatedAt: reconstructBaseTime}}
			if len(Reconstruct(s)) != 1 {
				t.Fatalf("status %q proved an outcome", status)
			}
		})
	}
}

func TestReconstructCausalSuppressionsOnly(t *testing.T) {
	s := baseSnapshot("outbound", "smtp")
	s.Suppressions = []SuppressionSnapshot{
		{ID: "sup_1", Address: "bounce@example.com", Source: "bounce", SourceMessageID: s.MessageID, CreatedAt: reconstructBaseTime.Add(time.Minute)},
		{ID: "sup_2", Address: "complaint@example.com", Source: "complaint", SourceMessageID: s.MessageID, CreatedAt: reconstructBaseTime.Add(2 * time.Minute)},
		{ID: "sup_3", Address: "manual@example.com", Source: "manual", SourceMessageID: s.MessageID, CreatedAt: reconstructBaseTime},
		{ID: "sup_4", Address: "foreign@example.com", Source: "bounce", SourceMessageID: "msg_other", CreatedAt: reconstructBaseTime},
	}
	got := Reconstruct(s)
	if findReason(got, ReasonSuppressionHardBounceApplied) == nil || findReason(got, ReasonSuppressionComplaintApplied) == nil || len(got) != 3 {
		t.Fatalf("suppressions = %#v", got)
	}
}

func TestReconstructMappedRetainedEvents(t *testing.T) {
	tests := []struct {
		typeName string
		data     map[string]any
		want     ReasonCode
	}{
		{"email.received", map[string]any{"message_id": "msg_1"}, ReasonAcceptanceInboundSMTP},
		{"email.sent", map[string]any{"message_id": "msg_1", "method": "smtp", "provider_message_id": "p1"}, ReasonSubmissionUpstreamAccepted},
		{"email.delivered", map[string]any{"message_id": "msg_1", "delivered_to": "a@example.com", "smtp_detail": "250 ok"}, ReasonDeliveryRecipientServerAccepted},
		{"email.bounced", map[string]any{"message_id": "msg_1", "delivered_to": "a@example.com", "bounce_type": "transient", "bounce_sub_type": "MailboxFull"}, ReasonDeliveryTransientBounce},
		{"email.complained", map[string]any{"message_id": "msg_1", "delivered_to": "a@example.com"}, ReasonComplaintRecipientReported},
		{"email.review_requested", map[string]any{"message_id": "msg_1"}, ReasonReviewHoldCreated},
		{"email.review_approved", map[string]any{"message_id": "msg_1", "reason": "human"}, ReasonReviewApproved},
		{"email.review_rejected", map[string]any{"message_id": "msg_1", "reason": "human"}, ReasonReviewRejected},
		{"domain.suppression_added", map[string]any{"message_id": "msg_1", "address": "a@example.com", "source": "bounce"}, ReasonSuppressionHardBounceApplied},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			direction := "outbound"
			if tt.typeName == "email.received" {
				direction = "inbound"
			}
			s := baseSnapshot(direction, "smtp")
			s.Events = []EventSnapshot{eventSnapshot("evt_1", tt.typeName, reconstructBaseTime.Add(time.Minute), tt.data)}
			got := findReason(Reconstruct(s), tt.want)
			if got == nil || got.CorrelationIDs["event_id"] != "evt_1" || got.Evidence["source"] != "webhook_events.envelope" {
				t.Fatalf("event transition = %#v", got)
			}
		})
	}
}

func TestReconstructExcludesScreeningUnknownAndAmbiguousFailureEvents(t *testing.T) {
	for _, eventType := range []string{"email.flagged", "email.blocked", "future.event", "email.failed"} {
		t.Run(eventType, func(t *testing.T) {
			s := baseSnapshot("outbound", "smtp")
			s.Events = []EventSnapshot{eventSnapshot("evt_1", eventType, reconstructBaseTime.Add(time.Minute), map[string]any{"message_id": s.MessageID, "reason": "failed"})}
			if got := Reconstruct(s); len(got) != 1 {
				t.Fatalf("excluded event reconstructed: %#v", got)
			}
		})
	}
	for source, want := range map[string]ReasonCode{"provider": ReasonSubmissionProviderRejected, "local": ReasonSubmissionLocalRetriesExhausted} {
		t.Run(source, func(t *testing.T) {
			s := baseSnapshot("outbound", "smtp")
			s.DeliveryFailureSource = source
			s.Events = []EventSnapshot{eventSnapshot("evt_1", "email.failed", reconstructBaseTime.Add(time.Minute), map[string]any{"message_id": s.MessageID, "reason": "no route", "reason_code": "smtp_rejected"})}
			got := findReason(Reconstruct(s), want)
			if got == nil || got.Evidence["failure_reason"] != "no route" || got.Evidence["failure_code"] != "smtp_rejected" {
				t.Fatalf("failure = %#v", got)
			}
		})
	}
}

func TestReconstructEventPreferredOverState(t *testing.T) {
	s := baseSnapshot("outbound", "smtp")
	s.Recipients = []RecipientSnapshot{{ID: "rcp_1", Address: "a@example.com", Status: "delivered", UpdatedAt: reconstructBaseTime.Add(3 * time.Minute)}}
	eventAt := reconstructBaseTime.Add(time.Minute)
	s.Events = []EventSnapshot{eventSnapshot("evt_1", "email.delivered", eventAt, map[string]any{"message_id": s.MessageID, "delivered_to": "a@example.com", "smtp_detail": "event detail"})}
	got := findReason(Reconstruct(s), ReasonDeliveryRecipientServerAccepted)
	if got == nil || !got.OccurredAt.Equal(eventAt) || got.Evidence["smtp_detail"] != "event detail" {
		t.Fatalf("event did not replace state: %#v", got)
	}
}

func TestReconstructExpiredReviewEventUsesDurableResolutionAndEventTimestamp(t *testing.T) {
	reviewedAt := reconstructBaseTime.Add(3 * time.Minute)
	eventAt := reconstructBaseTime.Add(time.Minute)
	s := baseSnapshot("outbound", "smtp")
	s.Status = "review_expired_rejected"
	s.ReviewedAt = &reviewedAt
	s.Events = []EventSnapshot{eventSnapshot("evt_expired", "email.review_rejected", eventAt, map[string]any{
		"message_id": s.MessageID, "reason": "ttl_expired",
	})}
	got := Reconstruct(s)
	transition := findReason(got, ReasonReviewExpiredRejected)
	if transition == nil || !transition.OccurredAt.Equal(eventAt) || transition.CorrelationIDs["event_id"] != "evt_expired" {
		t.Fatalf("expired review did not use event observation: %#v", got)
	}
	if findReason(got, ReasonReviewRejected) != nil {
		t.Fatalf("generic rejection duplicated expired rejection: %#v", got)
	}
}

func TestReconstructRejectsRetainedEventWithMismatchedDirection(t *testing.T) {
	s := baseSnapshot("inbound", "smtp")
	s.Events = []EventSnapshot{eventSnapshot("evt_wrong", "email.sent", reconstructBaseTime.Add(time.Minute), map[string]any{
		"message_id": s.MessageID, "direction": "outbound", "method": "smtp",
	})}
	if got := Reconstruct(s); len(got) != 1 || hasStage(got, StageSubmission) {
		t.Fatalf("mismatched event reconstructed: %#v", got)
	}
}

func TestReconstructDeterministicIDsDeepCopiesNonNilMapsAndOrdersEqualTimes(t *testing.T) {
	s := baseSnapshot("outbound", "smtp")
	s.ProviderAcceptedAt = ptrTime(reconstructBaseTime)
	s.ProviderMessageID = "provider-1"
	first := Reconstruct(s)
	second := Reconstruct(s)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeat reconstruction differs\n%#v\n%#v", first, second)
	}
	for _, item := range first {
		if !strings.HasPrefix(item.ID, "mlt_recon_") || len(item.ID) != len("mlt_recon_")+32 || item.Evidence == nil || item.CorrelationIDs == nil || !item.Reconstructed {
			t.Fatalf("invalid reconstructed item: %#v", item)
		}
	}
	first[0].Evidence["source"] = "mutated"
	first[0].CorrelationIDs["event_id"] = "mutated"
	if reflect.DeepEqual(first, second) {
		t.Fatal("result maps alias across calls")
	}
	ids := []string{second[0].ID, second[1].ID}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("equal timestamp IDs not ordered: %v", ids)
	}
}

func TestMergeTransitionsPersistedWinsAndRetainsRepeatedPersistedObservations(t *testing.T) {
	s := baseSnapshot("outbound", "smtp")
	s.Recipients = []RecipientSnapshot{{ID: "rcp", Address: "a@example.com", Status: "delivered", UpdatedAt: reconstructBaseTime.Add(time.Minute)}}
	reconstructed := Reconstruct(s)
	persisted := []MessageLifecycleTransition{
		persistedTransition("mlt_a", ReasonDeliveryRecipientServerAccepted, "a@example.com", reconstructBaseTime.Add(2*time.Minute)),
		persistedTransition("mlt_b", ReasonDeliveryRecipientServerAccepted, "a@example.com", reconstructBaseTime.Add(3*time.Minute)),
	}
	got := MergeTransitions(persisted, reconstructed)
	if len(got) != 3 || got[0].ReasonCode != ReasonAcceptanceOutboundAPI || got[1].ID != "mlt_a" || got[2].ID != "mlt_b" {
		t.Fatalf("merged = %#v", got)
	}
}

func TestReconstructDoesNotInventIntermediateStages(t *testing.T) {
	s := baseSnapshot("outbound", "smtp")
	s.Recipients = []RecipientSnapshot{{ID: "rcp", Address: "a@example.com", Status: "delivered", UpdatedAt: reconstructBaseTime.Add(time.Minute)}}
	got := Reconstruct(s)
	assertReasons(t, got, ReasonAcceptanceOutboundAPI, ReasonDeliveryRecipientServerAccepted)
	if hasStage(got, StageQueued) || hasStage(got, StageSubmission) {
		t.Fatalf("invented intermediate transition: %#v", got)
	}
}

func baseSnapshot(direction, method string) Snapshot {
	return Snapshot{MessageID: "msg_1", AgentID: "agt_1", Direction: direction, Method: method, CreatedAt: reconstructBaseTime}
}

func authenticationJSON(status string) json.RawMessage {
	return json.RawMessage(`{"spf":{"status":"none","domain":null,"aligned":null},"dkim":[],"dmarc":{"status":"` + status + `","domain":null,"policy":null,"aligned_by":[]}}`)
}

func eventSnapshot(id, eventType string, at time.Time, data map[string]any) EventSnapshot {
	envelope, _ := json.Marshal(map[string]any{"type": eventType, "id": id, "created_at": at, "data": data})
	return EventSnapshot{ID: id, Type: eventType, Envelope: envelope, CreatedAt: at.Add(time.Hour)}
}

func persistedTransition(id string, reason ReasonCode, recipient string, at time.Time) MessageLifecycleTransition {
	definition, _ := Lookup(reason)
	return MessageLifecycleTransition{ID: id, MessageID: "msg_1", Direction: "outbound", Recipient: recipient, Stage: definition.Stage, Outcome: definition.Outcome, ReasonCode: reason, Retryable: definition.Retryable, Evidence: map[string]any{}, CorrelationIDs: map[string]string{}, OccurredAt: at}
}

func assertReasons(t *testing.T, got []MessageLifecycleTransition, want ...ReasonCode) {
	t.Helper()
	reasons := make([]ReasonCode, len(got))
	for i := range got {
		reasons[i] = got[i].ReasonCode
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(reasons, want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
}

func findReason(items []MessageLifecycleTransition, reason ReasonCode) *MessageLifecycleTransition {
	for i := range items {
		if items[i].ReasonCode == reason {
			return &items[i]
		}
	}
	return nil
}

func hasStage(items []MessageLifecycleTransition, stage Stage) bool {
	for _, item := range items {
		if item.Stage == stage {
			return true
		}
	}
	return false
}

func ptrTime(value time.Time) *time.Time { return &value }
func boolName(value bool) string {
	if value {
		return " with timestamp"
	}
	return " without timestamp"
}
