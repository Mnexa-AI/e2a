"use client";

import { Suspense, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import {
  approvePendingMessage,
  getPendingMessage,
  rejectPendingMessage,
  type ApprovePayload,
} from "../../../../components/onboarding/api";
import type { PendingMessageDetail } from "../../../../components/types";
import { PageShell } from "../../../../components/loft/PageShell";
import { Chip } from "../../../../components/loft/Chip";

function parseCSV(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter((x) => x.length > 0);
}

// countWords splits on whitespace and returns the count — used by the
// draft footer to mimic the mock's "127 words · ~1 min read" hint.
// "~1 min read" assumes a 200-words-per-minute baseline.
function countWords(s: string): { words: number; minutes: number } {
  const trimmed = s.trim();
  if (!trimmed) return { words: 0, minutes: 0 };
  const words = trimmed.split(/\s+/).length;
  return { words, minutes: Math.max(1, Math.round(words / 200)) };
}

// detectPII: rough best-effort regex scan for email addresses, phone
// numbers, and SSN-shaped strings in the draft body. Intentionally
// permissive — surfaces hints, doesn't block sends. Server-side scrubbing
// is a separate concern.
function detectPII(s: string): string[] {
  const hits: string[] = [];
  if (/\b[\w.+-]+@[\w-]+\.[\w.-]+\b/.test(s)) hits.push("email");
  if (/\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b/.test(s))
    hits.push("phone");
  if (/\b\d{3}-\d{2}-\d{4}\b/.test(s)) hits.push("SSN");
  return hits;
}

// Pretty-print an SPF/DKIM/DMARC verdict for the provenance chip.
function verdictTone(verdict: string | undefined): "success" | "warn" | "neutral" {
  if (!verdict) return "neutral";
  const v = verdict.toLowerCase();
  if (v === "pass") return "success";
  if (v === "fail" || v === "softfail" || v === "permerror") return "warn";
  return "neutral";
}

function joinCSV(xs?: string[]): string {
  return (xs ?? []).join(", ");
}

function diffApproveEdits(
  current: PendingMessageDetail,
  draft: {
    subject: string;
    bodyText: string;
    bodyHTML: string;
    to: string;
    cc: string;
    bcc: string;
  },
): ApprovePayload {
  const out: ApprovePayload = {};
  if (draft.subject !== (current.subject ?? "")) out.subject = draft.subject;
  if (draft.bodyText !== (current.body_text ?? ""))
    out.body_text = draft.bodyText;
  if (draft.bodyHTML !== (current.body_html ?? ""))
    out.body_html = draft.bodyHTML;

  const toDraft = parseCSV(draft.to);
  if (JSON.stringify(toDraft) !== JSON.stringify(current.to ?? []))
    out.to = toDraft;

  const ccDraft = parseCSV(draft.cc);
  if (JSON.stringify(ccDraft) !== JSON.stringify(current.cc ?? []))
    out.cc = ccDraft;

  const bccDraft = parseCSV(draft.bcc);
  if (JSON.stringify(bccDraft) !== JSON.stringify(current.bcc ?? []))
    out.bcc = bccDraft;

  return out;
}

function ReviewContent() {
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";
  const router = useRouter();

  const [msg, setMsg] = useState<PendingMessageDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const [subject, setSubject] = useState("");
  const [bodyText, setBodyText] = useState("");
  const [bodyHTML, setBodyHTML] = useState("");
  const [to, setTo] = useState("");
  const [cc, setCC] = useState("");
  const [bcc, setBCC] = useState("");
  const [rejectReason, setRejectReason] = useState("");

  const [approving, setApproving] = useState(false);
  const [rejecting, setRejecting] = useState(false);

  const load = useCallback(async () => {
    if (!id) {
      setError("No message id in URL.");
      setLoading(false);
      return;
    }
    try {
      const data = await getPendingMessage(id);
      setMsg(data);
      setSubject(data.subject ?? "");
      setBodyText(data.body_text ?? "");
      setBodyHTML(data.body_html ?? "");
      setTo(joinCSV(data.to));
      setCC(joinCSV(data.cc));
      setBCC(joinCSV(data.bcc));
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load message");
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  const handleApprove = async () => {
    if (!msg) return;
    setApproving(true);
    setError("");
    try {
      const overrides = diffApproveEdits(msg, {
        subject,
        bodyText,
        bodyHTML,
        to,
        cc,
        bcc,
      });
      await approvePendingMessage(msg.id, overrides);
      router.push("/dashboard/pending");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Approve failed");
      setApproving(false);
    }
  };

  const handleReject = async () => {
    if (!msg) return;
    if (!confirm("Reject this message? It will be discarded and not sent."))
      return;
    setRejecting(true);
    setError("");
    try {
      await rejectPendingMessage(msg.id, rejectReason);
      router.push("/dashboard/pending");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Reject failed");
      setRejecting(false);
    }
  };

  if (loading) {
    return (
      <PageShell crumbs={["Pending", "Review"]}>
        <div
          className="text-[13px] py-12 text-center"
          style={{ color: "var(--fg-muted)" }}
        >
          Loading...
        </div>
      </PageShell>
    );
  }
  if (!msg) {
    return (
      <PageShell crumbs={["Pending", "Review"]}>
        <p
          className="text-[13px] mb-4"
          style={{ color: "var(--danger-strong)" }}
        >
          {error || "Message not found."}
        </p>
        <Link
          href="/dashboard/pending"
          className="text-[13px]"
          style={{ color: "var(--accent-strong)" }}
        >
          ← Back to pending
        </Link>
      </PageShell>
    );
  }

  const notPending = msg.status !== "pending_approval";
  const busy = approving || rejecting;

  return (
    <PageShell
      crumbs={["Pending", "Review"]}
      eyebrow="Outbound · review"
      title="Review message"
      subtitle={
        <>
          From{" "}
          <code
            className="font-mono"
            style={{ color: "var(--fg)" }}
          >
            {msg.agent_id}
          </code>
          {msg.type && <> · {msg.type}</>}
          {msg.approval_expires_at && (
            <>
              {" "}
              · expires {new Date(msg.approval_expires_at).toLocaleString()}
            </>
          )}
        </>
      }
      actions={
        <Link
          href="/dashboard/pending"
          className="text-[12px]"
          style={{ color: "var(--accent-strong)" }}
        >
          ← Back to pending
        </Link>
      }
    >
      {notPending && (
        <div
          className="mb-6 p-3 text-[13px]"
          style={{
            background: "var(--warn-bg)",
            color: "var(--warn-strong)",
            border: "1px solid var(--warn-bg)",
            borderRadius: "var(--r-md)",
          }}
        >
          This message is no longer pending — current status:{" "}
          <Chip tone="neutral" mono>
            {msg.status}
          </Chip>
          . Editing is disabled.
        </div>
      )}

      {error && (
        <div
          className="mb-6 p-3 text-[13px]"
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

      <div className="space-y-4">
        <Field label="Subject">
          <input
            type="text"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-[13px] px-3 py-2"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          />
        </Field>

        <Field label="To (comma-separated)">
          <input
            type="text"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-[13px] font-mono px-3 py-2"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          />
        </Field>

        <Field label="Cc (comma-separated)">
          <input
            type="text"
            value={cc}
            onChange={(e) => setCC(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-[13px] font-mono px-3 py-2"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          />
        </Field>

        <Field label="Bcc (comma-separated)">
          <input
            type="text"
            value={bcc}
            onChange={(e) => setBCC(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-[13px] font-mono px-3 py-2"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          />
        </Field>

        <Field label="Plain body">
          <textarea
            value={bodyText}
            onChange={(e) => setBodyText(e.target.value)}
            disabled={notPending || busy}
            rows={8}
            className="w-full text-[13px] font-mono px-3 py-2"
            style={{
              background: "var(--bg-panel)",
              border: "1px solid var(--border)",
              borderRadius: "var(--r-md)",
              color: "var(--fg)",
            }}
          />
          {/* Draft footer: word count + PII hints. Client-side; the mock
              also shows language detection but that's deferred until we
              wire a real detector — for now we just note the hint slot. */}
          {bodyText.length > 0 && (
            <div
              className="mt-1.5 flex items-center gap-3 text-[11px] flex-wrap"
              style={{ color: "var(--fg-subtle)" }}
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
        </Field>

        {msg.body_html !== undefined && (
          <Field label="HTML body">
            <textarea
              value={bodyHTML}
              onChange={(e) => setBodyHTML(e.target.value)}
              disabled={notPending || busy}
              rows={6}
              className="w-full text-[13px] font-mono px-3 py-2"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
                color: "var(--fg)",
              }}
            />
          </Field>
        )}

        {/* In reply to — inbound context pane (BACKEND_TODO #6 review polish).
            Shows the original sender, subject, and SPF/DKIM/DMARC chips
            from auth_headers. Only renders for replies; null inbound
            means the original aged out of retention or this is a
            non-reply (send/test). */}
        {msg.inbound && (
          <div
            className="p-3 space-y-2"
            style={{
              background: "var(--bg-elev)",
              border: "1px solid var(--border-sub)",
              borderRadius: "var(--r-md)",
            }}
          >
            <div
              className="font-mono text-[10px] uppercase font-semibold"
              style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
            >
              In reply to ·{" "}
              <span style={{ color: "var(--fg-muted)" }}>
                {new Date(msg.inbound.created_at).toLocaleString()}
              </span>
            </div>
            <div className="text-[13px]" style={{ color: "var(--fg)" }}>
              <span style={{ color: "var(--fg-muted)" }}>From </span>
              <code className="font-mono">{msg.inbound.sender}</code>
              <span style={{ color: "var(--fg-muted)" }}> · </span>
              {msg.inbound.subject || "(no subject)"}
            </div>
            {msg.inbound.auth_headers && (
              <div className="flex flex-wrap gap-1.5 text-[11px]">
                {(["spf", "dkim", "dmarc"] as const).map((k) => {
                  const v = msg.inbound!.auth_headers?.[k];
                  if (!v) return null;
                  const tone = verdictTone(v);
                  return (
                    <Chip key={k} tone={tone}>
                      {k.toUpperCase()}{" "}
                      <span style={{ fontFamily: "var(--f-mono)" }}>{v}</span>
                    </Chip>
                  );
                })}
              </div>
            )}
          </div>
        )}

        {/* Outbound headers preview — "what e2a will actually send".
            Pure client-side construction from the data we have; the
            HMAC body-hash + signature are computed at send time, so we
            label them as such instead of rendering placeholder hex. */}
        {!notPending && (
          <div
            className="p-3 font-mono text-[11px] leading-[1.6]"
            style={{
              background: "var(--ink, #1A1714)",
              color: "var(--ink-fg, #EFE6D8)",
              borderRadius: "var(--r-md)",
            }}
          >
            <div
              className="text-[10px] uppercase font-semibold mb-1.5"
              style={{ opacity: 0.6, letterSpacing: "0.08em" }}
            >
              Headers that will be sent
            </div>
            <div>From: {msg.agent_id}</div>
            <div>To: {parseCSV(to).join(", ") || "—"}</div>
            {parseCSV(cc).length > 0 && <div>Cc: {parseCSV(cc).join(", ")}</div>}
            <div>Subject: {subject}</div>
            {msg.email_message_id && (
              <div>In-Reply-To: {msg.email_message_id}</div>
            )}
            {msg.conversation_id && (
              <div>X-E2A-Conversation-Id: {msg.conversation_id}</div>
            )}
            <div style={{ opacity: 0.6 }}>
              X-E2A-Auth-Signature: [computed at send]
            </div>
          </div>
        )}

        {msg.attachments && msg.attachments.length > 0 && (
          <Field label="Attachments">
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
            <p
              className="text-[11px] mt-2"
              style={{ color: "var(--fg-subtle)" }}
            >
              Attachments are approved as-is; remove or edit via the API.
            </p>
          </Field>
        )}

        {/* Reviewer attribution from BACKEND_TODO #6. reviewed_by_name is
            null on worker-triggered transitions (TTL auto-approve / auto-
            reject) — surface that as "auto-resolved" so reviewers know no
            human looked at the message. */}
        {msg.reviewed_at && notPending && (
          <p
            className="text-[12px] mt-2"
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
                Auto-resolved at {new Date(msg.reviewed_at).toLocaleString()}
              </>
            )}
          </p>
        )}
      </div>

      {!notPending && (
        <div
          className="mt-8 pt-6 space-y-4"
          style={{ borderTop: "1px solid var(--border)" }}
        >
          <div className="flex items-center gap-3 flex-wrap">
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
          </div>
          <div>
            <label
              className="block text-[12px] mb-1"
              style={{ color: "var(--fg-muted)" }}
            >
              Optional rejection reason
            </label>
            <input
              type="text"
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              disabled={busy}
              placeholder="e.g., too aggressive, wrong recipient"
              className="w-full text-[13px] px-3 py-2"
              style={{
                background: "var(--bg-panel)",
                border: "1px solid var(--border)",
                borderRadius: "var(--r-md)",
                color: "var(--fg)",
              }}
            />
          </div>
          {/* CLI fallback — operators can also approve from the e2a CLI.
              Useful when the dashboard is being iterated on or for
              terminal-first workflows. */}
          <p
            className="font-mono text-[11px] mt-2"
            style={{ color: "var(--fg-subtle)" }}
          >
            CLI:{" "}
            <code
              className="px-2 py-0.5"
              style={{
                background: "var(--bg-elev)",
                border: "1px solid var(--border-sub)",
                borderRadius: "var(--r-sm)",
                color: "var(--fg-muted)",
              }}
            >
              e2a pending approve {msg.id}
            </code>
          </p>
        </div>
      )}
    </PageShell>
  );
}

export default function ReviewPage() {
  return (
    <Suspense
      fallback={
        <PageShell crumbs={["Pending", "Review"]}>
          <div
            className="text-[13px] py-12 text-center"
            style={{ color: "var(--fg-muted)" }}
          >
            Loading...
          </div>
        </PageShell>
      }
    >
      <ReviewContent />
    </Suspense>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label
        className="block text-[12px] mb-1"
        style={{ color: "var(--fg-muted)" }}
      >
        {label}
      </label>
      {children}
    </div>
  );
}
