package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/outbound"
	"github.com/Mnexa-AI/e2a/internal/piguard"
	"github.com/Mnexa-AI/e2a/internal/webhookpub"
)

// outboundVerdict is the outcome of screening one outbound send: the applied
// action (most-severe of the recipient gate + content scan) plus the data needed
// to annotate the message row, append audit rows, and emit
// email.injection_detected. Mirrors relay.inboundScreenResult on the egress side.
type outboundVerdict struct {
	Applied      piguard.Action // most-severe of gate + scan
	scanAction   piguard.Action // the scan's own action (for its audit row)
	ReviewReason string         // recipient_gate | outbound_scan (drives denorm + event)
	ScanScore    *float64
	Categories   []string // category names for the event payload
	catsJSON     json.RawMessage
	Reason       string
	GateAddr     string // recipient that tripped the gate (audit subject_addr)
	gateFlagged  bool
	scanDetected bool
}

// Block/Review/Annotate/Emit describe what the caller must do with the verdict.
func (v outboundVerdict) Block() bool    { return v.Applied == piguard.ActionBlock }
func (v outboundVerdict) Review() bool   { return v.Applied == piguard.ActionReview }
func (v outboundVerdict) Annotate() bool { return v.Applied != piguard.ActionAllow }

// recipientGate evaluates outbound_policy against the message recipients
// (To+CC+BCC). open: never flagged. allowlist: flagged if any recipient is not in
// outbound_allowlist. domain: flagged if any recipient's domain != the agent's.
// Returns the first offending recipient for the audit row. This is the egress
// firewall and the home of the trust-ramp (allowlist mode + review action).
func recipientGate(agent *identity.AgentIdentity, req outbound.SendRequest) (flagged bool, addr string) {
	switch agent.OutboundPolicy {
	case identity.OutboundPolicyAllowlist:
		allow := make(map[string]struct{}, len(agent.OutboundAllowlist))
		for _, a := range agent.OutboundAllowlist {
			allow[strings.ToLower(strings.TrimSpace(a))] = struct{}{}
		}
		for _, r := range allRecipients(req) {
			if _, ok := allow[strings.ToLower(strings.TrimSpace(r))]; !ok {
				return true, r
			}
		}
	case identity.OutboundPolicyDomain:
		for _, r := range allRecipients(req) {
			if !strings.EqualFold(domainOf(r), agent.Domain) {
				return true, r
			}
		}
	}
	return false, ""
}

func allRecipients(req outbound.SendRequest) []string {
	out := make([]string, 0, len(req.To)+len(req.CC)+len(req.BCC))
	out = append(out, req.To...)
	out = append(out, req.CC...)
	out = append(out, req.BCC...)
	return out
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return strings.TrimSpace(addr[i+1:])
	}
	return ""
}

// composeScanBody reconstructs the outbound message as a REAL MIME blob for
// piguard.Extract, so the same extractor that handles inbound (charset decode,
// attachment scanning regardless of declared type, unscannable→review) handles the
// egress side identically. Outbound content isn't composed into final MIME until
// the sender runs, so we rebuild it here from the SendRequest.
func composeScanBody(req outbound.SendRequest) []byte {
	var b strings.Builder
	b.WriteString("Subject: ")
	b.WriteString(headerSafe(req.Subject))
	b.WriteString("\r\n")

	writeBody := func() {
		if req.HTMLBody != "" {
			b.WriteString("Content-Type: text/html\r\n\r\n")
			b.WriteString(req.HTMLBody)
			if req.Body != "" {
				b.WriteString("\r\n")
				b.WriteString(req.Body)
			}
		} else {
			b.WriteString("Content-Type: text/plain\r\n\r\n")
			b.WriteString(req.Body)
		}
	}

	if len(req.Attachments) == 0 {
		writeBody()
		return []byte(b.String())
	}

	// Multipart so the attachments are real parts: Extract base64-decodes each,
	// scans textual content (a payload mislabeled image/png, a secret in
	// octet-stream — the declared type is attacker-controlled and not trusted), and
	// flags genuinely binary parts unscannable → review.
	const boundary = "e2ascanboundary"
	b.WriteString("Content-Type: multipart/mixed; boundary=" + boundary + "\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	writeBody()
	b.WriteString("\r\n")
	for _, att := range req.Attachments {
		ct := headerSafe(att.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + ct + "\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + headerSafe(att.Filename) + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(att.Data) // already base64; Extract decodes + caps it
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}

// headerSafe strips CR/LF so attacker-controlled subject/filename/content-type can't
// inject extra headers into the reconstructed scan MIME.
func headerSafe(s string) string {
	return strings.NewReplacer("\r", "", "\n", "", "\"", "").Replace(s)
}

// blockAuditID derives a STABLE soft-ref message id for a blocked send. A block
// persists no message row, so the audit/event must anchor to a deterministic id
// (not a fresh random one) — otherwise a retried block writes duplicate
// screening_events rows + duplicate email.injection_detected events. Keyed on the
// request-stable inputs so retries collapse to one audit row.
func blockAuditID(agentID string, req outbound.SendRequest) string {
	h := sha256.New()
	h.Write([]byte(agentID))
	h.Write([]byte{0})
	for _, r := range allRecipients(req) {
		h.Write([]byte(strings.ToLower(strings.TrimSpace(r))))
		h.Write([]byte{0})
	}
	h.Write([]byte(req.Subject))
	h.Write([]byte{0})
	h.Write([]byte(req.Body))
	h.Write([]byte(req.HTMLBody))
	return "msgblk_" + hex.EncodeToString(h.Sum(nil)[:12])
}

// screenOutbound runs the recipient gate + (when outbound_scan='on') the content
// scan over the composed body, and combines them into one applied action. Pure
// w.r.t. storage — the caller persists/annotates based on the verdict.
func (a *API) screenOutbound(ctx context.Context, agent *identity.AgentIdentity, req outbound.SendRequest) outboundVerdict {
	var v outboundVerdict

	gateAction := piguard.ActionAllow
	if flagged, addr := recipientGate(agent, req); flagged {
		gateAction = piguard.Action(agent.OutboundPolicyAction)
		v.gateFlagged = true
		v.GateAddr = addr
	}

	scanAction := piguard.ActionAllow
	if agent.OutboundScan == identity.ScanOn && a.screen != nil {
		body := composeScanBody(req)
		segs, sig, _ := piguard.Extract(body, 0)
		agg := a.screen.Evaluate(ctx, piguard.Request{
			Direction: piguard.DirectionOutput,
			Segments:  segs,
			Signals:   sig,
			Sender:    agent.EmailAddress(),
			SizeBytes: len(body),
		})
		act := agg.Action(agent.OutboundScanReviewThreshold, agent.OutboundScanBlockThreshold)
		if act != piguard.ActionAllow {
			scanAction = act
			score := agg.Score
			v.ScanScore = &score
			v.scanDetected = true
			v.Reason = scanReasonOutbound(agg)
			for _, c := range agg.Categories {
				v.Categories = append(v.Categories, c.Name)
			}
			v.catsJSON, _ = json.Marshal(agg.Categories)
		}
	}

	v.scanAction = scanAction
	v.Applied = piguard.MoreSevere(gateAction, scanAction)

	// Attribute the verdict to its driving producer for the denorm + event.
	switch {
	case v.scanDetected && scanAction == v.Applied:
		v.ReviewReason = identity.ReviewReasonOutboundScan
	case v.gateFlagged:
		v.ReviewReason = identity.ReviewReasonRecipientGate
	case v.scanDetected:
		v.ReviewReason = identity.ReviewReasonOutboundScan
	}
	if v.Reason == "" && v.gateFlagged {
		v.Reason = "recipient not permitted by outbound policy"
	}
	return v
}

// screeningEvents builds the append-only audit rows for this verdict, keyed to
// messageID. Deterministic ids + ON CONFLICT DO NOTHING make a retried send
// idempotent.
func (v outboundVerdict) screeningEvents(messageID string, agent *identity.AgentIdentity) []identity.ScreeningEvent {
	var evs []identity.ScreeningEvent
	if v.gateFlagged {
		evs = append(evs, identity.ScreeningEvent{
			ID:          identity.DeterministicScreeningEventID(messageID, identity.ScreeningSourceGate, identity.ReviewReasonRecipientGate, ""),
			MessageID:   messageID,
			AgentID:     agent.ID,
			Direction:   "outbound",
			Source:      identity.ScreeningSourceGate,
			Reason:      identity.ReviewReasonRecipientGate,
			Action:      agent.OutboundPolicyAction,
			SubjectAddr: v.GateAddr,
		})
	}
	if v.scanDetected {
		evs = append(evs, identity.ScreeningEvent{
			ID:         identity.DeterministicScreeningEventID(messageID, identity.ScreeningSourceScan, identity.ReviewReasonOutboundScan, "heuristics"),
			MessageID:  messageID,
			AgentID:    agent.ID,
			Direction:  "outbound",
			Source:     identity.ScreeningSourceScan,
			Reason:     identity.ReviewReasonOutboundScan,
			Action:     string(v.scanAction),
			Detector:   "heuristics",
			Score:      v.ScanScore,
			Categories: v.catsJSON,
		})
	}
	return evs
}

func scanReasonOutbound(agg piguard.Aggregate) string {
	if len(agg.Categories) == 0 {
		return "content scan flagged the message"
	}
	return "content scan: " + agg.Categories[0].Name
}

// writeScreeningEvents appends the audit rows best-effort (deterministic ids make
// it idempotent, so writing outside any message tx is safe).
func (a *API) writeScreeningEvents(ctx context.Context, messageID string, events []identity.ScreeningEvent) {
	for _, ev := range events {
		if err := a.store.CreateScreeningEvent(ctx, ev); err != nil {
			log.Printf("[mail:%s] screening_event write failed (%s/%s): %v", messageID, ev.Source, ev.Reason, err)
		}
	}
}

// annotateAndAudit denormalizes the verdict onto the (already-created) message
// row, writes the audit rows, and emits email.injection_detected. Used on the
// flag (sent) and review (held) paths once the row id is known.
func (a *API) annotateAndAudit(ctx context.Context, agent *identity.AgentIdentity, messageID string, req outbound.SendRequest, v outboundVerdict) {
	if err := a.store.SetMessageScreening(ctx, messageID, agent.ID, v.ReviewReason, v.ScanScore, string(v.Applied)); err != nil {
		log.Printf("[mail:%s] set screening denorm failed: %v", messageID, err)
	}
	a.writeScreeningEvents(ctx, messageID, v.screeningEvents(messageID, agent))
	a.emitInjectionOutbound(ctx, agent, messageID, req, v)
}

// auditRowless writes the audit rows + emits email.injection_detected WITHOUT
// denormalizing a message row — for outbound paths that persist no message row:
// a blocked (refused) send, and a flagged test send. Callers pass a stable
// soft-ref id (blockAuditID) so retries stay idempotent.
func (a *API) auditRowless(ctx context.Context, agent *identity.AgentIdentity, messageID string, req outbound.SendRequest, v outboundVerdict) {
	a.writeScreeningEvents(ctx, messageID, v.screeningEvents(messageID, agent))
	a.emitInjectionOutbound(ctx, agent, messageID, req, v)
}

// emitInjectionOutbound fires the fire-and-forget email.injection_detected event
// for an outbound verdict (mirrors the inbound emission in relay/server.go).
func (a *API) emitInjectionOutbound(ctx context.Context, agent *identity.AgentIdentity, messageID string, req outbound.SendRequest, v outboundVerdict) {
	score := 0.0
	if v.ScanScore != nil {
		score = *v.ScanScore
	}
	e := webhookpub.NewEvent(webhookpub.EventEmailInjectionDetected, agent.UserID, map[string]interface{}{
		"message_id": messageID,
		"agent":      map[string]interface{}{"id": agent.ID, "email": agent.EmailAddress(), "domain": agent.Domain},
		"direction":  "outbound",
		"recipients": allRecipients(req),
		"subject":    req.Subject,
		"score":      score,
		"action":     string(v.Applied),
		"categories": v.Categories,
		"reason":     v.Reason,
	})
	e.AgentID = agent.ID
	e.ConversationID = req.ConversationID
	e.MessageID = messageID
	e.ID = webhookpub.DeterministicEventID(messageID, webhookpub.EventEmailInjectionDetected)
	a.publishAsync(e)
}
