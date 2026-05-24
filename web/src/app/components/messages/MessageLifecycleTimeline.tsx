"use client";

// 4-step vertical timeline shown inside the focus page's Lifecycle
// collapsible. The four steps cover the outbound HITL lifecycle:
//
//   1. Inbound received     (only when this is a reply — for plain
//      outbound sends the step is omitted)
//   2. Agent drafted reply
//   3. Held for HITL approval
//   4. Sent to recipient
//
// We compute step state from the focus payload's status + timestamps.
// All inputs are pure — the caller passes the resolved values and we
// render. Keeps the timeline easy to unit-test.

export type LifecycleStepKind = "success" | "info" | "warn" | "neutral";

export type LifecycleStep = {
  label: string;
  /** Mono caption shown under the label (timestamp + sub-info). Empty allowed. */
  caption: string;
  /** Dot color. */
  kind: LifecycleStepKind;
  /** Step is the current one — gets a warn-bg halo. */
  current?: boolean;
  /** Step hasn't happened yet — renders as a dashed outline dot. */
  pending?: boolean;
};

export function MessageLifecycleTimeline({ steps }: { steps: LifecycleStep[] }) {
  return (
    <div
      data-testid="lifecycle-timeline"
      style={{ padding: "14px 18px 16px", position: "relative" }}
    >
      {steps.map((s, i) => (
        <div
          key={i}
          className="flex"
          style={{
            gap: 11,
            position: "relative",
            paddingBottom: i < steps.length - 1 ? 12 : 0,
          }}
        >
          {/* connector line — only between dots, not after the last */}
          {i < steps.length - 1 && (
            <span
              aria-hidden
              style={{
                position: "absolute",
                left: 5,
                top: 14,
                bottom: 0,
                width: 1,
                background: "var(--border-sub)",
              }}
            />
          )}
          <span
            aria-hidden
            style={{
              width: 11,
              height: 11,
              borderRadius: "50%",
              background: s.pending
                ? "var(--bg-elev)"
                : s.kind === "success"
                  ? "var(--success)"
                  : s.kind === "info"
                    ? "var(--info)"
                    : s.kind === "warn"
                      ? "var(--warn)"
                      : "var(--fg-subtle)",
              border: s.pending ? "1px dashed var(--border-strong)" : "none",
              marginTop: 4,
              flexShrink: 0,
              boxShadow: s.current ? "0 0 0 3px var(--warn-bg)" : "none",
            }}
          />
          <div className="flex-1 min-w-0">
            <div
              style={{
                fontSize: 12,
                fontWeight: s.current ? 600 : 500,
                color: s.pending ? "var(--fg-subtle)" : "var(--fg)",
              }}
            >
              {s.label}
            </div>
            {s.caption && (
              <div
                style={{
                  fontFamily: "var(--f-mono)",
                  fontSize: 10,
                  color: "var(--fg-subtle)",
                  letterSpacing: "0.02em",
                  marginTop: 1,
                }}
              >
                {s.caption}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

// Derive the 4-step lifecycle from an outbound focus payload's status +
// timestamps. Pure function exported for testing.
export type LifecycleInput = {
  /** Outbound HITL status: 'sent' | 'pending_approval' | 'rejected' |
   *  'expired_approved' | 'expired_rejected'. */
  status: string;
  /** ISO created_at of the outbound draft. */
  draftedAt: string;
  /** Optional ISO timestamp of the parent inbound (only present for replies). */
  inboundReceivedAt?: string | null;
  /** Optional ISO timestamp of the approval/rejection action. */
  reviewedAt?: string | null;
  /** Optional TTL hint for the pending step. */
  ttlHint?: string;
};

function fmtClock(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function deriveLifecycleSteps(input: LifecycleInput): LifecycleStep[] {
  const steps: LifecycleStep[] = [];

  if (input.inboundReceivedAt) {
    steps.push({
      label: "Inbound received",
      caption: `${fmtClock(input.inboundReceivedAt)} · auth verified`,
      kind: "success",
    });
  }

  steps.push({
    label: "Agent drafted reply",
    caption: `${fmtClock(input.draftedAt)} · agent`,
    kind: "info",
  });

  const terminal =
    input.status === "sent" ||
    input.status === "rejected" ||
    input.status === "expired_approved" ||
    input.status === "expired_rejected";

  steps.push({
    label: "Held for HITL approval",
    caption: terminal
      ? input.reviewedAt
        ? `${fmtClock(input.reviewedAt)} · resolved`
        : "resolved"
      : `${input.ttlHint ?? "TTL"} · auto-reject on expiry`,
    kind: "warn",
    current: input.status === "pending_approval",
  });

  if (input.status === "sent" || input.status === "expired_approved") {
    steps.push({
      label: "Sent to recipient",
      caption: `${input.reviewedAt ? fmtClock(input.reviewedAt) : "—"} · ${input.status === "expired_approved" ? "auto-approved" : "delivered"}`,
      kind: "success",
    });
  } else if (input.status === "rejected" || input.status === "expired_rejected") {
    steps.push({
      label: "Rejected",
      caption: `${input.reviewedAt ? fmtClock(input.reviewedAt) : "—"} · ${input.status === "expired_rejected" ? "auto-rejected" : "by reviewer"}`,
      kind: "neutral",
    });
  } else {
    // Pending — last step is in the future.
    steps.push({
      label: "Sent to recipient",
      caption: "— · awaiting reviewer",
      kind: "neutral",
      pending: true,
    });
  }

  return steps;
}
