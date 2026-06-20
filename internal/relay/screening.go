package relay

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/Mnexa-AI/e2a/internal/emailauth"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/inboundpolicy"
	"github.com/Mnexa-AI/e2a/internal/piguard"
)

// inboundScreenResult is the outcome of content-screening one inbound message: the
// denormalized verdict (incl. any review-hold status) for the message row, the audit
// rows to append, and the data the email.injection_detected event carries.
type inboundScreenResult struct {
	Denorm        identity.InboundScreening
	Events        []identity.ScreeningEvent
	AppliedAction piguard.Action // most-severe of gate + scan
	Hold          bool           // applied action is review|block → suppress delivery
	Detected      bool           // a scan violation fired (used to attribute payload fields)
	Score         float64
	Action        string
	Categories    []string
	Reason        string
}

// Emit reports whether the email.injection_detected event should fire: when the scan
// flagged the message OR it was held.
func (r inboundScreenResult) Emit() bool { return r.Detected || r.Hold }

// screenInbound runs the agent's content scan (when inbound_scan='on'), combines it
// with the ingestion-gate decision into one applied action, and decides whether the
// message is HELD (review/block) or delivered (flag/allow).
//
//   - review → held as pending_review (awaiting a human / TTL), delivery suppressed.
//   - block  → accept-then-quarantine as review_rejected (dropped, no human),
//     delivery suppressed.
//   - flag   → delivered + annotated (the gate's email.flagged path is unchanged).
//   - allow  → delivered normally.
func (s *Server) screenInbound(ctx context.Context, agent *identity.AgentIdentity, messageID, senderEmail string, body []byte, auth *emailauth.Result, gate inboundpolicy.Decision) inboundScreenResult {
	var res inboundScreenResult

	// Gate action: a flagged sender escalates to the agent's inbound_policy_action
	// (default 'flag' → no behavior change; operators opt into review/block).
	gateAction := piguard.ActionAllow
	if gate.Flagged {
		gateAction = piguard.Action(agent.InboundPolicyAction)
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

	// Scan action.
	scanAction := piguard.ActionAllow
	var scanScore *float64
	if agent.InboundScan == identity.ScanOn {
		segs, sig, _ := piguard.Extract(body, 0)
		agg := s.screen.Evaluate(ctx, piguard.Request{
			Direction: piguard.DirectionInput,
			Segments:  segs,
			Signals:   sig,
			Sender:    senderEmail,
			Auth:      auth,
			SizeBytes: len(body),
		})
		act := agg.Action(agent.InboundScanReviewThreshold, agent.InboundScanBlockThreshold)
		// Record only violations (action ≠ allow). A below-threshold score is allowed
		// and delivered silently; flag (from the force-floor, e.g. Unicode tags) is
		// recorded + delivered; review/block are held.
		if act != piguard.ActionAllow {
			scanAction = act
			score := agg.Score
			scanScore = &score
			res.Detected = true
			res.Score = score
			res.Reason = scanReason(agg)
			for _, c := range agg.Categories {
				res.Categories = append(res.Categories, c.Name)
			}
			catsJSON, _ := json.Marshal(agg.Categories)
			res.Events = append(res.Events, identity.ScreeningEvent{
				ID:         identity.DeterministicScreeningEventID(messageID, identity.ScreeningSourceScan, identity.ReviewReasonInboundScan, "heuristics"),
				MessageID:  messageID,
				AgentID:    agent.ID,
				Direction:  "inbound",
				Source:     identity.ScreeningSourceScan,
				Reason:     identity.ReviewReasonInboundScan,
				Action:     string(act),
				Detector:   "heuristics",
				Score:      &score,
				Categories: json.RawMessage(catsJSON),
			})
		}
	}

	applied := piguard.MoreSevere(gateAction, scanAction)
	res.AppliedAction = applied
	res.Action = string(applied)
	res.Hold = applied == piguard.ActionReview || applied == piguard.ActionBlock

	if applied == piguard.ActionAllow {
		return res // benign: no denorm, no hold (gate audit row may still be present)
	}

	// Attribute the verdict to its driving producer for the denorm + event.
	reviewReason := identity.ReviewReasonSenderGate
	if res.Detected && scanAction == applied {
		reviewReason = identity.ReviewReasonInboundScan
	} else if !gate.Flagged && res.Detected {
		reviewReason = identity.ReviewReasonInboundScan
	}
	if res.Reason == "" {
		res.Reason = gate.Reason
	}

	res.Denorm = identity.InboundScreening{
		ReviewReason: reviewReason,
		ScanScore:    scanScore,
		ScanAction:   string(applied),
	}
	if res.Hold {
		if applied == piguard.ActionBlock {
			// Accept-then-quarantine: persisted but terminal-dropped, no human.
			res.Denorm.Status = identity.MessageStatusReviewRejected
		} else {
			res.Denorm.Status = identity.MessageStatusPendingReview
			ttl := agent.HITLTTLSeconds
			if ttl <= 0 {
				ttl = identity.HITLDefaultTTLSeconds
			}
			exp := time.Now().Add(time.Duration(ttl) * time.Second)
			res.Denorm.ApprovalExpiresAt = &exp
		}
	}
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
