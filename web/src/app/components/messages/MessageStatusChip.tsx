// Maps a message's (direction, review_status, webhook_status, inbox_status)
// to a Loft Chip. The chip is the single source of truth for tone +
// label across the inbox bubble footer and the focus-page title row.
//
// Status taxonomy in the backend:
//   - Outbound `review_status`: 'sent' | 'pending_approval' | 'rejected'
//     | 'expired_approved' | 'expired_rejected'.
//   - Outbound `webhook_status`: webhook delivery state. 'failed' is
//     terminal post-retry-exhaustion.
//   - Inbound `inbox_status`: 'unread' | 'read'.
//
// Precedence: webhook_status='failed' dominates HITL state on outbound.
// The data model can't currently produce "pending_approval + failed"
// concurrently (delivery only fires post-approval), but if it ever
// does the chip surfaces Failed — delivery is a louder alarm than a
// stale-but-recoverable approval.

import { Chip, Dot, type ChipTone } from "@e2a/ui";

export type MessageStatusInput = {
  direction: "inbound" | "outbound";
  /** Outbound HITL/send lifecycle. Empty on inbound. */
  review_status?: string;
  /** Outbound webhook delivery state. */
  webhook_status?: string;
  /** Inbound unread→read marker. Empty on outbound. */
  inbox_status?: string;
};

type ChipSpec = { tone: ChipTone; label: string; dot?: boolean };

export function deriveStatusChip(m: MessageStatusInput): ChipSpec {
  if (m.direction === "outbound") {
    if (m.webhook_status === "failed") {
      return { tone: "danger", label: "Failed", dot: true };
    }
    switch (m.review_status) {
      case "pending_review":
        return { tone: "warn", label: "Pending", dot: true };
      case "review_rejected":
        return { tone: "danger", label: "Rejected" };
      case "review_expired_approved":
        return { tone: "success", label: "Sent (auto)" };
      case "review_expired_rejected":
        return { tone: "danger", label: "Auto-rejected" };
      case "sent":
      case undefined:
      case "":
        return { tone: "success", label: "Sent" };
      default:
        return { tone: "neutral", label: m.review_status };
    }
  }
  // Inbound
  if (m.inbox_status === "unread") return { tone: "info", label: "Unread" };
  return { tone: "neutral", label: "Read" };
}

export function MessageStatusChip(props: MessageStatusInput) {
  const spec = deriveStatusChip(props);
  // Only the live-state chips (Pending / Failed) get a leading dot —
  // matches the design mocks. Passive labels (Sent / Read / Unread) read
  // cleaner without one.
  const dotTone =
    spec.tone === "warn" ? "warn" :
    spec.tone === "danger" ? "danger" : null;
  return (
    <Chip tone={spec.tone}>
      {spec.dot && dotTone && <Dot tone={dotTone} />}
      {spec.label}
    </Chip>
  );
}
