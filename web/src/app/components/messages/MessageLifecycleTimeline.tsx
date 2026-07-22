"use client";

// Canonical observation timeline shown in the conversation and focus views.

import { useState } from "react";
import useSWRInfinite from "swr/infinite";
import {
  getMessageLifecycle,
  type MessageLifecyclePageWire,
  type MessageLifecycleTransitionWire,
} from "../../../lib/messageLifecycle";

const REASON_LABELS: Record<MessageLifecycleTransitionWire["reason_code"], string> = {
  "acceptance.inbound_smtp": "Accepted via inbound SMTP",
  "acceptance.outbound_api": "Accepted by e2a",
  "acceptance.local_loopback": "Accepted for local delivery",
  "authentication.dmarc_pass": "DMARC passed",
  "authentication.dmarc_fail": "DMARC failed",
  "authentication.dmarc_none": "DMARC had no policy",
  "authentication.dmarc_temporary_error": "DMARC check temporarily unavailable",
  "authentication.dmarc_permanent_error": "DMARC check failed",
  "review.hold_created": "Held for review",
  "review.approved": "Review approved",
  "review.rejected": "Review rejected",
  "review.expired_approved": "Review expired and was approved",
  "review.expired_rejected": "Review expired and was rejected",
  "suppression.recipient_blocked": "Recipient suppressed",
  "suppression.hard_bounce_applied": "Suppressed after hard bounce",
  "suppression.complaint_applied": "Suppressed after complaint",
  "queue.inbound_processing": "Queued for inbound processing",
  "queue.outbound_submission": "Queued for submission",
  "submission.upstream_accepted": "Accepted by upstream provider",
  "submission.local_loopback_accepted": "Accepted for local delivery",
  "submission.temporary_failure": "Submission delayed",
  "submission.provider_rejected": "Rejected by upstream provider",
  "submission.local_retries_exhausted": "Submission retries exhausted",
  "submission.cancelled": "Submission cancelled",
  "delivery.recipient_server_accepted": "Accepted by recipient server",
  "delivery.temporary_delay": "Delivery temporarily delayed",
  "delivery.permanent_bounce": "Permanently bounced",
  "delivery.transient_bounce": "Temporarily bounced",
  "delivery.undetermined_bounce": "Bounce classification unavailable",
  "complaint.recipient_reported": "Recipient reported spam",
};

function lifecycleSummary(last: MessageLifecycleTransitionWire): string {
  switch (last.reason_code) {
    case "review.hold_created":
      return "Pending review";
    case "acceptance.inbound_smtp":
    case "acceptance.outbound_api":
    case "acceptance.local_loopback":
      return "Accepted · awaiting next observation";
    case "queue.inbound_processing":
      return "Queued · awaiting processing";
    case "queue.outbound_submission":
      return "Queued · awaiting submission";
    case "submission.temporary_failure":
    case "delivery.temporary_delay":
      return last.retryable ? "Delayed · retrying" : "Delayed";
    case "submission.upstream_accepted":
    case "submission.local_loopback_accepted":
      return "Sent";
    case "delivery.recipient_server_accepted":
      return "Delivered to recipient server";
    case "delivery.permanent_bounce":
    case "delivery.transient_bounce":
    case "delivery.undetermined_bounce":
      return "Bounced";
    case "complaint.recipient_reported":
      return "Complaint reported";
    case "review.rejected":
    case "review.expired_rejected":
    case "submission.provider_rejected":
    case "submission.local_retries_exhausted":
    case "submission.cancelled":
    case "suppression.recipient_blocked":
      return "Failed";
    default:
      return REASON_LABELS[last.reason_code];
  }
}

function observationTone(row: MessageLifecycleTransitionWire): string {
  if (["failed", "rejected", "blocked", "bounced", "reported"].includes(row.outcome)) {
    return "var(--danger-strong)";
  }
  if (row.outcome === "pending" || row.outcome === "deferred" || row.retryable) {
    return "var(--warn-strong)";
  }
  if (["passed", "approved", "delivered", "accepted"].includes(row.outcome)) {
    return "var(--success)";
  }
  return "var(--fg-muted)";
}

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
  });
}

function DiagnosticMap({ title, values }: { title: string; values: Record<string, unknown> }) {
  const entries = Object.entries(values);
  if (entries.length === 0) return null;
  return (
    <div>
      <div style={{ color: "var(--fg-subtle)", marginBottom: 3 }}>{title}</div>
      {entries.map(([key, value]) => (
        <div key={key} className="flex" style={{ gap: 8 }}>
          <span style={{ color: "var(--fg-muted)" }}>{key}</span>
          <span style={{ color: "var(--fg)" }}>{typeof value === "string" ? value : JSON.stringify(value)}</span>
        </div>
      ))}
    </div>
  );
}

function CanonicalObservation({ row, last }: { row: MessageLifecycleTransitionWire; last: boolean }) {
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const label = REASON_LABELS[row.reason_code];
  return (
    <div className="flex" style={{ gap: 11, position: "relative", paddingBottom: last ? 0 : 15 }}>
      {!last && (
        <span aria-hidden style={{ position: "absolute", left: 5, top: 14, bottom: 0, width: 1, background: "var(--border-sub)" }} />
      )}
      <span
        aria-hidden
        style={{
          width: 11,
          height: 11,
          marginTop: 4,
          flexShrink: 0,
          borderRadius: "50%",
          background: observationTone(row),
          boxShadow: last ? "0 0 0 3px var(--bg-elev)" : "none",
        }}
      />
      <div className="flex-1 min-w-0">
        <div className="flex items-start" style={{ gap: 8 }}>
          <div className="flex-1 min-w-0">
            <div style={{ fontSize: 12, fontWeight: last ? 650 : 500, color: "var(--fg)" }}>{label}</div>
            <div style={{ fontFamily: "var(--f-mono)", fontSize: 10, color: "var(--fg-subtle)", marginTop: 2 }}>
              {formatTimestamp(row.occurred_at)} · {row.reason_code}
            </div>
          </div>
          <span style={{ fontFamily: "var(--f-mono)", fontSize: 10, color: observationTone(row), textTransform: "capitalize" }}>
            {row.outcome}
          </span>
        </div>
        <button
          type="button"
          onClick={() => setDiagnosticsOpen((open) => !open)}
          aria-expanded={diagnosticsOpen}
          style={{
            marginTop: 5,
            padding: 0,
            border: 0,
            background: "transparent",
            color: "var(--accent-strong)",
            cursor: "pointer",
            fontFamily: "var(--f-mono)",
            fontSize: 10,
          }}
        >
          {diagnosticsOpen ? "Hide diagnostics" : "Diagnostics"}
        </button>
        {diagnosticsOpen && (
          <div
            style={{
              display: "grid",
              gap: 7,
              marginTop: 7,
              padding: "8px 10px",
              background: "var(--bg-elev)",
              border: "1px solid var(--border-sub)",
              borderRadius: "var(--r-sm)",
              fontFamily: "var(--f-mono)",
              fontSize: 10,
              lineHeight: 1.5,
              overflowWrap: "anywhere",
            }}
          >
            <div className="flex flex-wrap" style={{ gap: 8, color: "var(--fg-muted)" }}>
              {row.retryable && <span>Retryable</span>}
              {row.reconstructed && <span>Reconstructed from durable history</span>}
              {row.recipient && <span>Recipient: {row.recipient}</span>}
            </div>
            <DiagnosticMap title="Evidence" values={row.evidence} />
            <DiagnosticMap title="Correlation IDs" values={row.correlation_ids} />
          </div>
        )}
      </div>
    </div>
  );
}

export function MessageLifecycleTimeline({
  transitions,
}: {
  transitions: MessageLifecycleTransitionWire[];
}) {
  if (transitions.length === 0) return null;
  return (
    <div data-testid="lifecycle-timeline" style={{ padding: "14px 18px 16px", position: "relative" }}>
      <div style={{ marginBottom: 13 }}>
        <div style={{ fontSize: 13, fontWeight: 650, color: "var(--fg)" }}>
          {lifecycleSummary(transitions[transitions.length - 1])}
        </div>
        <div style={{ marginTop: 2, fontFamily: "var(--f-mono)", fontSize: 10, color: "var(--fg-subtle)" }}>
          Observations recorded by e2a · not an inbox-placement claim
        </div>
      </div>
      {transitions.map((row, index) => (
        <CanonicalObservation key={row.id} row={row} last={index === transitions.length - 1} />
      ))}
    </div>
  );
}

export function MessageLifecycleData({
  email,
  messageId,
}: {
  email: string;
  messageId: string;
}) {
  const { data: pages, error, isLoading, isValidating, size, setSize } = useSWRInfinite<MessageLifecyclePageWire>(
    (pageIndex, previousPage) => {
      if (previousPage && previousPage.next_cursor === null) return null;
      return [
        "message-lifecycle",
        email,
        messageId,
        pageIndex,
        previousPage?.next_cursor ?? null,
      ] as const;
    },
    ([, lifecycleEmail, lifecycleMessageId, , cursor]: readonly [
      string,
      string,
      string,
      number,
      string | null,
    ]) =>
      getMessageLifecycle(
        lifecycleEmail,
        lifecycleMessageId,
        cursor ? { cursor, limit: 100 } : { limit: 100 },
      ),
    { shouldRetryOnError: false, revalidateFirstPage: false },
  );
  const items = pages?.flatMap((page) => page.items) ?? [];
  const nextCursor = pages?.[pages.length - 1]?.next_cursor ?? null;

  if (isLoading) {
    return (
      <div role="status" style={{ padding: "14px 18px", fontSize: 12, color: "var(--fg-muted)" }}>
        Loading lifecycle…
      </div>
    );
  }
  if (error) {
    return (
      <div role="alert" style={{ padding: "14px 18px", fontSize: 12, color: "var(--danger-strong)" }}>
        Lifecycle unavailable. Try again shortly.
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <div style={{ padding: "14px 18px", fontSize: 12, color: "var(--fg-muted)" }}>
        No lifecycle observations were recorded for this message.
      </div>
    );
  }
  return (
    <>
      <MessageLifecycleTimeline transitions={items} />
      {nextCursor && (
        <button
          type="button"
          onClick={() => void setSize(size + 1)}
          disabled={isValidating}
          style={{
            width: "100%",
            padding: "9px 18px",
            background: "var(--bg-elev)",
            border: 0,
            borderTop: "1px solid var(--border-sub)",
            color: "var(--accent-strong)",
            cursor: isValidating ? "default" : "pointer",
            fontFamily: "var(--f-mono)",
            fontSize: 10,
          }}
        >
          {isValidating ? "Loading more…" : "Load more observations"}
        </button>
      )}
    </>
  );
}
