"use client";

// Single-message focus view (Screen 2 of agent_messages_v2).
//
// `/v1` exposes a single agent-scoped detail endpoint:
//   GET /v1/agents/{address}/messages/{id} → MessageView
// There is no bare-id endpoint, so the agent address is threaded in via
// the ?email= query param (the inbox list page links here with it). We
// fetch the same MessageView twice-shaped: once projected to the
// outbound PendingMessageDetail (for the draft/HITL surfaces) and once
// to the inbound InboundMessageDetail (for the received-mail surfaces),
// keyed off the row's `direction`-derived state. The outbound fetch is
// tried first (focus is most often a held draft); inbound is the
// fallback when the outbound projection isn't a pending draft.

import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import useSWR from "swr";
import {
  approvePendingMessage,
  getMessageDetail,
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
import { EmailHtmlBody } from "../../../../../components/messages/EmailHtmlBody";
import { MessageDirectionIcon } from "../../../../../components/messages/MessageDirectionIcon";
import {
  MessageLifecycleTimeline,
  deriveLifecycleSteps,
} from "../../../../../components/messages/MessageLifecycleTimeline";
import { formatRelativeAge } from "../../../../../../lib/relativeTime";
import {
  invalidateAgentMessages,
  invalidateAgents,
  invalidateMessageDetail,
  invalidatePendingList,
  pendingMessageKey,
} from "../../../../../../lib/swrKeys";

type LoadedMessage =
  | { direction: "outbound"; data: PendingMessageDetail }
  | { direction: "inbound"; data: InboundMessageDetail };

// Decode base64 `raw_message` and pull out the body part — everything
// after the first blank line is the body in RFC 5322 framing. Honest
// fallback only: multipart / MIME-encoded messages will render with
// boundary markers visible. Backend-side body_text parsing for inbound
// is tracked as a follow-up.
//
// UTF-8 detail: atob returns a "binary string" (one JS code unit per
// byte). Naively slicing + rendering would treat each byte as a Latin-1
// codepoint and corrupt any multi-byte UTF-8 sequence (emoji, accents,
// CJK). We re-encode through TextDecoder so plain text/plain bodies
// with non-ASCII characters render correctly. The split on the
// CRLF/LF blank-line boundary is byte-safe because both \r and \n are
// single-byte in UTF-8.
function decodeInboundBody(rawBase64: string | undefined): string {
  if (!rawBase64) return "";
  try {
    let bytes: Uint8Array;
    if (typeof atob === "function") {
      const binary = atob(rawBase64);
      bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    } else {
      // Node / SSR — the static export shouldn't hit this branch, but
      // tests run in jsdom where atob exists; this is a defensive
      // fallback for any future runtime that doesn't ship atob.
      bytes = new Uint8Array(Buffer.from(rawBase64, "base64"));
    }
    const decoded = new TextDecoder("utf-8", { fatal: false }).decode(bytes);
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

// AgentMessageFocusPage is split into an outer shell that reads the
// URL params and an inner FocusContent component keyed by `id`. The
// agent layout keys its children by `email`, so a same-agent
// navigation between message IDs (?id=A → ?id=B with same email)
// doesn't remount the page — without the inner `key={id}`, all the
// per-message state (draftBody, editingDraft, hasUserEditedRef,
// rejectReason, showRejectPrompt, submitError) would persist across
// the navigation and corrupt B with A's UI state.
// Next.js 16+ requires useSearchParams to live inside a Suspense
// boundary. The current "use client" declaration at the top of the
// file currently keeps this working, but adding any server component
// above the route would silently bail the static export. Wrap
// pre-emptively so the routing tree stays static-export-safe.
export default function AgentMessageFocusPage() {
  return (
    <Suspense fallback={null}>
      <FocusPageRouter />
    </Suspense>
  );
}

function FocusPageRouter() {
  const searchParams = useSearchParams();
  const email = searchParams.get("email") ?? "";
  const id = searchParams.get("id") ?? "";
  const initialHeadersOpen = searchParams.get("headers") === "1";
  // The `/v1` detail endpoint (MessageView) carries NEITHER `direction`
  // nor `review_status`, and blanks `from`/`status` on outbound rows — so
  // direction and pending-state can't be recovered from the fetch. The
  // list/pending rows (MessageSummaryView) have both, so they thread the
  // authoritative values in via query params. Missing → inbound /
  // not-pending: a deep link can't prove a held outbound draft, so we
  // default to the safe shape that hides approve/reject.
  const direction: "inbound" | "outbound" =
    searchParams.get("direction") === "outbound" ? "outbound" : "inbound";
  const pending = searchParams.get("pending") === "1";
  return (
    <FocusContent
      key={`${email}|${id}`}
      email={email}
      id={id}
      direction={direction}
      pending={pending}
      initialHeadersOpen={initialHeadersOpen}
    />
  );
}

function FocusContent({
  email,
  id,
  direction: threadedDirection,
  pending: threadedPending,
  initialHeadersOpen,
}: {
  email: string;
  id: string;
  direction: "inbound" | "outbound";
  pending: boolean;
  initialHeadersOpen: boolean;
}) {
  const router = useRouter();

  // Holds are now driven by inbound/outbound policy + content scans
  // (there's no per-agent HITL on/off flag any more), so the lifecycle
  // panel always includes the "Held for review" step.
  const hitlEnabled = true;

  // `hasUserEditedRef` flips true the first time the user types into
  // the draft-body textarea OR clicks Edit. The seeding effect (below)
  // uses it to decide whether to overwrite the textarea with the
  // server's body when SWR revalidates. Without the guard, a
  // revalidation mid-edit (focus event, manual mutate) would silently
  // revert the user's edits — or, more subtly, re-populate a body the
  // user deliberately cleared.
  const hasUserEditedRef = useRef(false);

  // Single agent-scoped fetch (GET /v1/agents/{address}/messages/{id}).
  // The detail MessageView has no `direction`, so we pass the threaded
  // direction (from the list row's MessageSummaryView) into
  // `getMessageDetail` to pick the right projection. Opts out of
  // `keepPreviousData` so navigating between focus pages (different
  // `?id=`) doesn't briefly render the previous message under the new
  // URL.
  const detailSWR = useSWR(
    email && id ? pendingMessageKey(email, id) : null,
    () => getMessageDetail(email, id, threadedDirection),
    {
      shouldRetryOnError: false,
      keepPreviousData: false,
      // The detail GET flips inbox_status unread → read server-side as a
      // side effect. Invalidate the inbox cache so the row's read state
      // reflects immediately rather than waiting for focus revalidation.
      onSuccess: () => {
        void invalidateAgentMessages(email);
      },
    },
  );

  const msg: LoadedMessage | null = detailSWR.data ?? null;

  const error: string = detailSWR.error
    ? detailSWR.error.message || "Failed to load message"
    : "";

  const [editingDraft, setEditingDraft] = useState(false);
  const [draftBody, setDraftBody] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState("");
  const [showRejectPrompt, setShowRejectPrompt] = useState(false);
  const [rejectReason, setRejectReason] = useState("");

  // Seed the draft-body textarea from the loaded outbound message.
  // Effect-based seeding fires on every render where the loaded
  // message changes — including the cache-hit case where data resolves
  // synchronously on mount. The hasUserEditedRef guard prevents
  // window-focus revalidation from stomping in-progress edits.
  const outboundData =
    detailSWR.data?.direction === "outbound" ? detailSWR.data.data : null;
  useEffect(() => {
    if (hasUserEditedRef.current) return;
    if (!outboundData) return;
    setDraftBody(outboundData.body_text ?? "");
  }, [outboundData]);

  // The detail MessageView's `status` is the DELIVERY rollup
  // (queued/sent/…), never the HITL lifecycle — "pending_review" only
  // ever lives in `review_status` on the summary view, which the detail
  // doesn't return. So we gate the approve/reject affordances on the
  // `pending` flag threaded from the list/pending row (alongside
  // `direction`). Absent param → not pending → approve/reject hidden.
  const isPending = msg?.direction === "outbound" && threadedPending;

  const inboxLink = `/inboxes/messages?email=${encodeURIComponent(email)}`;
  const convLink = msg?.direction === "outbound"
    ? `${inboxLink}#${msg.data.conversation_id ? `conv:${msg.data.conversation_id}` : `orphan:${msg.data.id}`}`
    : msg?.direction === "inbound"
      ? `${inboxLink}#${msg.data.conversation_id ? `conv:${msg.data.conversation_id}` : `orphan:${msg.data.message_id}`}`
      : inboxLink;

  // Both approve and reject invalidate four SWR caches so the rest
  // of the dashboard reflects the new state immediately:
  //   • pendingMessagesKey  → Sidebar badge drops
  //   • pendingMessageKey   → this focus page itself (the row's
  //                            status moves from pending_approval
  //                            to sent/rejected)
  //   • agentMessagesKey*   → the inbox view drops the pending callout
  //   • agentsKey           → /inboxes agent cards show updated
  //                            `pending_count` per agent
  // We `await Promise.all(...)` before navigating so the inbox
  // re-render happens against fresh data, not the previous payload.
  const refreshAfterMutation = useCallback(
    async (msgId: string) => {
      await Promise.all([
        invalidatePendingList(),
        invalidateMessageDetail(msgId),
        invalidateAgentMessages(email),
        invalidateAgents(),
      ]);
    },
    [email],
  );

  const onApprove = useCallback(async () => {
    if (!msg || msg.direction !== "outbound") return;
    setSubmitting(true);
    setSubmitError("");
    try {
      const overrides = editingDraft && draftBody !== (msg.data.body_text ?? "")
        ? { body: draftBody }
        : {};
      await approvePendingMessage(email, msg.data.id, overrides);
      await refreshAfterMutation(msg.data.id);
      router.push(convLink);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to approve");
      setSubmitting(false);
    }
  }, [msg, editingDraft, draftBody, email, router, convLink, refreshAfterMutation]);

  const onReject = useCallback(async () => {
    if (!msg || msg.direction !== "outbound") return;
    setSubmitting(true);
    setSubmitError("");
    try {
      await rejectPendingMessage(email, msg.data.id, rejectReason || "rejected by reviewer");
      await refreshAfterMutation(msg.data.id);
      router.push(convLink);
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "Failed to reject");
      setSubmitting(false);
    }
  }, [msg, rejectReason, email, router, convLink, refreshAfterMutation]);

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
  // submitError surfaces from a failed approve/reject — it's
  // localized to the action card, not the fetch path.
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
  // PendingMessageDetail uses `id`, InboundMessageDetail uses
  // `message_id`; everything else lives at the same key under both
  // shapes, so we only narrow at the points where the shapes differ.
  const direction = msg.direction;
  const directionLabel =
    direction === "outbound"
      ? msg.data.type === "reply"
        ? "outbound · reply"
        : "outbound"
      : "inbound";
  const subject = msg.data.subject;
  const from = direction === "outbound" ? email : msg.data.from;
  const to = direction === "outbound" ? msg.data.to?.[0] ?? "" : email;
  const convId = msg.data.conversation_id;
  const createdAt = msg.data.created_at;
  const messageId = direction === "outbound" ? msg.data.id : msg.data.message_id;
  const inReplyTo =
    direction === "outbound" && msg.data.inbound
      ? {
          sender: msg.data.inbound.sender,
          createdAt: msg.data.inbound.created_at,
          parentId: msg.data.email_message_id,
        }
      : null;
  // The detail MessageView's `status` is the delivery rollup; the HITL
  // "pending_review" state is threaded in, not present on the wire.
  const statusAttr = isPending ? "pending_review" : msg.data.status;

  return (
    <div
      data-testid="message-focus"
      data-direction={direction}
      data-status={statusAttr}
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
              <Dot tone="warn" /> Pending review
            </Chip>
          )}
          {direction === "outbound" && msg.data.status === "sent" && (
            <Chip tone="success">Sent</Chip>
          )}
          {direction === "outbound" && (msg.data.status === "review_rejected" || msg.data.status === "review_expired_rejected") && (
            <Chip tone="danger">{msg.data.status === "review_expired_rejected" ? "Auto-rejected" : "Rejected"}</Chip>
          )}
          {direction === "outbound" && msg.data.status === "review_expired_approved" && (
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
            isPending={isPending}
            editingDraft={editingDraft}
            draftBody={draftBody}
            onChangeDraft={(v) => {
              hasUserEditedRef.current = true;
              setDraftBody(v);
            }}
            onStartEdit={() => {
              setEditingDraft(true);
              // Even if the user hasn't typed yet, opening the
              // editor signals intent to control the body — a
              // revalidation mid-edit (focus event, scrubbed TTL)
              // must not overwrite the textarea out from under them.
              hasUserEditedRef.current = true;
            }}
            onCancelEdit={() => {
              setEditingDraft(false);
              setDraftBody(msg.direction === "outbound" ? (msg.data.body_text ?? "") : "");
              // Cancel reverts to the server's body — clear the
              // "user edited" flag so future revalidations apply.
              hasUserEditedRef.current = false;
            }}
          />
          <HeadersCollapsible msg={msg} defaultOpen={initialHeadersOpen} />
        </div>
        <aside className="flex flex-col gap-4">
          {isPending && msg.direction === "outbound" && (
            <>
              <ActionCard
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
              {submitError && (
                <p
                  className="text-[12px]"
                  style={{ color: "var(--danger-strong)" }}
                >
                  {submitError}
                </p>
              )}
            </>
          )}
          <LifecycleSection msg={msg} hitlEnabled={hitlEnabled} />
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
  isPending,
  editingDraft,
  draftBody,
  onChangeDraft,
  onStartEdit,
  onCancelEdit,
}: {
  msg: LoadedMessage;
  // Threaded HITL-pending flag (the detail MessageView can't tell us —
  // its `status` is the delivery rollup, not the HITL lifecycle).
  isPending: boolean;
  editingDraft: boolean;
  draftBody: string;
  onChangeDraft: (v: string) => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
}) {
  const bodyText = useMemo(() => {
    if (msg.direction === "outbound") return msg.data.body_text ?? "";
    // Prefer the backend-parsed text (QP/base64 decoded, HTML→text); the raw
    // decode is a last-resort fallback that shows MIME framing.
    return msg.data.parsed?.text || decodeInboundBody(msg.data.raw_message);
  }, [msg]);
  // Rich HTML body, when the message carries one — rendered sanitized in a
  // sandboxed iframe. Outbound drafts/sends use body_html; inbound uses
  // parsed.html (the decoded text/html part).
  const bodyHtml = useMemo(() => {
    if (msg.direction === "outbound") return msg.data.body_html ?? "";
    return msg.data.parsed?.html ?? "";
  }, [msg]);

  const isOutbound = msg.direction === "outbound";
  // A non-pending outbound row with no body_text is a sent/scrubbed
  // draft — its body is no longer available. Keyed off the threaded
  // pending flag rather than the (delivery-rollup) detail `status`.
  const isTerminal = isOutbound && !isPending && !msg.data.body_text;
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
            whiteSpace: bodyHtml.trim() !== "" ? "normal" : "pre-wrap",
          }}
        >
          {isTerminal ? (
            <span style={{ color: "var(--fg-muted)", fontStyle: "italic" }}>
              Body no longer available (scrubbed after delivery).
            </span>
          ) : bodyHtml.trim() !== "" ? (
            <EmailHtmlBody html={bodyHtml} />
          ) : (
            bodyText || (
              <span style={{ color: "var(--fg-muted)", fontStyle: "italic" }}>
                (message body not available)
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
          </>
        ) : isOutbound && isPending ? (
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

// Strip control chars (and stray CR/LF) from upstream-supplied header
// values so they paste cleanly out of the InkConsole's copy button.
// Upstream SMTP servers can legally include 8-bit bytes in continuation
// lines; React escapes for the DOM but the copy buffer would still
// carry the raw value. Truncate to a sane length too.
function scrubHeaderValue(raw: string): string {
  if (!raw) return raw;
  const stripped = raw.replace(/[\x00-\x1f\x7f]+/g, " ");
  return stripped.length > 200 ? stripped.slice(0, 197) + "…" : stripped;
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
  // header we got. Values are scrubbed for clipboard safety — upstream
  // SMTP could embed control chars that would otherwise pollute the
  // copy buffer when a reviewer pastes from the InkConsole.
  for (const [k, rawV] of Object.entries(d.auth_headers ?? {})) {
    const v = scrubHeaderValue(rawV);
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
    for (const [k, rawV] of Object.entries(d.inbound.auth_headers)) {
      const v = scrubHeaderValue(rawV);
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
  submitting,
  showRejectPrompt,
  rejectReason,
  onChangeReason,
  onApprove,
  onStartReject,
  onCancelReject,
  onConfirmReject,
}: {
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
        default:{" "}
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

function LifecycleSection({ msg, hitlEnabled }: { msg: LoadedMessage; hitlEnabled: boolean }) {
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
    hitlEnabled,
  });
  // Collapsible meta tracks the latest meaningful event. For HITL-off
  // agents the held step doesn't exist, so we summarize sent/created
  // directly instead of the misleading "resolved" fallback.
  const sentAt = d.reviewed_at ?? (!hitlEnabled ? d.created_at : null);
  const heldSummary = d.status === "pending_review"
    ? "held"
    : sentAt
      ? `${hitlEnabled ? "resolved" : "sent"} ${formatRelativeAge(sentAt)}`
      : "resolved";

  return (
    <Collapsible
      label="Lifecycle"
      meta={
        <span style={{ color: d.status === "pending_review" ? "var(--warn-strong)" : "var(--fg-subtle)" }}>
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
