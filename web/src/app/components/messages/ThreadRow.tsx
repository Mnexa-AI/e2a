"use client";

// One row in the Gmail-style inbox list: a single dense line —
// sender · subject — preview ……… [pending] [count] time. Unread threads
// render bold. Clicking opens the conversation full-width.

import { CounterpartyAvatar } from "./CounterpartyAvatar";
import { formatRelativeAge } from "../../../lib/relativeTime";
import type { Thread } from "./threading";

export function ThreadRow({
  thread,
  active,
  onSelect,
}: {
  thread: Thread;
  active: boolean;
  onSelect: (key: string) => void;
}) {
  // Unread = any inbound message still marked unread. v1 carries inbound
  // read state in read_status (delivery_status is outbound-only). Drives
  // Gmail's bold row.
  const unread = thread.messages.some(
    (m) => m.direction === "inbound" && m.read_status === "unread",
  );
  const pending = thread.state === "pending";
  const fw = unread ? 600 : 400;

  return (
    <div
      data-testid="thread-row"
      data-thread-key={thread.key}
      data-selected={active ? "true" : "false"}
      role="button"
      tabIndex={0}
      onClick={() => onSelect(thread.key)}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onSelect(thread.key);
        }
      }}
      className="flex items-center hover:bg-[var(--bg-elev)] transition"
      style={{
        gap: 12,
        padding: "10px 18px",
        borderBottom: "1px solid var(--border-sub)",
        background: active ? "var(--bg-elev)" : unread ? "var(--bg-panel)" : "transparent",
        boxShadow: active ? "inset 2px 0 0 var(--accent)" : "none",
        cursor: "pointer",
      }}
    >
      <CounterpartyAvatar
        email={thread.counterparty.email}
        name={thread.counterparty.name}
        size={26}
      />

      {/* Sender — fixed-ish column so subjects line up like Gmail. */}
      <span
        style={{
          fontSize: 13,
          fontWeight: fw,
          color: "var(--fg)",
          width: 170,
          flexShrink: 0,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {thread.counterparty.name}
        {thread.msgCount > 1 && (
          <span style={{ color: "var(--fg-subtle)", fontWeight: 400 }}>
            {" "}
            {thread.msgCount}
          </span>
        )}
      </span>

      {/* Subject — preview, single line, takes the remaining width. */}
      <span className="flex-1 min-w-0" style={{ fontSize: 13, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
        <span style={{ color: "var(--fg)", fontWeight: fw }}>{thread.subject}</span>
        {thread.lastPreview && thread.lastPreview !== thread.subject && (
          <span style={{ color: "var(--fg-subtle)" }}> — {thread.lastPreview}</span>
        )}
      </span>

      {/* Right meta: pending pill (needs attention) + timestamp. */}
      {pending && (
        <span
          className="shrink-0"
          style={{
            fontSize: 10,
            fontWeight: 600,
            color: "var(--warn-strong)",
            background: "var(--warn-bg)",
            borderRadius: 999,
            padding: "1px 8px",
            whiteSpace: "nowrap",
          }}
        >
          Pending
        </span>
      )}
      <span
        className="shrink-0"
        style={{
          fontFamily: "var(--f-mono)",
          fontSize: 11,
          color: unread ? "var(--fg)" : "var(--fg-subtle)",
          fontWeight: unread ? 600 : 400,
          whiteSpace: "nowrap",
          minWidth: 52,
          textAlign: "right",
        }}
      >
        {formatRelativeAge(thread.lastMessageAt)}
      </span>
    </div>
  );
}
