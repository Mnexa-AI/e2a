import { Chip, Dot, type ChipTone } from "@e2a/ui";

export type MessageStatusInput = {
  direction: "inbound" | "outbound";
  /** Outbound delivery lifecycle. Empty on inbound. */
  delivery_status?: string;
  /** Outbound HITL/send lifecycle. Empty on inbound. */
  review_status?: string;
  /** Inbound unread→read marker. Empty on outbound. */
  inbox_status?: string;
};

export type MessageStatusSpec = {
  tone: ChipTone;
  label: string;
  dot?: boolean;
  attention: boolean;
};

export function deriveStatusChip(m: MessageStatusInput): MessageStatusSpec | null {
  if (m.direction === "inbound") {
    if (m.inbox_status === "unread") {
      return { tone: "info", label: "Unread", attention: false };
    }
    return { tone: "neutral", label: "Read", attention: false };
  }

  switch (m.review_status) {
    case "pending_review":
      return {
        tone: "warn",
        label: "Pending review",
        dot: true,
        attention: true,
      };
    case "review_rejected":
      return { tone: "danger", label: "Rejected", attention: true };
    case "review_expired_rejected":
      return { tone: "danger", label: "Auto-rejected", attention: true };
  }

  if (m.delivery_status) {
    switch (m.delivery_status) {
      case "accepted":
        return { tone: "info", label: "Queued", attention: true };
      case "sending":
        return { tone: "info", label: "Sending", attention: true };
      case "deferred":
        return { tone: "warn", label: "Delayed", attention: true };
      case "failed":
        return { tone: "danger", label: "Failed", attention: true };
      case "bounced":
        return { tone: "danger", label: "Bounced", attention: true };
      case "complained":
        return { tone: "danger", label: "Complaint", attention: true };
      case "sent":
        return { tone: "success", label: "Sent", attention: false };
      case "delivered":
        return { tone: "success", label: "Delivered", attention: false };
      default:
        return {
          tone: "neutral",
          label: m.delivery_status,
          attention: false,
        };
    }
  }

  switch (m.review_status) {
    case "sent":
      return { tone: "success", label: "Sent", attention: false };
    case "review_expired_approved":
      return { tone: "success", label: "Sent (auto)", attention: false };
    default:
      return null;
  }
}

export function MessageStatusChip({
  className,
  ...props
}: MessageStatusInput & { className?: string }) {
  const spec = deriveStatusChip(props);
  if (!spec) return null;

  const dotTone =
    spec.tone === "warn"
      ? "warn"
      : spec.tone === "danger"
        ? "danger"
        : null;

  return (
    <Chip tone={spec.tone} className={className}>
      {spec.dot && dotTone && <Dot tone={dotTone} />}
      {spec.label}
    </Chip>
  );
}
