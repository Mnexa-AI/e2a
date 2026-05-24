// Maps a message's (direction, status, webhook_status) to a Loft Chip.
//
// Status taxonomy comes from two sources in the backend:
//   - Outbound messages carry status: pending_approval | sent | rejected |
//     expired_approved | expired_rejected. Webhook delivery failure is
//     surfaced separately via webhook_status='failed'.
//   - Inbound messages carry inbox_status: unread | read.
//
// The chip collapses both into the visual vocabulary documented in
// agent_messages/README.md → "Status chip mapping".

import { Chip } from "../loft/Chip";
import { Dot } from "../loft/Dot";
import type { ChipTone } from "../loft/Chip";

export type MessageStatusInput = {
  direction: "inbound" | "outbound";
  // Outbound-only — the HITL/send lifecycle.
  status?: string;
  // Outbound-only — failed when retries are exhausted.
  webhook_status?: string;
  // Inbound-only — unread until the agent has read it (poll-mode).
  inbox_status?: string;
};

type ChipSpec = { tone: ChipTone; label: string; dot?: boolean };

export function deriveStatusChip(m: MessageStatusInput): ChipSpec {
  if (m.direction === "outbound") {
    if (m.webhook_status === "failed") {
      return { tone: "danger", label: "Failed", dot: true };
    }
    switch (m.status) {
      case "pending_approval":
        return { tone: "warn", label: "Pending", dot: true };
      case "rejected":
        return { tone: "danger", label: "Rejected" };
      case "expired_approved":
        return { tone: "success", label: "Sent (auto)" };
      case "expired_rejected":
        return { tone: "danger", label: "Auto-rejected" };
      case "sent":
      case undefined:
      case "":
        return { tone: "success", label: "Sent" };
      default:
        return { tone: "neutral", label: m.status };
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
