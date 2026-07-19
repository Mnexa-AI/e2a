package httpapi

import "github.com/tokencanopy/e2a/internal/identity"

// HoldReasonView is the product-facing explanation for why a message is in
// the review queue. Code is an open set; clients should render Summary rather
// than translating Code themselves.
type HoldReasonView struct {
	Type       string   `json:"type" doc:"Producer that caused the hold. Open set; tolerate unknown values. Known values: gate, scan, send, unknown."`
	Code       string   `json:"code" doc:"Stable machine-readable hold code. Open set; tolerate unknown values."`
	Summary    string   `json:"summary" doc:"Plain-language explanation suitable for showing directly to a reviewer."`
	Category   string   `json:"category,omitempty" doc:"Top screening category when a scan hold has detail enrichment."`
	Detail     string   `json:"detail,omitempty" doc:"Validated plain-language detector rationale when available."`
	Confidence *float64 `json:"confidence,omitempty" doc:"Screening confidence from 0 through 1 when available. Intended for expanded technical detail, not the queue summary."`
}

func baseHoldReason(code string) *HoldReasonView {
	if code == "" {
		return nil
	}

	reason := &HoldReasonView{Code: code}
	switch code {
	case identity.ReviewReasonSenderGate:
		reason.Type = "gate"
		reason.Summary = "This sender isn't allowed by the inbox policy."
	case identity.ReviewReasonRecipientGate:
		reason.Type = "gate"
		reason.Summary = "One or more recipients aren't allowed by the inbox policy."
	case identity.ReviewReasonInboundScan, identity.ReviewReasonOutboundScan:
		reason.Type = "scan"
		reason.Summary = "Content screening found a potential risk."
	case identity.ReviewReasonOutboundSend:
		reason.Type = "send"
		reason.Summary = "This outbound message requires review before sending."
	default:
		reason.Type = "unknown"
		reason.Summary = "This message requires review."
	}
	return reason
}
