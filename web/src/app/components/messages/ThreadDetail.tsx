"use client";

// Right column of the dashboard inbox. Renders the selected thread's
// header (conv chip + state + meta + subject + between-line) and the
// chat-log of bubbles below it. Newest-at-bottom — the spec uses
// reverse-chronological in the artboard, but for an email-client
// reading flow the conversation reads oldest→newest top→bottom.

import { Chip } from "../loft/Chip";
import { Dot } from "../loft/Dot";
import { CounterpartyAvatar } from "./CounterpartyAvatar";
import { ThreadBubble } from "./ThreadBubble";
import { PendingCallout } from "./PendingCallout";
import { formatRelativeAge } from "../../../lib/relativeTime";
import type { Thread } from "./threading";

const STATE_CHIP: Record<Thread["state"], { tone: "warn" | "info" | "accent" | "neutral"; label: string; dot: boolean }> = {
  pending: { tone: "warn", label: "Pending review", dot: true },
  active: { tone: "info", label: "Active", dot: false },
  "handed-off": { tone: "accent", label: "Handed off", dot: false },
  closed: { tone: "neutral", label: "Closed", dot: false },
};

export function ThreadDetail({
  thread,
  agentEmail,
  onOpenMessage,
  onOpenHeaders,
}: {
  thread: Thread | null;
  agentEmail: string;
  onOpenMessage: (messageId: string) => void;
  onOpenHeaders: (messageId: string) => void;
}) {
  if (!thread) {
    return (
      <div
        data-testid="thread-detail-empty"
        className="flex flex-col items-center justify-center text-center"
        style={{
          background: "var(--bg)",
          padding: "0 24px",
          color: "var(--fg-muted)",
          fontSize: 13,
        }}
      >
        Select a conversation from the inbox.
      </div>
    );
  }

  const stateChip = STATE_CHIP[thread.state];
  const pendingDraft = thread.messages.find(
    (m) => m.hitl_status === "pending_approval",
  );

  return (
    <div
      data-testid="thread-detail"
      className="flex flex-col min-w-0 min-h-0 overflow-hidden"
      style={{ background: "var(--bg)" }}
    >
      {/* Header */}
      <div
        style={{
          padding: "18px 28px",
          borderBottom: "1px solid var(--border)",
          background: "var(--bg-panel)",
        }}
      >
        <div className="flex items-center" style={{ gap: 8, marginBottom: 10 }}>
          {thread.conversationId && (
            <code
              style={{
                fontFamily: "var(--f-mono)",
                fontSize: 11,
                color: "var(--fg-muted)",
                background: "var(--bg-elev)",
                padding: "1px 6px",
                borderRadius: 4,
                border: "1px solid var(--border-sub)",
              }}
            >
              {thread.conversationId}
            </code>
          )}
          <Chip tone={stateChip.tone}>
            {stateChip.dot && <Dot tone={stateChip.tone === "warn" ? "warn" : "neutral"} />}
            {stateChip.label}
          </Chip>
          <span className="flex-1" />
          <span
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              color: "var(--fg-subtle)",
            }}
          >
            {thread.msgCount} {thread.msgCount === 1 ? "message" : "messages"} ·
            started {formatRelativeAge(thread.startedAt)}
          </span>
        </div>
        <h2
          style={{
            fontFamily: "var(--f-ui)",
            fontSize: 22,
            fontWeight: 700,
            letterSpacing: "-0.012em",
            color: "var(--fg)",
            margin: 0,
          }}
        >
          {thread.subject}
        </h2>
        <div
          className="flex items-center"
          style={{
            marginTop: 8,
            gap: 10,
            fontFamily: "var(--f-mono)",
            fontSize: 12,
            color: "var(--fg-muted)",
          }}
        >
          <CounterpartyAvatar
            email={thread.counterparty.email}
            name={thread.counterparty.name}
            size={20}
          />
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>between</span>{" "}
            {agentEmail}{" "}
            <span style={{ color: "var(--fg-subtle)" }}>↔</span>{" "}
            {thread.counterparty.email}
          </span>
        </div>
      </div>

      {/* Body: chat log */}
      <div
        className="flex-1 overflow-y-auto"
        style={{ padding: "24px 28px 28px" }}
      >
        {thread.messages.map((m) => (
          <ThreadBubble
            key={m.message_id}
            message={m}
            counterparty={thread.counterparty}
            agentEmail={agentEmail}
            onOpen={onOpenMessage}
            onOpenHeaders={onOpenHeaders}
          />
        ))}

        {pendingDraft && (
          <PendingCallout
            draftedBy="agent"
            expiresInLabel={null}
            onReview={() => onOpenMessage(pendingDraft.message_id)}
          />
        )}
      </div>
    </div>
  );
}
