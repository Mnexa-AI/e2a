package outbound

import (
	"encoding/base64"
	"strings"
)

// MaxComposedMessageBytes is the hard cap on a composed outbound message — the
// sum of subject + text + html + DECODED attachment bytes. It is the SES v1
// stored-message ceiling (the real upstream limit), distinct from the per-field
// (subject/body) and per-attachment caps: a caller can stay under every
// individual limit while the composed MIME still exceeds what the upstream
// provider accepts, so the true ceiling is checked on the fully-composed
// content. Over → 413 payload_too_large at the API edge.
const MaxComposedMessageBytes = 10 * 1024 * 1024

// ComposedSize returns the composed byte total of an outbound message: the sum
// of subject + text + html + DECODED attachment bytes. Attachment Data is
// base64; embedded whitespace (CR/LF, spaces, tabs) is stripped before decoding
// and a decode failure falls back to the raw wire length so the total is never
// under-counted.
//
// This is the single source of truth for the composed-message ceiling, shared
// by the direct send path (httpapi send/reply/forward) and the HITL approve-
// override path (agent) so neither entry point can bypass the cap.
func ComposedSize(subject, text, html string, atts []Attachment) int {
	total := len(subject) + len(text) + len(html)
	for _, att := range atts {
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, att.Data)
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			total += len(att.Data) // never under-count on a decode miss
			continue
		}
		total += len(decoded)
	}
	return total
}
