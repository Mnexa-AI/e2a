// Maps the backend's coded hold reason (ReviewView.review_reason /
// MessageView.review_reason) to the reader-friendly line shown in the review
// queue. The API value is an OPEN set — new codes may appear before the UI
// knows them — so unknown codes fall back to a humanized form rather than being
// dropped. Known codes mirror internal/identity/screening.go.
const REVIEW_REASON_LABELS: Record<string, string> = {
  sender_gate: "Sender blocked by inbound policy",
  recipient_gate: "Recipient blocked by outbound policy",
  inbound_scan: "Content flagged by screening scan",
  outbound_scan: "Content flagged by screening scan",
  outbound_send: "Outbound send blocked",
};

// Humanize an unknown coded value: "some_new_reason" → "Some new reason".
function humanizeCode(code: string): string {
  const spaced = code.replace(/_/g, " ").trim();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

// Builds the "why held" line for a review row. Returns null when there is no
// reason to show (so callers can omit the element entirely). `score` — the
// aggregate content-scan confidence (0..1), present only for scan holds — is
// appended as a parenthetical when available.
export function reviewReasonLabel(
  reason?: string | null,
  score?: number | null,
): string | null {
  if (!reason) return null;
  const base = REVIEW_REASON_LABELS[reason] ?? humanizeCode(reason);
  if (typeof score === "number" && Number.isFinite(score)) {
    return `${base} (${score.toFixed(2)})`;
  }
  return base;
}
