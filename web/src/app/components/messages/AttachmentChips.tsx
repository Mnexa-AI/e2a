"use client";

// Download chips for a message's attachments that are NOT inline images — i.e.
// the ones not rendered in the body via a `cid:` reference (PDFs, documents,
// images sent as real attachments). Each chip fetches a short-lived signed
// download URL on click and opens it; the backend streams the bytes with
// Content-Disposition: attachment, so the browser saves the file.

import { useState } from "react";
import { getAttachment } from "../onboarding/api";
import { findCidRefs } from "./inlineImages";
import { AttachmentViewer } from "./AttachmentViewer";
import { canPreview } from "./attachmentPreview";
import type { AttachmentMeta } from "../types";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// Attachments whose content_id is referenced by a `cid:` in the body render
// inline; the rest are downloadable. When there's no HTML body, every
// attachment is downloadable.
export function downloadableAttachments(
  attachments: AttachmentMeta[] | undefined,
  html: string | undefined,
): AttachmentMeta[] {
  const list = attachments ?? [];
  if (list.length === 0) return [];
  const refs = html ? findCidRefs(html) : new Set<string>();
  return list.filter((a) => !(a.content_id && refs.has(a.content_id)));
}

function AttachmentChip({
  email,
  messageId,
  att,
  onPreview,
}: {
  email: string;
  messageId: string;
  att: AttachmentMeta;
  // Set for types the in-app viewer can render; absent means download-on-click.
  onPreview?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);

  const download = async () => {
    setBusy(true);
    setError(false);
    try {
      const meta = await getAttachment(email, messageId, att.index);
      const a = document.createElement("a");
      a.href = meta.download_url;
      a.target = "_blank";
      a.rel = "noopener";
      a.click();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  };

  // Previewable attachments open the viewer (which offers Download); the rest
  // download straight away, since there's nothing to show.
  const onClick = onPreview ?? download;

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      title={
        error
          ? "Download failed — try again"
          : `${onPreview ? "Preview" : "Download"} ${att.filename || "attachment"}`
      }
      data-testid="attachment-chip"
      className="flex items-center"
      style={{
        gap: 8,
        padding: "6px 10px",
        fontSize: 12,
        maxWidth: "100%",
        cursor: busy ? "default" : "pointer",
        color: error ? "var(--danger-strong)" : "var(--fg)",
        background: "var(--bg-elev)",
        border: `1px solid ${error ? "var(--danger-strong)" : "var(--border-sub)"}`,
        borderRadius: "var(--r-sm)",
      }}
    >
      <span aria-hidden style={{ flexShrink: 0 }}>{busy ? "…" : "📎"}</span>
      <span
        style={{
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
          minWidth: 0,
        }}
      >
        {att.filename || `attachment ${att.index}`}
      </span>
      <span style={{ color: "var(--fg-subtle)", flexShrink: 0 }}>
        {fmtBytes(att.size_bytes)}
      </span>
    </button>
  );
}

export function AttachmentChips({
  email,
  messageId,
  attachments,
}: {
  email: string;
  messageId: string;
  attachments: AttachmentMeta[];
}) {
  // Which attachment the viewer is showing, by index (null = closed).
  const [previewing, setPreviewing] = useState<number | null>(null);

  if (attachments.length === 0) return null;
  const active = attachments.find((a) => a.index === previewing);

  return (
    <div
      className="flex items-center"
      data-testid="attachment-chips"
      style={{ gap: 8, marginTop: 10, flexWrap: "wrap" }}
    >
      {attachments.map((a) => (
        <AttachmentChip
          key={a.index}
          email={email}
          messageId={messageId}
          att={a}
          onPreview={
            canPreview(a.content_type)
              ? () => setPreviewing(a.index)
              : undefined
          }
        />
      ))}
      {active && (
        <AttachmentViewer
          email={email}
          messageId={messageId}
          att={active}
          onClose={() => setPreviewing(null)}
        />
      )}
    </div>
  );
}
