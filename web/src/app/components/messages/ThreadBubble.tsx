"use client";

// One message inside the conversation view, laid out like a Gmail
// message (flat vertical list — not chat bubbles): sender + absolute
// time on the right, a collapsible Details row (Subject / To / Cc /
// Reply-To), then the body rendered INLINE. The body is fetched
// per-message from the detail endpoint (the list/summary payload carries
// no body); recipients/subject come from the summary row itself.

import { useState } from "react";
import useSWR from "swr";
import { Chip, Dot } from "@e2a/ui";
import { CounterpartyAvatar } from "./CounterpartyAvatar";
import { EmailHtmlBody } from "./EmailHtmlBody";
import { getMessageDetail } from "../onboarding/api";
import {
  invalidateAgentMessages,
  invalidateAgentUnread,
} from "../../../lib/swrKeys";
import type { MessageSummary } from "../types";
import type { Counterparty } from "./threading";

// Absolute, human time for the message header (e.g. "Jun 21, 8:07 PM").
// title carries the full locale string for hover.
function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}

// Decode the base64 raw RFC-822 bytes and return the body (everything
// after the first blank line). Honest fallback for inbound rows whose
// parsed body_text isn't on the wire; multipart bodies render with
// boundary markers visible (a tracked backend follow-up).
function decodeRawBody(b64?: string): string {
  if (!b64) return "";
  try {
    const bin = atob(b64);
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
    const text = new TextDecoder().decode(bytes);
    const idx = text.search(/\r?\n\r?\n/);
    return idx >= 0 ? text.slice(idx).replace(/^\r?\n\r?\n/, "") : text;
  } catch {
    return "";
  }
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex" style={{ gap: 8 }}>
      <span style={{ color: "var(--fg-subtle)", width: 64, flexShrink: 0 }}>{label}</span>
      <span style={{ color: "var(--fg-muted)", wordBreak: "break-word" }}>{value}</span>
    </div>
  );
}

export function ThreadBubble({
  message,
  counterparty,
  agentEmail,
}: {
  message: MessageSummary;
  counterparty: Counterparty;
  agentEmail: string;
}) {
  const isInbound = message.direction === "inbound";
  const pending = message.review_status === "pending_review";
  const [showDetails, setShowDetails] = useState(false);

  // Opening a message body fetches its detail, which flips inbox_status
  // unread → read on the backend (GetMessageWithContent). Capture whether
  // THIS row was an unread inbound message at fetch time so we can refresh
  // the stale caches once the read-flip has happened.
  const wasUnreadInbound = isInbound && message.read_status === "unread";

  // Fetch this message's body. Keyed per (agent, id, direction) so each
  // message caches independently and shares with the focus page's cache.
  const { data: detail, isLoading } = useSWR(
    ["thread-msg-body", agentEmail, message.message_id, message.direction] as const,
    () => getMessageDetail(agentEmail, message.message_id, message.direction),
    {
      // After the read-flip, the thread list (bold rows) and the Inboxes
      // unread badge both hold stale unread state. Revalidate them so the
      // row un-bolds and the badge count drops without a hard refresh.
      onSuccess: () => {
        if (wasUnreadInbound) {
          invalidateAgentMessages(agentEmail);
          invalidateAgentUnread(agentEmail);
        }
      },
    },
  );

  // Resolve both representations: a rich HTML body (rendered sanitized in a
  // sandboxed iframe) when the message has one, else the plain text. Prefer the
  // backend-parsed bodies (QP/base64 decoded); the raw decode is last resort.
  let textBody = "";
  let htmlBody = "";
  if (detail) {
    if (detail.direction === "outbound") {
      textBody = detail.data.body_text ?? "";
      htmlBody = detail.data.body_html ?? "";
    } else {
      htmlBody = detail.data.parsed?.html ?? "";
      textBody =
        detail.data.parsed?.text ||
        detail.data.body?.text ||
        decodeRawBody(detail.data.raw_message) ||
        "";
    }
  }
  const showBody = htmlBody.trim() !== "" || textBody.trim() !== "";

  const senderName = isInbound ? counterparty.name : "Inbox";
  const senderEmail = isInbound ? counterparty.email : agentEmail;
  const toList = (message.to ?? []).join(", ");

  return (
    <div
      data-testid="thread-bubble"
      data-message-id={message.message_id}
      className="flex"
      style={{ gap: 12, marginBottom: 20, alignItems: "flex-start" }}
    >
      {/* Avatar — counterparty face for inbound, e2a tile for outbound. */}
      <div style={{ flexShrink: 0, paddingTop: 2 }}>
        {isInbound ? (
          <CounterpartyAvatar email={counterparty.email} name={counterparty.name} size={32} />
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

      <div style={{ flex: 1, minWidth: 0 }}>
        {/* Header: sender ……… time */}
        <div className="flex items-baseline" style={{ gap: 8 }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: "var(--fg)", whiteSpace: "nowrap" }}>
            {senderName}
          </span>
          <span
            className="min-w-0"
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              color: "var(--fg-subtle)",
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {senderEmail}
          </span>
          {pending && (
            <Chip tone="warn">
              <Dot tone="warn" /> Pending
            </Chip>
          )}
          <span className="flex-1" />
          <span
            title={new Date(message.created_at).toLocaleString()}
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              color: "var(--fg-subtle)",
              whiteSpace: "nowrap",
              flexShrink: 0,
            }}
          >
            {fmtTime(message.created_at)}
          </span>
        </div>

        {/* Recipients line + Details toggle */}
        <div
          className="flex items-center"
          style={{ gap: 8, marginTop: 2, marginBottom: 8, fontFamily: "var(--f-mono)", fontSize: 11 }}
        >
          <span style={{ color: "var(--fg-subtle)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", minWidth: 0 }}>
            to {toList || message.recipient}
          </span>
          <button
            type="button"
            onClick={() => setShowDetails((v) => !v)}
            aria-expanded={showDetails}
            style={{
              color: "var(--accent-strong)",
              background: "transparent",
              border: "none",
              padding: 0,
              cursor: "pointer",
              fontFamily: "inherit",
              fontSize: "inherit",
              flexShrink: 0,
            }}
          >
            {showDetails ? "Hide details ▴" : "Details ▾"}
          </button>
        </div>

        {/* Details panel */}
        {showDetails && (
          <div
            className="mb-3"
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              lineHeight: 1.7,
              padding: "8px 10px",
              background: "var(--bg-elev)",
              border: "1px solid var(--border-sub)",
              borderRadius: "var(--r-sm)",
            }}
          >
            <MetaRow label="Subject" value={message.subject || "(no subject)"} />
            <MetaRow label="From" value={message.from} />
            <MetaRow label="To" value={toList || message.recipient || "—"} />
            {message.cc && message.cc.length > 0 && (
              <MetaRow label="Cc" value={message.cc.join(", ")} />
            )}
            {message.reply_to && message.reply_to.length > 0 && (
              <MetaRow label="Reply-To" value={message.reply_to.join(", ")} />
            )}
          </div>
        )}

        {/* Inline body */}
        <div
          style={{
            background: isInbound ? "var(--bg-panel)" : "var(--accent-soft)",
            border: `1px solid ${isInbound ? "var(--border)" : "var(--accent)"}`,
            borderRadius: "var(--r-lg)",
            padding: "12px 14px",
            fontSize: 13.5,
            lineHeight: 1.6,
            color: "var(--fg)",
            wordBreak: "break-word",
          }}
        >
          {!showBody ? (
            isLoading ? (
              <span style={{ color: "var(--fg-subtle)" }}>Loading…</span>
            ) : (
              <span style={{ fontStyle: "italic", color: "var(--fg-muted)" }}>
                {message.subject || "(no content)"}
              </span>
            )
          ) : htmlBody.trim() !== "" ? (
            <EmailHtmlBody html={htmlBody} />
          ) : (
            <div style={{ whiteSpace: "pre-wrap" }}>{textBody}</div>
          )}
        </div>
      </div>
    </div>
  );
}
