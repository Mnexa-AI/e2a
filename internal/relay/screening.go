package relay

import (
	"context"
	"encoding/json"
	"log"

	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/inboundpolicy"
	"github.com/Mnexa-AI/e2a/internal/piguard"
)

// inboundScreenResult is the outcome of content-screening one inbound message:
// the denormalized verdict for the message row, the audit rows to append, and the
// data the email.injection_detected event carries.
type inboundScreenResult struct {
	Denorm     identity.InboundScreening
	Events     []identity.ScreeningEvent
	Detected   bool // a scan violation fired → emit email.injection_detected
	Score      float64
	Action     string
	Categories []string
	Reason     string
}

// screenInbound runs the agent's content scan (when inbound_scan='on') and combines
// it with the already-computed ingestion-gate decision into the audit + denormalized
// verdict.
//
// Scope note (Slice 4 — detect + audit + annotate): this does NOT hold/quarantine.
// review/block enforcement (delivery suppression) lands in a later slice, so the
// message still delivers here; the computed action is recorded for the audit trail
// and the agent's runtime to act on. The ingestion gate's own flagged/flag_reason
// handling and the email.flagged event are unchanged in the caller.
func (s *Server) screenInbound(ctx context.Context, agent *identity.AgentIdentity, messageID, senderEmail string, body []byte, auth *emailauth.Result, gate inboundpolicy.Decision) inboundScreenResult {
	var res inboundScreenResult

	// Gate violation → audit row (source=gate). The gate's flagged/flag_reason +
	// email.flagged are handled by the caller; this only records the audit trail.
	if gate.Flagged {
		res.Events = append(res.Events, identity.ScreeningEvent{
			ID:          identity.DeterministicScreeningEventID(messageID, identity.ScreeningSourceGate, identity.ReviewReasonSenderGate, ""),
			MessageID:   messageID,
			AgentID:     agent.ID,
			Direction:   "inbound",
			Source:      identity.ScreeningSourceGate,
			Reason:      identity.ReviewReasonSenderGate,
			Action:      agent.InboundPolicyAction,
			SubjectAddr: senderEmail,
		})
	}

	if agent.InboundScan != identity.ScanOn {
		return res
	}

	segs, sig, _ := piguard.Extract(body, 0)
	agg := s.screen.Evaluate(ctx, piguard.Request{
		Direction: piguard.DirectionInput,
		Segments:  segs,
		Signals:   sig,
		Sender:    senderEmail,
		Auth:      auth,
		SizeBytes: len(body),
	})
	action := agg.Action(agent.InboundScanReviewThreshold, agent.InboundScanBlockThreshold)
	if action == piguard.ActionAllow && !agg.Flagged {
		return res // benign — nothing to record beyond any gate row
	}

	score := agg.Score
	cats := make([]string, 0, len(agg.Categories))
	for _, c := range agg.Categories {
		cats = append(cats, c.Name)
	}
	catsJSON, _ := json.Marshal(agg.Categories)

	res.Detected = true
	res.Score = score
	res.Action = string(action)
	res.Categories = cats
	res.Reason = scanReason(agg)
	res.Denorm = identity.InboundScreening{
		ReviewReason: identity.ReviewReasonInboundScan,
		ScanScore:    &score,
		ScanAction:   string(action),
	}
	res.Events = append(res.Events, identity.ScreeningEvent{
		ID:         identity.DeterministicScreeningEventID(messageID, identity.ScreeningSourceScan, identity.ReviewReasonInboundScan, "heuristics"),
		MessageID:  messageID,
		AgentID:    agent.ID,
		Direction:  "inbound",
		Source:     identity.ScreeningSourceScan,
		Reason:     identity.ReviewReasonInboundScan,
		Action:     string(action),
		Detector:   "heuristics",
		Score:      &score,
		Categories: json.RawMessage(catsJSON),
	})
	return res
}

func scanReason(agg piguard.Aggregate) string {
	if len(agg.Categories) == 0 {
		return "content scan flagged the message"
	}
	return "content scan: " + agg.Categories[0].Name
}

// writeScreeningEvents appends the audit rows best-effort. Deterministic ids +
// ON CONFLICT DO NOTHING make an MTA-retried re-screen idempotent, so writing
// outside the message transaction is safe.
func (s *Server) writeScreeningEvents(ctx context.Context, messageID string, events []identity.ScreeningEvent) {
	for _, ev := range events {
		if err := s.store.CreateScreeningEvent(ctx, ev); err != nil {
			log.Printf("[mail:%s] screening_event write failed (%s/%s): %v", messageID, ev.Source, ev.Reason, err)
		}
	}
}
