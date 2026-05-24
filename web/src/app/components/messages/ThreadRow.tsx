"use client";

// One row in the inbox thread list. 5 stacked rows per the design spec
// (identity + timestamp / conv id / subject / preview / state + count).
// The conv-id line is omitted for synthetic (orphan) threads.

import { Chip } from "../loft/Chip";
import { CounterpartyAvatar } from "./CounterpartyAvatar";
import { MessageDirectionIcon } from "./MessageDirectionIcon";
import { formatRelativeAge } from "../../../lib/relativeTime";
import type { Thread } from "./threading";

const STATE_CHIPS: Record<Thread["state"], { tone: "warn" | "info" | "accent" | "neutral"; label: string }> = {
  pending: { tone: "warn", label: "Pending review" },
  active: { tone: "info", label: "Active" },
  "handed-off": { tone: "accent", label: "Handed off" },
  closed: { tone: "neutral", label: "Closed" },
};

export function ThreadRow({
  thread,
  active,
  onSelect,
}: {
  thread: Thread;
  active: boolean;
  onSelect: (key: string) => void;
}) {
  const stateChip = STATE_CHIPS[thread.state];
  const orgHint = thread.counterparty.email.split("@")[1] || "";

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
      style={{
        padding: "14px 16px",
        borderBottom: "1px solid var(--border-sub)",
        background: active ? "var(--bg-elev)" : "transparent",
        boxShadow: active ? "inset 2px 0 0 var(--accent)" : "none",
        cursor: "pointer",
      }}
    >
      {/* Row 1: avatar + name + org + timestamp */}
      <div
        className="flex items-center"
        style={{ gap: 10, marginBottom: 6 }}
      >
        <CounterpartyAvatar
          email={thread.counterparty.email}
          name={thread.counterparty.name}
          size={28}
        />
        <div className="flex-1 min-w-0">
          <div className="flex items-baseline" style={{ gap: 6 }}>
            <span
              style={{
                fontSize: 13,
                fontWeight: 600,
                color: "var(--fg)",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {thread.counterparty.name}
            </span>
            {orgHint && (
              <span
                style={{
                  fontFamily: "var(--f-mono)",
                  fontSize: 10,
                  color: "var(--fg-subtle)",
                  whiteSpace: "nowrap",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                }}
              >
                · {orgHint}
              </span>
            )}
            <span className="flex-1" />
            <span
              style={{
                fontFamily: "var(--f-mono)",
                fontSize: 10,
                color: "var(--fg-subtle)",
                whiteSpace: "nowrap",
              }}
            >
              {formatRelativeAge(thread.lastMessageAt)}
            </span>
          </div>
          {thread.conversationId && (
            <div
              style={{
                fontFamily: "var(--f-mono)",
                fontSize: 10,
                color: "var(--fg-subtle)",
                letterSpacing: "0.02em",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {thread.conversationId}
            </div>
          )}
        </div>
      </div>

      {/* Row 3: subject */}
      <div
        style={{
          fontSize: 13,
          fontWeight: 600,
          color: "var(--fg)",
          marginBottom: 4,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {thread.subject}
      </div>

      {/* Row 4: direction icon + preview */}
      <div className="flex items-center" style={{ gap: 7 }}>
        <MessageDirectionIcon direction={thread.lastDirection} size={10} />
        <span
          style={{
            fontSize: 12,
            color: "var(--fg-muted)",
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
            flex: 1,
            minWidth: 0,
          }}
        >
          {thread.lastPreview}
        </span>
      </div>

      {/* Row 5: state chip + msg count */}
      <div
        className="flex items-center"
        style={{ marginTop: 8, gap: 6 }}
      >
        <Chip tone={stateChip.tone}>{stateChip.label}</Chip>
        <span
          style={{
            fontFamily: "var(--f-mono)",
            fontSize: 10,
            color: "var(--fg-subtle)",
          }}
        >
          {thread.msgCount} {thread.msgCount === 1 ? "msg" : "msgs"}
        </span>
      </div>
    </div>
  );
}
