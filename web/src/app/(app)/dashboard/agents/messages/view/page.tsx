"use client";

// Single-message focus view (Screen 2 of agent_messages_v2).
//
// Loads via try-outbound-then-inbound fallback because the e2a backend
// splits message detail endpoints by direction:
//   - GET /api/v1/messages/{id}        → outbound (PendingMessageDetail)
//   - GET /api/v1/agents/{email}/messages/{id} → inbound  (InboundMessageDetail)
//
// A unified `GET /api/v1/messages/{id}` that returns a discriminated
// union is a tracked follow-up; until then we do two requests in the
// worst case.

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  approvePendingMessage,
  getInboundMessage,
  getPendingMessage,
  rejectPendingMessage,
} from "../../../../../components/onboarding/api";
import type {
  InboundMessageDetail,
  PendingMessageDetail,
} from "../../../../../components/types";
import { Chip } from "../../../../../components/loft/Chip";
import { Dot } from "../../../../../components/loft/Dot";
import { Eyebrow } from "../../../../../components/loft/Eyebrow";
import { InkConsole, type InkLine } from "../../../../../components/loft/InkConsole";
import { Collapsible } from "../../../../../components/messages/Collapsible";
import { MessageDirectionIcon } from "../../../../../components/messages/MessageDirectionIcon";
import {
  MessageLifecycleTimeline,
  deriveLifecycleSteps,
} from "../../../../../components/messages/MessageLifecycleTimeline";

type LoadedMessage =
  | { direction: "outbound"; data: PendingMessageDetail }
  | { direction: "inbound"; data: InboundMessageDetail };

function formatRelativeAge(iso: string | null | undefined): string {
  if (!iso) return "—";
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 0 || isNaN(diff)) return "—";
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return "just now";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}

function formatExpiresIn(iso: string | undefined): string | null {
  if (!iso) return null;
  const diff = new Date(iso).getTime() - Date.now();
  if (diff <= 0) return "expired";
  const min = Math.floor(diff / 60_000);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  return `${hr}h ${min % 60}m`;
}

// Decode base64 `raw_message` and pull out the body part — everything
// after the first blank line is the body in RFC 5322 framing. Honest
// fallback only: multipart / MIME-encoded messages will render with
// boundary markers visible. Backend-side body_text parsing for inbound
// is tracked as a follow-up.
function decodeInboundBody(rawBase64: string | undefined): string {
  if (!rawBase64) return "";
  try {
    const decoded = typeof atob === "function"
      ? atob(rawBase64)
      : Buffer.from(rawBase64, "base64").toString("utf8");
    const splitIdx = decoded.indexOf("\r\n\r\n");
    if (splitIdx === -1) {
      const lfIdx = decoded.indexOf("\n\n");
      if (lfIdx === -1) return decoded;
      return decoded.slice(lfIdx + 2);
    }
    return decoded.slice(splitIdx + 4);
  } catch {
    return "";
  }
}

export default function AgentMessageFocusPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const email = searchParams.get("email") ?? "";
  const id = searchParams.get("id") ?? "";
  const initialHeadersOpen = searchParams.get("headers") === "1";

  const [msg, setMsg] = useState<LoadedMessage | null>(null);
  const [error, setError] = useState("");
  const [editingDraft, setEditingDraft] = useState(false);
  const [draftBody, setDraftBody] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [showRejectPrompt, setShowRejectPrompt] = useState(false);
  const [rejectReason, setRejectReason] = useState("");

  // Try outbound first (focus is most often a Pending draft from the
  // inbox callout); fall back to inbound on 404.
  useEffect(() => {
    if (!email || !id) return;
    let cancelled = false;
    (async () => {
      try {
        const out = await getPendingMessage(id);
        if (cancelled) return;
        setMsg({ direction: "outbound", data: out });
        setDraftBody(out.body_text ?? "");
      } catch (outErr) {
        // Outbound 404 → try inbound. Any other error from outbound is
        // still surfaced as a fallback attempt — getInboundMessage will
        // return its own error if the id isn't an inbound either.
        try {
          const inb = await getInboundMessage(email, id);
          if (cancelled) return;
          setMsg({ direction: "inbound", data: inb });
        } catch (inbErr) {
          if (cancelled) return;
          setError(
            inbErr instanceof Error
              ? inbErr.message
              : outErr instanceof Error
                ? outErr.message
                : "Message not found",
          );
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [email, id]);

  const isPending = msg?.direction === "outbound" && msg.data.status === "pending_approval";

  const inboxLink = `/dashboard/agents/messages?email=${encodeURIComponent(email)}`;
  const convLink = msg?.direction === "outbound"
    ? `${inboxLink}#${msg.data.conversation_id || `msg:${msg.data.id}`}`
    : msg?.direction === "inbound"
      ? `${inboxLink}#${msg.data.conversation_id || `msg:${msg.data.message_id}`}`
      : inboxLink;

  const onApprove = useCallback(async () => {
    if (!msg || msg.direction !== "outbound") return;
    setSubmitting(true);
    try {
      const overrides = editingDraft && draftBody !== (msg.data.body_text ?? "")
        ? { body_text: draftBody }
        : {};
      await approvePendingMessage(msg.data.id, overrides);
      router.push(convLink);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to approve");
      setSubmitting(false);
    }
  }, [msg, editingDraft, draftBody, router, convLink]);

  const onReject = useCallback(async () => {
    if (!msg || msg.direction !== "outbound") return;
    setSubmitting(true);
    try {
      await rejectPendingMessage(msg.data.id, rejectReason || "rejected by reviewer");
      router.push(convLink);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to reject");
      setSubmitting(false);
    }
  }, [msg, rejectReason, router, convLink]);

  // ⌘↵ on the focus page approves a pending message.
  useEffect(() => {
    if (!isPending) return;
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        onApprove();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [isPending, onApprove]);

  if (!email || !id) {
    return (
      <div className="m-6 p-4 text-[13px]" style={{ color: "var(--danger-strong)" }}>
        Missing ?email= or ?id= query parameter
      </div>
    );
  }
  if (error) {
    return (
      <div
        className="m-6 p-4 text-[13px]"
        style={{
          background: "var(--danger-bg)",
          border: "1px solid var(--danger-bg)",
          color: "var(--danger-strong)",
          borderRadius: "var(--r-md)",
        }}
      >
        {error}
      </div>
    );
  }
  if (!msg) {
    return (
      <div className="px-7 py-8 text-[13px]" style={{ color: "var(--fg-muted)" }}>
        Loading message…
      </div>
    );
  }

  // Direction-specific shaping for the title row + identity line.
  const direction = msg.direction;
  const directionLabel = direction === "outbound"
    ? (msg.data.type === "reply" ? "outbound · reply" : "outbound")
    : "inbound";
  const subject = direction === "outbound" ? msg.data.subject : msg.data.subject;
  const from = direction === "outbound" ? email : msg.data.from;
  const to = direction === "outbound"
    ? (msg.data.to?.[0] ?? "")
    : email;
  const convId = direction === "outbound" ? msg.data.conversation_id : msg.data.conversation_id;
  const createdAt = direction === "outbound" ? msg.data.created_at : msg.data.created_at;
  const messageId = direction === "outbound" ? msg.data.id : msg.data.message_id;
  const inReplyTo = direction === "outbound" && msg.data.inbound
    ? { sender: msg.data.inbound.sender, createdAt: msg.data.inbound.created_at, parentId: msg.data.email_message_id }
    : null;
  const expiresIn = direction === "outbound" ? formatExpiresIn(msg.data.approval_expires_at) : null;

  return (
    <div
      data-testid="message-focus"
      data-direction={direction}
      data-status={direction === "outbound" ? msg.data.status : msg.data.status}
      className="flex-1 min-h-0 overflow-y-auto"
      style={{ padding: "24px 28px 32px" }}
    >
      {/* Title block */}
      <div className="mb-5">
        <div className="flex items-center flex-wrap gap-2.5 mb-3">
          <Link
            href={convLink}
            className="inline-flex items-center gap-1"
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              padding: "4px 9px",
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
              textDecoration: "none",
            }}
          >
            ← thread
          </Link>
          <MessageDirectionIcon direction={direction} />
          <span
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              fontWeight: 600,
              color: direction === "inbound" ? "var(--success)" : "var(--info-strong)",
              letterSpacing: "0.06em",
              textTransform: "uppercase",
            }}
          >
            {directionLabel}
          </span>
          {isPending && (
            <Chip tone="warn">
              <Dot tone="warn" /> Pending review{expiresIn ? ` · ${expiresIn}` : ""}
            </Chip>
          )}
          {direction === "outbound" && msg.data.status === "sent" && (
            <Chip tone="success">Sent</Chip>
          )}
          {direction === "outbound" && (msg.data.status === "rejected" || msg.data.status === "expired_rejected") && (
            <Chip tone="danger">{msg.data.status === "expired_rejected" ? "Auto-rejected" : "Rejected"}</Chip>
          )}
          {direction === "outbound" && msg.data.status === "expired_approved" && (
            <Chip tone="success">Sent (auto)</Chip>
          )}
          <span className="flex-1" />
          {direction === "inbound" && (
            <Chip tone="success">
              <Dot tone="success" /> Auth verified
            </Chip>
          )}
          <code
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 12,
              color: "var(--fg)",
              background: "var(--bg-elev)",
              padding: "3px 9px",
              borderRadius: "var(--r-sm)",
              border: "1px solid var(--border-sub)",
            }}
          >
            {messageId}
          </code>
        </div>
        <h1
          style={{
            fontFamily: "var(--f-ui)",
            fontSize: 26,
            fontWeight: 700,
            letterSpacing: "-0.012em",
            color: "var(--fg)",
            margin: "6px 0 10px",
          }}
        >
          {subject || "(no subject)"}
        </h1>
        <div
          className="flex items-center flex-wrap"
          style={{
            gap: 14,
            fontFamily: "var(--f-mono)",
            fontSize: 12,
            color: "var(--fg-muted)",
          }}
        >
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>from</span>{" "}
            <span style={{ color: "var(--fg)" }}>{from}</span>
          </span>
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>to</span>{" "}
            <span style={{ color: "var(--fg)" }}>{to}</span>
          </span>
          {convId && (
            <span>
              <span style={{ color: "var(--fg-subtle)" }}>in</span>{" "}
              <span style={{ color: "var(--fg)" }}>{convId}</span>
            </span>
          )}
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>queued</span>{" "}
            {formatRelativeAge(createdAt)}
          </span>
          {inReplyTo && (
            <>
              <span style={{ color: "var(--fg-subtle)" }}>·</span>
              <span>
                <span style={{ color: "var(--fg-subtle)" }}>in reply to</span>{" "}
                <span style={{ color: "var(--fg)" }}>{inReplyTo.parentId || "(parent)"}</span>{" "}
                <span style={{ color: "var(--fg-subtle)" }}>
                  ({inReplyTo.sender.split("@")[0]}, {formatRelativeAge(inReplyTo.createdAt)})
                </span>
              </span>
            </>
          )}
        </div>
      </div>

      {/* Two columns: body + headers (left), action + lifecycle (right) */}
      <div className="grid gap-5 md:grid-cols-[1fr_340px]">
        <div className="flex flex-col gap-4">
          <BodyCard
            msg={msg}
            editingDraft={editingDraft}
            draftBody={draftBody}
            onChangeDraft={setDraftBody}
            onStartEdit={() => setEditingDraft(true)}
            onCancelEdit={() => {
              setEditingDraft(false);
              setDraftBody(msg.direction === "outbound" ? (msg.data.body_text ?? "") : "");
            }}
          />
          <HeadersCollapsible msg={msg} defaultOpen={initialHeadersOpen} />
        </div>
        <aside className="flex flex-col gap-4">
          {isPending && msg.direction === "outbound" && (
            <ActionCard
              expiresIn={expiresIn}
              submitting={submitting}
              showRejectPrompt={showRejectPrompt}
              rejectReason={rejectReason}
              onChangeReason={setRejectReason}
              onApprove={onApprove}
              onStartReject={() => setShowRejectPrompt(true)}
              onCancelReject={() => {
                setShowRejectPrompt(false);
                setRejectReason("");
              }}
              onConfirmReject={onReject}
            />
          )}
          <LifecycleSection msg={msg} />
          {isPending && msg.direction === "outbound" && (
            <CliHint messageId={msg.data.id} />
          )}
        </aside>
      </div>
    </div>
  );
}

function BodyCard({
  msg,
  editingDraft,
  draftBody,
  onChangeDraft,
  onStartEdit,
  onCancelEdit,
}: {
  msg: LoadedMessage;
  editingDraft: boolean;
  draftBody: string;
  onChangeDraft: (v: string) => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
}) {
  const bodyText = useMemo(() => {
    if (msg.direction === "outbound") return msg.data.body_text ?? "";
    return decodeInboundBody(msg.data.raw_message);
  }, [msg]);

  const isOutbound = msg.direction === "outbound";
  const isTerminal =
    isOutbound &&
    msg.data.status !== "pending_approval" &&
    !msg.data.body_text;
  const wordCount = bodyText.trim() ? bodyText.trim().split(/\s+/).length : 0;
  const bytes = bodyText.length;

  return (
    <section
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        overflow: "hidden",
      }}
    >
      {editingDraft && isOutbound ? (
        <textarea
          value={draftBody}
          onChange={(e) => onChangeDraft(e.target.value)}
          rows={Math.max(8, draftBody.split("\n").length + 1)}
          className="w-full"
          style={{
            padding: "26px 28px 22px",
            fontSize: 14,
            lineHeight: 1.65,
            color: "var(--fg)",
            background: "var(--bg-panel)",
            border: "none",
            outline: "none",
            fontFamily: "var(--f-ui)",
            resize: "vertical",
          }}
        />
      ) : (
        <div
          data-testid="body-card"
          style={{
            padding: "26px 28px 22px",
            fontSize: 14,
            lineHeight: 1.65,
            color: "var(--fg)",
            whiteSpace: "pre-wrap",
          }}
        >
          {isTerminal ? (
            <span style={{ color: "var(--fg-muted)", fontStyle: "italic" }}>
              Body no longer available (scrubbed after delivery).
            </span>
          ) : (
            bodyText || (
              <span style={{ color: "var(--fg-muted)", fontStyle: "italic" }}>
                (body unavailable — backend parsed-body support for inbound is a tracked follow-up)
              </span>
            )
          )}
        </div>
      )}
      {/* Footer strip */}
      <div
        className="flex gap-4"
        style={{
          padding: "10px 18px",
          borderTop: "1px solid var(--border-sub)",
          background: "var(--bg-elev)",
          fontFamily: "var(--f-mono)",
          fontSize: 11,
          color: "var(--fg-subtle)",
        }}
      >
        <span>
          {wordCount} {wordCount === 1 ? "word" : "words"} ·{" "}
          {bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(1)} KB`}
        </span>
        <span className="flex-1" />
        {editingDraft && isOutbound ? (
          <>
            <button
              type="button"
              onClick={onCancelEdit}
              style={{
                color: "var(--fg-muted)",
                background: "transparent",
                border: "none",
                padding: 0,
                cursor: "pointer",
                fontFamily: "inherit",
                fontSize: "inherit",
              }}
            >
              cancel edit
            </button>
            <span
              title="Drafts are sent on Approve — there's no save-without-send endpoint yet"
              style={{ color: "var(--fg-subtle)", cursor: "help" }}
            >
              save draft (n/a)
            </span>
          </>
        ) : isOutbound && msg.data.status === "pending_approval" ? (
          <>
            <button
              type="button"
              onClick={onStartEdit}
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
              edit draft
            </button>
          </>
        ) : (
          <span style={{ color: "var(--fg-subtle)" }}>raw .eml</span>
        )}
      </div>
    </section>
  );
}

function HeadersCollapsible({ msg, defaultOpen }: { msg: LoadedMessage; defaultOpen: boolean }) {
  const lines: InkLine[] = msg.direction === "inbound"
    ? buildInboundHeaderLines(msg.data)
    : buildOutboundHeaderLines(msg.data);

  return (
    <Collapsible
      label="Full headers"
      meta={`rfc-5322 · ${msg.direction === "inbound" ? "received" : "signed"} · ${lines.length} lines`}
      defaultOpen={defaultOpen}
    >
      <div style={{ padding: "14px 18px 18px" }}>
        <InkConsole
          lang={msg.direction === "inbound" ? "rfc-5322" : "rfc-5322 · signed"}
          copy
          lines={lines}
        />
      </div>
    </Collapsible>
  );
}

function buildInboundHeaderLines(d: InboundMessageDetail): InkLine[] {
  const lines: InkLine[] = [
    { c: "comment", text: "# captured at receive-time" },
    {
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>From:</span>{" "}
          <span style={{ color: "var(--spectral)" }}>{d.from}</span>
        </span>
      ),
    },
    {
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>To:</span> {d.recipient}
        </span>
      ),
    },
    {
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>Subject:</span>{" "}
          {d.subject || "(no subject)"}
        </span>
      ),
    },
  ];
  // Surface every X-E2A-Auth-* + Received-SPF + Authentication-Results
  // header we got. The auth_headers map preserves the wire order.
  for (const [k, v] of Object.entries(d.auth_headers ?? {})) {
    lines.push({
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>{k}:</span>{" "}
          {v === "pass" || v === "verified" || v === "true" ? (
            <span style={{ color: "var(--machine)" }}>{v}</span>
          ) : v === "fail" || v === "false" ? (
            <span style={{ color: "var(--danger)" }}>{v}</span>
          ) : (
            <span style={{ color: "var(--spectral)" }}>{v}</span>
          )}
        </span>
      ),
    });
  }
  return lines;
}

function buildOutboundHeaderLines(d: PendingMessageDetail): InkLine[] {
  const to = d.to?.[0] ?? "";
  const lines: InkLine[] = [
    { c: "comment", text: "# signed at send-time" },
    {
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>To:</span>{" "}
          <span style={{ color: "var(--spectral)" }}>{to}</span>
        </span>
      ),
    },
    {
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>Subject:</span>{" "}
          {d.subject || "(no subject)"}
        </span>
      ),
    },
  ];
  if (d.conversation_id) {
    lines.push({
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>X-E2A-Conversation-Id:</span>{" "}
          {d.conversation_id}
        </span>
      ),
    });
  }
  if (d.provider_message_id) {
    lines.push({
      node: (
        <span>
          <span style={{ color: "var(--ink-fg-muted)" }}>Message-ID:</span>{" "}
          <span style={{ color: "var(--spectral)" }}>{d.provider_message_id}</span>
        </span>
      ),
    });
  }
  if (d.inbound?.auth_headers) {
    lines.push({ c: "comment", text: "# parent inbound auth" });
    for (const [k, v] of Object.entries(d.inbound.auth_headers)) {
      lines.push({
        node: (
          <span>
            <span style={{ color: "var(--ink-fg-muted)" }}>{k}:</span>{" "}
            <span style={{ color: "var(--spectral)" }}>{v}</span>
          </span>
        ),
      });
    }
  }
  return lines;
}

function ActionCard({
  expiresIn,
  submitting,
  showRejectPrompt,
  rejectReason,
  onChangeReason,
  onApprove,
  onStartReject,
  onCancelReject,
  onConfirmReject,
}: {
  expiresIn: string | null;
  submitting: boolean;
  showRejectPrompt: boolean;
  rejectReason: string;
  onChangeReason: (v: string) => void;
  onApprove: () => void;
  onStartReject: () => void;
  onCancelReject: () => void;
  onConfirmReject: () => void;
}) {
  return (
    <section
      data-testid="action-card"
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--accent)",
        borderRadius: "var(--r-lg)",
        padding: "16px 18px",
      }}
    >
      <Eyebrow>Awaiting your approval</Eyebrow>
      <div
        style={{
          fontFamily: "var(--f-mono)",
          fontSize: 11,
          color: "var(--warn-strong)",
          margin: "10px 0 14px",
          letterSpacing: "0.02em",
        }}
      >
        {expiresIn ? `expires in ${expiresIn} · ` : ""}default:{" "}
        <span style={{ color: "var(--fg)" }}>auto-reject</span>
      </div>
      {showRejectPrompt ? (
        <div className="flex flex-col gap-2">
          <textarea
            placeholder="Reason for rejection (optional but helpful for the agent)"
            value={rejectReason}
            onChange={(e) => onChangeReason(e.target.value)}
            rows={3}
            style={{
              padding: "8px 10px",
              fontFamily: "var(--f-ui)",
              fontSize: 13,
              border: "1px solid var(--border-strong)",
              borderRadius: "var(--r-sm)",
              outline: "none",
              resize: "vertical",
              color: "var(--fg)",
              background: "var(--bg-panel)",
            }}
          />
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onCancelReject}
              disabled={submitting}
              style={{
                flex: 1,
                fontFamily: "var(--f-ui)",
                fontSize: 13,
                padding: "8px 12px",
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
                color: "var(--fg)",
                cursor: submitting ? "default" : "pointer",
              }}
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={onConfirmReject}
              disabled={submitting}
              style={{
                flex: 1,
                fontFamily: "var(--f-ui)",
                fontSize: 13,
                padding: "8px 12px",
                background: "var(--bg-panel)",
                border: "1px solid var(--danger-bg)",
                borderRadius: "var(--r-md)",
                color: "var(--danger-strong)",
                cursor: submitting ? "default" : "pointer",
              }}
            >
              {submitting ? "rejecting…" : "Confirm reject"}
            </button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <button
            type="button"
            onClick={onApprove}
            disabled={submitting}
            className="flex items-center justify-between"
            style={{
              fontFamily: "var(--f-ui)",
              fontSize: 13,
              fontWeight: 500,
              padding: "10px 14px",
              background: "var(--accent-fill)",
              color: "#fff",
              border: "none",
              borderRadius: "var(--r-md)",
              cursor: submitting ? "default" : "pointer",
            }}
          >
            <span>{submitting ? "approving…" : "Approve & send"}</span>
            <span
              style={{
                fontFamily: "var(--f-mono)",
                fontSize: 10,
                fontWeight: 600,
                background: "rgba(255,255,255,.2)",
                padding: "1px 5px",
                borderRadius: 3,
              }}
            >
              ⌘↵
            </span>
          </button>
          <button
            type="button"
            onClick={onStartReject}
            disabled={submitting}
            style={{
              fontFamily: "var(--f-ui)",
              fontSize: 13,
              padding: "8px 12px",
              background: "var(--bg-panel)",
              border: "1px solid var(--danger-bg)",
              borderRadius: "var(--r-md)",
              color: "var(--danger-strong)",
              cursor: submitting ? "default" : "pointer",
            }}
          >
            Reject
          </button>
        </div>
      )}
    </section>
  );
}

function LifecycleSection({ msg }: { msg: LoadedMessage }) {
  // Inbound messages don't go through the HITL lifecycle — show a small
  // "received" summary instead of the timeline.
  if (msg.direction === "inbound") {
    return (
      <section
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
          padding: "14px 18px",
        }}
      >
        <Eyebrow>Lifecycle</Eyebrow>
        <div
          style={{
            marginTop: 10,
            fontFamily: "var(--f-mono)",
            fontSize: 11,
            color: "var(--fg-muted)",
            lineHeight: 1.7,
          }}
        >
          received {formatRelativeAge(msg.data.created_at)}
        </div>
      </section>
    );
  }

  const d = msg.data;
  const steps = deriveLifecycleSteps({
    status: d.status,
    draftedAt: d.created_at,
    inboundReceivedAt: d.inbound?.created_at ?? null,
    reviewedAt: d.reviewed_at ?? null,
    ttlHint: d.approval_expires_at
      ? `TTL ${formatExpiresIn(d.approval_expires_at) ?? "—"}`
      : undefined,
  });
  const heldSummary = d.status === "pending_approval"
    ? `held · ${formatExpiresIn(d.approval_expires_at) ?? "—"} left`
    : d.reviewed_at
      ? `resolved ${formatRelativeAge(d.reviewed_at)}`
      : "resolved";

  return (
    <Collapsible
      label="Lifecycle"
      meta={
        <span style={{ color: d.status === "pending_approval" ? "var(--warn-strong)" : "var(--fg-subtle)" }}>
          {heldSummary}
        </span>
      }
      defaultOpen
    >
      <MessageLifecycleTimeline steps={steps} />
    </Collapsible>
  );
}

function CliHint({ messageId }: { messageId: string }) {
  return (
    <div
      style={{
        fontFamily: "var(--f-mono)",
        fontSize: 11,
        color: "var(--fg-subtle)",
        padding: "0 4px",
      }}
    >
      or via CLI:
      <div
        style={{
          marginTop: 6,
          background: "var(--bg-elev)",
          border: "1px solid var(--border-sub)",
          borderRadius: "var(--r-sm)",
          padding: "6px 10px",
          color: "var(--fg)",
        }}
      >
        e2a pending approve {messageId}
      </div>
    </div>
  );
}
