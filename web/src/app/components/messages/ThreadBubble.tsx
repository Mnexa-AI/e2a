"use client";

// One chat bubble inside the thread detail view. Inbound bubbles sit on
// the left with a `bg-panel` background and `--border`; outbound on the
// right with `--accent-soft` + `--accent`. The "tail" corner (top of
// the avatar side) is reduced to 4px to match the email-client look.
//
// Footer mono row carries the message's provenance/delivery signal —
// what we can derive from the wire payload today: size · auth pill
// (inbound) or status (outbound) · `headers` link to the focus page.

import { Chip } from "../loft/Chip";
import { Dot } from "../loft/Dot";
import { CounterpartyAvatar } from "./CounterpartyAvatar";
import { deriveStatusChip } from "./MessageStatusChip";
import { formatRelativeAge } from "../../../lib/relativeTime";
import type { MessageSummary } from "../types";
import type { Counterparty } from "./threading";

function formatSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function ThreadBubble({
  message,
  counterparty,
  agentEmail,
  onOpen,
  onOpenHeaders,
}: {
  message: MessageSummary;
  counterparty: Counterparty;
  agentEmail: string;
  /** Click on the bubble body — navigate to the focus page. */
  onOpen: (messageId: string) => void;
  /** Click on the bubble footer's `headers` link — focus page w/ headers open. */
  onOpenHeaders: (messageId: string) => void;
}) {
  const isInbound = message.direction === "inbound";
  const pending = message.hitl_status === "pending_approval";
  const size = formatSize(message.size_bytes);

  return (
    <div
      data-testid="thread-bubble"
      data-message-id={message.message_id}
      className="flex"
      style={{
        gap: 12,
        flexDirection: isInbound ? "row" : "row-reverse",
        marginBottom: 24,
        alignItems: "flex-start",
      }}
    >
      {/* Avatar (counterparty on inbound, agent tile on outbound) */}
      <div style={{ flexShrink: 0, paddingTop: 4 }}>
        {isInbound ? (
          <CounterpartyAvatar
            email={counterparty.email}
            name={counterparty.name}
            size={32}
          />
        ) : (
          <span
            aria-hidden
            style={{
              width: 32,
              height: 32,
              borderRadius: 6,
              background: "var(--fg)",
              color: "var(--bg)",
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              fontFamily: "var(--f-mono)",
              fontSize: 10,
              fontWeight: 700,
            }}
          >
            e2a
          </span>
        )}
      </div>

      {/* Bubble + header + footer */}
      <div style={{ maxWidth: 560, minWidth: 0, flex: 1 }}>
        {/* Header row above the bubble */}
        <div
          className="flex items-baseline"
          style={{
            gap: 8,
            marginBottom: 5,
            flexDirection: isInbound ? "row" : "row-reverse",
          }}
        >
          <span
            style={{
              fontSize: 12,
              fontWeight: 600,
              color: "var(--fg)",
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
              maxWidth: 240,
            }}
          >
            {isInbound ? counterparty.name : agentEmail}
          </span>
          <span
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 10,
              color: "var(--fg-subtle)",
              whiteSpace: "nowrap",
            }}
          >
            {isInbound
              ? counterparty.email
              : pending
                ? "agent · drafted"
                : "agent"}{" "}
            · {formatRelativeAge(message.created_at)}
          </span>
          {pending && (
            <Chip tone="warn">
              <Dot tone="warn" /> Pending
            </Chip>
          )}
        </div>

        {/* Bubble body — clicking navigates to the focus page */}
        <button
          type="button"
          onClick={() => onOpen(message.message_id)}
          className="text-left"
          style={{
            background: isInbound ? "var(--bg-panel)" : "var(--accent-soft)",
            border: `1px solid ${isInbound ? "var(--border)" : "var(--accent)"}`,
            borderRadius: "var(--r-lg)",
            borderTopLeftRadius: isInbound ? 4 : "var(--r-lg)",
            borderTopRightRadius: isInbound ? "var(--r-lg)" : 4,
            padding: "12px 14px",
            fontSize: 13.5,
            lineHeight: 1.55,
            color: "var(--fg)",
            display: "block",
            width: "100%",
            cursor: "pointer",
          }}
        >
          {/* Subject + preview — body parts aren't on the wire yet, so we
              show the subject as a placeholder. When `body_text` lands on
              ActivityEntry, swap this for a short preview snippet. */}
          {message.subject || (
            <span style={{ fontStyle: "italic", color: "var(--fg-muted)" }}>
              (no subject)
            </span>
          )}
        </button>

        {/* Footer: provenance / delivery */}
        <div
          className="flex items-center"
          style={{
            marginTop: 6,
            gap: 10,
            flexDirection: isInbound ? "row" : "row-reverse",
            fontFamily: "var(--f-mono)",
            fontSize: 10,
            color: "var(--fg-subtle)",
            letterSpacing: "0.02em",
          }}
        >
          {size && <span>{size}</span>}
          {size && <span aria-hidden>·</span>}
          {isInbound ? (
            <span style={{ color: "var(--success)" }}>auth verified</span>
          ) : (
            <OutboundStatusInline message={message} />
          )}
          <span aria-hidden>·</span>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onOpenHeaders(message.message_id);
            }}
            style={{
              color: "var(--accent-strong)",
              background: "transparent",
              border: "none",
              padding: 0,
              cursor: "pointer",
              fontFamily: "inherit",
              fontSize: "inherit",
            }}
          >
            headers
          </button>
        </div>
      </div>
    </div>
  );
}

// Webhook error strings are uncontrolled by us — they come from
// whatever the customer's webhook target returned (often URLs with
// query strings or sometimes echoed credentials). Truncate to a sane
// length and strip query parameters from anything that looks like a
// URL so screen-shares / screenshots don't accidentally leak request
// internals.
function scrubWebhookError(raw: string): string {
  if (!raw) return "";
  const stripped = raw.replace(/(https?:\/\/[^\s?]+)\?[^\s]*/gi, "$1?…");
  return stripped.length > 80 ? stripped.slice(0, 77) + "…" : stripped;
}

// Inline status snippet for the bubble footer. Shares precedence with
// the focus-page chip (`deriveStatusChip`) — single source of truth.
// Re-uses the chip's `Failed` / `Pending` / `Rejected` / `Sent` /
// `Sent (auto)` / `Auto-rejected` labels, lowercased for the mono
// footer aesthetic, with the (scrubbed) webhook error appended when
// delivery failed.
function OutboundStatusInline({ message }: { message: MessageSummary }) {
  const spec = deriveStatusChip({
    direction: "outbound",
    hitl_status: message.hitl_status,
    webhook_status: message.webhook_status,
  });
  const color =
    spec.tone === "danger"
      ? "var(--danger-strong)"
      : spec.tone === "warn"
        ? "var(--warn-strong)"
        : spec.tone === "success"
          ? "var(--success)"
          : "var(--fg-muted)";
  const errSuffix =
    message.webhook_status === "failed" && message.webhook_error
      ? ` · ${scrubWebhookError(message.webhook_error)}`
      : "";
  return (
    <span style={{ color }}>
      {spec.label.toLowerCase()}
      {errSuffix}
    </span>
  );
}
