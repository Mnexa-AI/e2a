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
