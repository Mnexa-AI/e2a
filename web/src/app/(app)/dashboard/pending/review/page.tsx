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

// parseCSV splits a comma-separated recipients input into trimmed
// non-empty addresses. Keeps the input UI simple — one field per
// recipient type — while matching the API's array shape.
function parseCSV(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter((x) => x.length > 0);
}

function joinCSV(xs?: string[]): string {
  return (xs ?? []).join(", ");
}

// diffApproveEdits compares the editor state against the loaded message
// and returns an ApprovePayload with only the fields the reviewer
// actually changed. Prevents sending a full body as an "edit" when the
// reviewer only tweaked the subject — keeps the edited flag accurate.
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
  if (draft.bodyText !== (current.body_text ?? "")) out.body_text = draft.bodyText;
  if (draft.bodyHTML !== (current.body_html ?? "")) out.body_html = draft.bodyHTML;

  const toDraft = parseCSV(draft.to);
  if (JSON.stringify(toDraft) !== JSON.stringify(current.to ?? [])) out.to = toDraft;

  const ccDraft = parseCSV(draft.cc);
  if (JSON.stringify(ccDraft) !== JSON.stringify(current.cc ?? [])) out.cc = ccDraft;

  const bccDraft = parseCSV(draft.bcc);
  if (JSON.stringify(bccDraft) !== JSON.stringify(current.bcc ?? [])) out.bcc = bccDraft;

  return out;
}

// Implementation split from the default export so useSearchParams can
// live inside a Suspense boundary (Next.js static export requirement).
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
      const overrides = diffApproveEdits(msg, { subject, bodyText, bodyHTML, to, cc, bcc });
      await approvePendingMessage(msg.id, overrides);
      router.push("/dashboard/pending");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Approve failed");
      setApproving(false);
    }
  };

  const handleReject = async () => {
    if (!msg) return;
    if (!confirm("Reject this message? It will be discarded and not sent.")) return;
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
    return <div className="text-sm text-muted py-12 text-center">Loading...</div>;
  }
  if (!msg) {
    return (
      <div>
        <p className="text-sm text-red-600 mb-4">{error || "Message not found."}</p>
        <Link href="/dashboard/pending" className="text-sm text-accent hover:underline">
          ← Back to pending
        </Link>
      </div>
    );
  }

  const notPending = msg.status !== "pending_approval";
  const busy = approving || rejecting;

  return (
    <>
      <div className="mb-6">
        <Link
          href="/dashboard/pending"
          className="text-xs text-accent hover:underline"
        >
          ← Back to pending
        </Link>
        <h2 className="text-2xl font-bold tracking-tight mt-2 mb-1">Review message</h2>
        <p className="text-xs text-muted">
          From <code className="font-mono text-foreground">{msg.agent_id}</code>
          {msg.type && <> · <span>{msg.type}</span></>}
          {msg.approval_expires_at && (
            <> · expires {new Date(msg.approval_expires_at).toLocaleString()}</>
          )}
        </p>
      </div>

      {notPending && (
        <div className="mb-6 p-3 bg-amber-50 border border-amber-200 rounded-lg text-sm text-amber-800">
          This message is no longer pending — current status:{" "}
          <strong>{msg.status}</strong>. Editing is disabled.
        </div>
      )}

      {error && (
        <div className="mb-6 p-3 bg-red-50 border border-red-200 rounded-lg text-sm text-red-700">
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
            className="w-full text-sm px-3 py-2 border border-border rounded-md"
          />
        </Field>

        <Field label="To (comma-separated)">
          <input
            type="text"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-sm px-3 py-2 border border-border rounded-md font-mono"
          />
        </Field>

        <Field label="Cc (comma-separated)">
          <input
            type="text"
            value={cc}
            onChange={(e) => setCC(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-sm px-3 py-2 border border-border rounded-md font-mono"
          />
        </Field>

        <Field label="Bcc (comma-separated)">
          <input
            type="text"
            value={bcc}
            onChange={(e) => setBCC(e.target.value)}
            disabled={notPending || busy}
            className="w-full text-sm px-3 py-2 border border-border rounded-md font-mono"
          />
        </Field>

        <Field label="Plain body">
          <textarea
            value={bodyText}
            onChange={(e) => setBodyText(e.target.value)}
            disabled={notPending || busy}
            rows={8}
            className="w-full text-sm px-3 py-2 border border-border rounded-md font-mono"
          />
        </Field>

        {msg.body_html !== undefined && (
          <Field label="HTML body">
            <textarea
              value={bodyHTML}
              onChange={(e) => setBodyHTML(e.target.value)}
              disabled={notPending || busy}
              rows={6}
              className="w-full text-sm px-3 py-2 border border-border rounded-md font-mono"
            />
          </Field>
        )}

        {msg.attachments && msg.attachments.length > 0 && (
          <Field label="Attachments">
            <ul className="text-xs text-muted space-y-1">
              {msg.attachments.map((a, i) => (
                <li key={i} className="font-mono">
                  {a.filename}{" "}
                  <span className="text-muted">({a.content_type})</span>
                </li>
              ))}
            </ul>
            <p className="text-[11px] text-muted mt-2">
              Attachments are approved as-is; remove or edit via the API.
            </p>
          </Field>
        )}
      </div>

      {!notPending && (
        <div className="mt-8 border-t border-border pt-6 space-y-4">
          <div className="flex items-center gap-3">
            <button
              onClick={handleApprove}
              disabled={busy}
              className="text-sm px-4 py-2 bg-green-600 text-white rounded-md hover:opacity-90 transition disabled:opacity-50"
            >
              {approving ? "Sending…" : "Approve & send"}
            </button>
            <button
              onClick={handleReject}
              disabled={busy}
              className="text-sm px-4 py-2 bg-red-600 text-white rounded-md hover:opacity-90 transition disabled:opacity-50"
            >
              {rejecting ? "Rejecting…" : "Reject"}
            </button>
          </div>
          <div>
            <label className="block text-xs text-muted mb-1">
              Optional rejection reason
            </label>
            <input
              type="text"
              value={rejectReason}
              onChange={(e) => setRejectReason(e.target.value)}
              disabled={busy}
              placeholder="e.g., too aggressive, wrong recipient"
              className="w-full text-sm px-3 py-2 border border-border rounded-md"
            />
          </div>
        </div>
      )}
    </>
  );
}

export default function ReviewPage() {
  return (
    <Suspense fallback={<div className="text-sm text-muted py-12 text-center">Loading...</div>}>
      <ReviewContent />
    </Suspense>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-xs text-muted mb-1">{label}</label>
      {children}
    </div>
  );
}
