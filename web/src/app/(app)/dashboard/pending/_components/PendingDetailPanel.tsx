"use client";

import { useEffect, useState } from "react";
import useSWR from "swr";
import { pendingMessageKey } from "../../../../../lib/swrKeys";
import {
  approvePendingMessage,
  getPendingMessage,
  rejectPendingMessage,
} from "../../../../components/onboarding/api";
import type { PendingMessageDetail } from "../../../../components/types";
import { Chip } from "../../../../components/loft/Chip";
import { diffApproveEdits, joinCSV } from "./edits";

// PendingDetailPanel renders the right-hand side of the pending review
// split-pane: header (chips + meta), two-column body (draft + context),
// outbound headers preview, and the approve/reject action bar.
//
// Driven by props (messageId, onChanged) so the parent split-pane owns
// the selection and refresh cycle. After approve/reject, onChanged
// fires so the queue can refetch and either advance to the next row or
// show the empty state.
//
// Pure helpers (parseCSV, joinCSV, diffApproveEdits) live in edits.ts
// so they can be unit-tested without rendering.

function countWords(s: string): { words: number; minutes: number } {
  const trimmed = s.trim();
  if (!trimmed) return { words: 0, minutes: 0 };
  const words = trimmed.split(/\s+/).length;
  return { words, minutes: Math.max(1, Math.round(words / 200)) };
}

function detectPII(s: string): string[] {
  const hits: string[] = [];
  if (/\b[\w.+-]+@[\w-]+\.[\w.-]+\b/.test(s)) hits.push("email");
  if (/\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b/.test(s))
    hits.push("phone");
  if (/\b\d{3}-\d{2}-\d{4}\b/.test(s)) hits.push("SSN");
  return hits;
}

function verdictTone(
  verdict: string | undefined,
): "success" | "warn" | "neutral" {
  if (!verdict) return "neutral";
  const v = verdict.toLowerCase();
  if (v === "pass") return "success";
  if (v === "fail" || v === "softfail" || v === "permerror") return "warn";
  return "neutral";
}

// formatExpiresIn returns the "47m" / "1h 23m" / "expired" form for the
// header chip. Distinct from the queue row formatter (full "in 47m").
function formatExpiresIn(iso?: string): string {
  if (!iso) return "—";
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return "expired";
  const min = Math.floor(ms / 60_000);
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  const mm = min % 60;
  return mm === 0 ? `${h}h` : `${h}h ${mm}m`;
}

function formatQueuedAgo(iso: string): string {
  const sec = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function PendingDetailPanel({
  agentEmail,
  messageId,
  onChanged,
}: {
  agentEmail: string;
  messageId: string;
  onChanged: () => void;
}) {
  // Shared SWR cache with the focus page: when the focus page (or
  // the pending page's handleChanged) calls
  // `invalidateMessageDetail(id)`, this panel's data refetches
  // automatically. The detail fetch is agent-scoped in /v1
  // (GET /v1/agents/{address}/messages/{id}), so the owning agent's
  // address is threaded in from the queue row.
  const {
    data: msg,
    error: fetchError,
    isLoading,
  } = useSWR<PendingMessageDetail>(
    agentEmail && messageId ? pendingMessageKey(agentEmail, messageId) : null,
    () => getPendingMessage(agentEmail, messageId),
    {
      // Different message ID = different conceptual content; don't
      // briefly render the previous message's data under the new id.
      keepPreviousData: false,
      // Seed the form fields whenever fresh data arrives. The
      // `editing === false` guard prevents revalidations from
      // stomping the reviewer's in-progress edits.
      onSuccess: (data) => {
        if (editing) return;
        setSubject(data.subject ?? "");
        setBodyText(data.body_text ?? "");
        setBodyHTML(data.body_html ?? "");
        setTo(joinCSV(data.to));
        setCC(joinCSV(data.cc));
        setBCC(joinCSV(data.bcc));
      },
    },
  );
  const loading = isLoading && !msg;

  // Reviewers see the draft read-only by default; clicking "Edit draft"
  // unlocks the inputs. Matches the mock's explicit edit-mode toggle
  // and prevents accidental keystrokes from being interpreted as edits.
  const [editing, setEditing] = useState(false);

  const [subject, setSubject] = useState("");
  const [bodyText, setBodyText] = useState("");
  const [bodyHTML, setBodyHTML] = useState("");
  const [to, setTo] = useState("");
  const [cc, setCC] = useState("");
  const [bcc, setBCC] = useState("");
  const [rejectReason, setRejectReason] = useState("");

  const [approving, setApproving] = useState(false);
  const [rejecting, setRejecting] = useState(false);

  // Errors from approve/reject mutations live here; load errors come
  // from SWR's fetchError. Both surface through the same banner below.
  const [actionError, setActionError] = useState("");
  const error =
    actionError ||
    (fetchError ? fetchError.message || "Failed to load message" : "");

  // Re-lock the form when the user navigates between messages —
  // otherwise an edit-mode session on message A persists into the
  // view of message B (which shares the panel instance via parent
  // remount strategy). Also clear stale action errors.
  useEffect(() => {
    setEditing(false);
    setActionError("");
  }, [messageId]);

  const handleApprove = async () => {
    if (!msg) return;
    setApproving(true);
    setActionError("");
    try {
      const overrides = diffApproveEdits(msg, {
        subject,
        bodyText,
        bodyHTML,
        to,
        cc,
        bcc,
      });
      await approvePendingMessage(agentEmail, msg.id, overrides);
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Approve failed");
      setApproving(false);
    }
  };

  const handleReject = async () => {
    if (!msg) return;
    if (!confirm("Reject this message? It will be discarded and not sent."))
      return;
    setRejecting(true);
    setActionError("");
    try {
      await rejectPendingMessage(agentEmail, msg.id, rejectReason);
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Reject failed");
      setRejecting(false);
    }
  };

  if (loading) {
    return (
      <div
        className="text-[13px] py-12 text-center"
        style={{ color: "var(--fg-muted)" }}
      >
        Loading message…
      </div>
    );
  }
  if (!msg) {
    return (
      <div
        className="text-[13px] py-12 text-center"
        style={{ color: "var(--fg-muted)" }}
      >
        {error || "Message not found."}
      </div>
    );
  }

  const notPending = msg.status !== "pending_approval";
  const busy = approving || rejecting;
  const expiresIn = formatExpiresIn(msg.approval_expires_at);
  const queuedAgo = formatQueuedAgo(msg.created_at);

  return (
    <div className="flex flex-col min-h-0 h-full">
      {/* Header */}
      <div
        className="px-6 py-5"
        style={{ borderBottom: "1px solid var(--border)" }}
      >
        <div className="flex items-center gap-2 flex-wrap mb-2">
          {msg.type && (
            <Chip tone="info" mono>
              {msg.type === "reply" ? "Reply" : msg.type === "test" ? "Test" : "Send"}
            </Chip>
          )}
          <Chip tone={notPending ? "neutral" : "warn"}>
            <span
              className="inline-block"
              style={{
                width: 6,
                height: 6,
                borderRadius: "50%",
                background: notPending ? "var(--fg-subtle)" : "var(--warn)",
                marginRight: 4,
              }}
            />
            {notPending ? msg.status : "Pending"}
          </Chip>
          {!notPending && msg.approval_expires_at && (
            <span
              className="font-mono text-[11px] font-semibold"
              style={{
                color:
                  expiresIn === "expired"
                    ? "var(--danger-strong)"
                    : "var(--warn-strong)",
                letterSpacing: "0.02em",
              }}
            >
              Expires in {expiresIn} · auto-reject on TTL
            </span>
          )}
          <span className="flex-1" />
          <span
            className="font-mono text-[11px]"
            style={{ color: "var(--fg-subtle)" }}
          >
            {msg.id}
          </span>
        </div>
        <h2
          className="text-[20px] font-semibold mb-2"
          style={{ color: "var(--fg)", letterSpacing: "-0.01em" }}
        >
          {msg.subject || "(no subject)"}
        </h2>
        <div
          className="font-mono text-[12px] flex flex-wrap gap-4"
          style={{ color: "var(--fg-muted)" }}
        >
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>from </span>
            {agentEmail}
          </span>
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>to </span>
            {joinCSV(msg.to) || "—"}
          </span>
          {msg.conversation_id && (
            <span>
              <span style={{ color: "var(--fg-subtle)" }}>conversation </span>
              {msg.conversation_id}
            </span>
          )}
          <span>
            <span style={{ color: "var(--fg-subtle)" }}>queued </span>
            {queuedAgo}
          </span>
        </div>
      </div>

      {error && (
        <div
          className="m-6 p-3 text-[13px]"
          style={{
            background: "var(--danger-bg)",
            color: "var(--danger-strong)",
            border: "1px solid var(--danger-bg)",
            borderRadius: "var(--r-md)",
          }}
        >
          {error}
        </div>
      )}

      {/* Body — two-column: draft (left) + context (right). On narrow
          screens collapses to one column. */}
      <div className="flex-1 overflow-y-auto">
        {/* Draft + Context. Side-by-side at md+, stacked on phones. The
            queue/detail outer shell also stacks on mobile (page.tsx),
            so the detail pane gets the full width when it's selected
            and the inner split has room to remain side-by-side at md+. */}
        <div
          className="grid grid-cols-1 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]"
          style={{
            borderBottom: "1px solid var(--border)",
          }}
        >
          {/* Draft pane */}
          <div
            className="px-6 py-5 space-y-4 border-b md:border-b-0 md:border-r"
            style={{ borderColor: "var(--border-sub)" }}
          >
            <SectionEyebrow>Draft from agent</SectionEyebrow>
            <DraftField label="Subject">
              <input
                type="text"
                value={subject}
                onChange={(e) => setSubject(e.target.value)}
                disabled={!editing || notPending || busy}
                className="w-full text-[14px] px-2 py-1"
                style={{
                  background: notPending
                    ? "transparent"
                    : "var(--bg-elev)",
                  border: "1px solid var(--border-sub)",
                  borderRadius: "var(--r-sm)",
                  color: "var(--fg)",
                }}
              />
            </DraftField>
            <DraftField label="To">
              <input
                type="text"
                value={to}
                onChange={(e) => setTo(e.target.value)}
                disabled={!editing || notPending || busy}
                className="w-full text-[12px] font-mono px-2 py-1"
                style={{
                  background: notPending
                    ? "transparent"
                    : "var(--bg-elev)",
                  border: "1px solid var(--border-sub)",
                  borderRadius: "var(--r-sm)",
                  color: "var(--fg)",
                }}
              />
            </DraftField>
            {(cc !== "" || !notPending) && (
              <DraftField label="Cc">
                <input
                  type="text"
                  value={cc}
                  onChange={(e) => setCC(e.target.value)}
                  disabled={!editing || notPending || busy}
                  className="w-full text-[12px] font-mono px-2 py-1"
                  style={{
                    background: notPending
                      ? "transparent"
                      : "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                  }}
                />
              </DraftField>
            )}
            {(bcc !== "" || !notPending) && (
              <DraftField label="Bcc">
                <input
                  type="text"
                  value={bcc}
                  onChange={(e) => setBCC(e.target.value)}
                  disabled={!editing || notPending || busy}
                  className="w-full text-[12px] font-mono px-2 py-1"
                  style={{
                    background: notPending
                      ? "transparent"
                      : "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                  }}
                />
              </DraftField>
            )}
            <DraftField label="Body">
              <textarea
                value={bodyText}
                onChange={(e) => setBodyText(e.target.value)}
                disabled={!editing || notPending || busy}
                rows={10}
                className="w-full text-[14px] px-2 py-2"
                style={{
                  background: notPending
                    ? "transparent"
                    : "var(--bg-elev)",
                  border: "1px solid var(--border-sub)",
                  borderRadius: "var(--r-sm)",
                  color: "var(--fg)",
                  lineHeight: 1.65,
                  whiteSpace: "pre-wrap",
                }}
              />
            </DraftField>
            {msg.body_html !== undefined && msg.body_html !== "" && (
              <DraftField label="HTML body">
                <textarea
                  value={bodyHTML}
                  onChange={(e) => setBodyHTML(e.target.value)}
                  disabled={!editing || notPending || busy}
                  rows={6}
                  className="w-full text-[12px] font-mono px-2 py-2"
                  style={{
                    background: notPending
                      ? "transparent"
                      : "var(--bg-elev)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-sm)",
                    color: "var(--fg)",
                  }}
                />
              </DraftField>
            )}
            {bodyText.length > 0 && (
              <div
                className="pt-3 flex items-center gap-4 text-[11px] flex-wrap"
                style={{
                  color: "var(--fg-subtle)",
                  borderTop: "1px solid var(--border-sub)",
                }}
              >
                <span>
                  {countWords(bodyText).words} words · ~
                  {countWords(bodyText).minutes} min read
                </span>
                {detectPII(bodyText).length > 0 ? (
                  <span style={{ color: "var(--warn-strong)" }}>
                    ⚠ PII hint: {detectPII(bodyText).join(", ")}
                  </span>
                ) : (
                  <span style={{ color: "var(--success)" }}>
                    ✓ no PII detected
                  </span>
                )}
              </div>
            )}
            {msg.attachments && msg.attachments.length > 0 && (
              <DraftField label="Attachments">
                <ul
                  className="text-[12px] space-y-1"
                  style={{ color: "var(--fg-muted)" }}
                >
                  {msg.attachments.map((a, i) => (
                    <li key={i} className="font-mono">
                      {a.filename}{" "}
                      <span style={{ color: "var(--fg-subtle)" }}>
                        ({a.content_type})
                      </span>
                    </li>
                  ))}
                </ul>
              </DraftField>
            )}
          </div>

          {/* Context pane */}
          <div
            className="px-6 py-5"
            style={{ background: "var(--bg-elev)" }}
          >
            {msg.inbound ? (
              <>
                <SectionEyebrow>
                  In reply to ·{" "}
                  <span style={{ color: "var(--fg-muted)" }}>
                    {formatQueuedAgo(msg.inbound.created_at)}
                  </span>
                </SectionEyebrow>
                <div className="mt-3 mb-3 flex items-center gap-3">
                  <Avatar name={msg.inbound.sender} />
                  <div className="min-w-0">
                    <div
                      className="text-[13px] font-semibold"
                      style={{ color: "var(--fg)" }}
                    >
                      {msg.inbound.sender.split("@")[0]}
                    </div>
                    <div
                      className="font-mono text-[11px] truncate"
                      style={{ color: "var(--fg-subtle)" }}
                    >
                      {msg.inbound.sender}
                    </div>
                  </div>
                </div>
                <div
                  className="text-[13px] italic mb-4 p-3"
                  style={{
                    background: "var(--bg-panel)",
                    border: "1px solid var(--border-sub)",
                    borderRadius: "var(--r-md)",
                    color: "var(--fg-muted)",
                    lineHeight: 1.6,
                  }}
                >
                  {msg.inbound.subject ? `"${msg.inbound.subject}"` : "(no subject)"}
                </div>
                {msg.inbound.auth_headers && (
                  <>
                    <SectionEyebrow>Provenance</SectionEyebrow>
                    <div
                      className="mt-2 font-mono text-[11px] space-y-1.5"
                      style={{ color: "var(--fg-muted)", lineHeight: 1.7 }}
                    >
                      <div className="flex flex-wrap gap-1.5">
                        {(["spf", "dkim", "dmarc"] as const).map((k) => {
                          const v = msg.inbound!.auth_headers?.[k];
                          if (!v) return null;
                          const tone = verdictTone(v);
                          return (
                            <Chip key={k} tone={tone}>
                              {k.toUpperCase()}{" "}
                              <span style={{ fontFamily: "var(--f-mono)" }}>
                                {v}
                              </span>
                            </Chip>
                          );
                        })}
                      </div>
                    </div>
                  </>
                )}
              </>
            ) : (
              <div
                className="text-[12px]"
                style={{ color: "var(--fg-subtle)" }}
              >
                <SectionEyebrow>Context</SectionEyebrow>
                <p className="mt-2">
                  No inbound context. This is a {msg.type || "send"} from the
                  agent, not a reply.
                </p>
              </div>
            )}
            {msg.reviewed_at && notPending && (
              <p
                className="text-[12px] mt-4"
                style={{ color: "var(--fg-muted)" }}
              >
                {msg.reviewed_by_name ? (
                  <>
                    Reviewed by{" "}
                    <span style={{ color: "var(--fg)", fontWeight: 500 }}>
                      {msg.reviewed_by_name}
                    </span>{" "}
                    at {new Date(msg.reviewed_at).toLocaleString()}
                  </>
                ) : (
                  <>
                    Auto-resolved at{" "}
                    {new Date(msg.reviewed_at).toLocaleString()}
                  </>
                )}
              </p>
            )}
          </div>
        </div>

      </div>

      {/* Action bar */}
      <div
        className="px-6 py-3 flex items-center gap-3 flex-wrap"
        style={{
          background: "var(--bg-elev)",
          borderTop: "1px solid var(--border)",
        }}
      >
        {!notPending ? (
          <>
            <button
              onClick={handleApprove}
              disabled={busy}
              className="text-[13px] font-medium px-4 py-2 transition disabled:opacity-50"
              style={{
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                borderRadius: "var(--r-md)",
              }}
            >
              {approving ? "Sending…" : "Approve & send"}
            </button>
            {/* Edit-draft toggle. Default state is read-only; clicking
                unlocks every field on the draft pane. "Cancel edit" re-
                locks and reverts to the originally-loaded values. */}
            <button
              onClick={() => {
                if (editing && msg) {
                  // Cancel: revert form state to the loaded message.
                  setSubject(msg.subject ?? "");
                  setBodyText(msg.body_text ?? "");
                  setBodyHTML(msg.body_html ?? "");
                  setTo(joinCSV(msg.to));
                  setCC(joinCSV(msg.cc));
                  setBCC(joinCSV(msg.bcc));
                }
                setEditing(!editing);
              }}
              disabled={busy}
              className="text-[13px] font-medium px-4 py-2 transition disabled:opacity-50"
              style={{
                background: "var(--bg-panel)",
                color: "var(--fg)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
              }}
            >
              {editing ? "Cancel edit" : "Edit draft"}
            </button>
            <button
              onClick={handleReject}
              disabled={busy}
              className="text-[13px] font-medium px-4 py-2 transition disabled:opacity-50"
              style={{
                background: "var(--bg-panel)",
                color: "var(--danger-strong)",
                border: "1px solid var(--danger-bg)",
                borderRadius: "var(--r-md)",
              }}
            >
              {rejecting ? "Rejecting…" : "Reject"}
            </button>
            <input
              type="text"
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              disabled={busy}
              placeholder="reject reason (optional)"
              className="text-[12px] px-2 py-1 flex-1 min-w-[180px] max-w-[280px]"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
                color: "var(--fg)",
              }}
            />
          </>
        ) : (
          <span
            className="text-[12px]"
            style={{ color: "var(--fg-muted)" }}
          >
            This message is no longer pending — editing is disabled.
          </span>
        )}
        <span className="flex-1" />
        <span
          className="font-mono text-[11px]"
          style={{ color: "var(--fg-muted)" }}
        >
          or use the CLI:
        </span>
        <code
          className="px-2 py-1 text-[11px]"
          style={{
            background: "var(--bg-panel)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-sm)",
            color: "var(--fg)",
            fontFamily: "var(--f-mono)",
          }}
        >
          e2a pending {notPending ? "view" : "approve"} {msg.id}
        </code>
      </div>
    </div>
  );
}

function SectionEyebrow({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="font-mono text-[10px] font-semibold uppercase"
      style={{ color: "var(--accent-strong)", letterSpacing: "0.08em" }}
    >
      {children}
    </div>
  );
}

function DraftField({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label
        className="block text-[11px] mb-1"
        style={{ color: "var(--fg-muted)" }}
      >
        {label}
      </label>
      {children}
    </div>
  );
}

// Avatar renders the sender's initials over a colored circle. Color is
// derived from the email hash so the same sender always looks the same.
function Avatar({ name }: { name: string }) {
  const local = name.split("@")[0] || name;
  const initials = local
    .split(/[.\-_]/)
    .slice(0, 2)
    .map((s) => s.charAt(0).toUpperCase())
    .join("");
  // Deterministic hue from the local part.
  let hash = 0;
  for (let i = 0; i < local.length; i++) hash = (hash * 31 + local.charCodeAt(i)) | 0;
  const hue = Math.abs(hash) % 360;
  return (
    <div
      className="flex items-center justify-center font-mono text-[11px] font-semibold"
      style={{
        width: 28,
        height: 28,
        borderRadius: "50%",
        background: `hsl(${hue}, 45%, 35%)`,
        color: "#fff",
        flexShrink: 0,
      }}
    >
      {initials || "?"}
    </div>
  );
}
