"use client";

// Full-screen overlay that renders one attachment in-app — an image or a PDF —
// so reviewing what an agent sent or received doesn't require downloading it
// first. Anything we can't render natively falls back to a download prompt
// rather than a broken embed.
//
// The bytes are fetched once into a blob: object URL, which serves both the
// preview and the Download button (no second round trip). The URL is revoked
// on unmount and whenever the attachment changes.

import { useCallback, useEffect, useRef, useState } from "react";
import { loadAttachmentObjectUrl } from "../onboarding/api";
import { previewKind } from "./attachmentPreview";
import type { AttachmentMeta } from "../types";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function AttachmentViewer({
  email,
  messageId,
  att,
  onClose,
}: {
  email: string;
  messageId: string;
  att: AttachmentMeta;
  onClose: () => void;
}) {
  const [url, setUrl] = useState("");
  const [error, setError] = useState("");
  const closeRef = useRef<HTMLButtonElement>(null);
  const kind = previewKind(att.content_type);
  const name = att.filename || `attachment ${att.index}`;

  // Fetch bytes once per attachment. The revoke from the *previous* load runs
  // on cleanup, so switching attachments never leaks an object URL.
  useEffect(() => {
    let cancelled = false;
    let revoke: (() => void) | undefined;
    setUrl("");
    setError("");
    (async () => {
      try {
        const loaded = await loadAttachmentObjectUrl(email, messageId, att);
        revoke = loaded.revoke;
        if (cancelled) {
          loaded.revoke();
          return;
        }
        setUrl(loaded.url);
      } catch {
        if (!cancelled) setError("Couldn't load this attachment.");
      }
    })();
    return () => {
      cancelled = true;
      revoke?.();
    };
  }, [email, messageId, att]);

  // Esc closes from anywhere in the overlay, matching every other dismissible
  // surface in the app.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Move focus into the dialog so Esc and Tab act on it rather than the page
  // behind it.
  useEffect(() => {
    closeRef.current?.focus();
  }, []);

  const stop = useCallback((e: React.MouseEvent) => e.stopPropagation(), []);

  return (
    <div
      data-testid="attachment-viewer"
      role="dialog"
      aria-modal="true"
      aria-label={name}
      onClick={onClose}
      className="fixed inset-0 flex items-center justify-center"
      style={{
        zIndex: 50,
        background: "color-mix(in srgb, var(--ink) 72%, transparent)",
        padding: 24,
      }}
    >
      <div
        onClick={stop}
        className="flex flex-col"
        style={{
          background: "var(--bg-panel)",
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
          boxShadow: "var(--sh-pop)",
          maxWidth: 1000,
          width: "100%",
          maxHeight: "100%",
          overflow: "hidden",
        }}
      >
        {/* Title bar */}
        <div
          className="flex items-center"
          style={{
            gap: 12,
            padding: "10px 12px",
            borderBottom: "1px solid var(--border-sub)",
            flexShrink: 0,
          }}
        >
          <span
            className="truncate"
            style={{ fontSize: 13, fontWeight: 600, color: "var(--fg)", minWidth: 0 }}
          >
            {name}
          </span>
          <span
            className="font-mono"
            style={{ fontSize: 11, color: "var(--fg-subtle)", flexShrink: 0 }}
          >
            {fmtBytes(att.size_bytes)}
          </span>
          <span className="flex-1" />
          {url && (
            <a
              href={url}
              download={name}
              data-testid="attachment-viewer-download"
              style={{
                fontSize: 12,
                fontWeight: 500,
                padding: "6px 12px",
                borderRadius: "var(--r-md)",
                background: "var(--accent-fill)",
                color: "var(--accent-fg)",
                textDecoration: "none",
                flexShrink: 0,
              }}
            >
              Download
            </a>
          )}
          <button
            ref={closeRef}
            type="button"
            onClick={onClose}
            aria-label="Close attachment preview"
            style={{
              fontSize: 14,
              lineHeight: 1,
              padding: "6px 10px",
              borderRadius: "var(--r-md)",
              background: "var(--bg-elev)",
              border: "1px solid var(--border)",
              color: "var(--fg-muted)",
              flexShrink: 0,
            }}
          >
            ✕
          </button>
        </div>

        {/* Body */}
        <div
          className="flex items-center justify-center"
          style={{
            padding: kind === "pdf" ? 0 : 16,
            background: "var(--bg-sunken)",
            overflow: "auto",
            flex: 1,
            minHeight: 320,
          }}
        >
          {error ? (
            <p style={{ fontSize: 13, color: "var(--danger-strong)" }}>{error}</p>
          ) : !url ? (
            <p style={{ fontSize: 13, color: "var(--fg-muted)" }}>Loading preview…</p>
          ) : kind === "image" ? (
            /* eslint-disable-next-line @next/next/no-img-element -- blob: URL of
               already-fetched bytes; next/image would re-request through the
               optimizer, which cannot read an object URL. */
            <img
              src={url}
              alt={name}
              data-testid="attachment-viewer-image"
              style={{ maxWidth: "100%", maxHeight: "70vh", objectFit: "contain" }}
            />
          ) : kind === "pdf" ? (
            <iframe
              src={url}
              title={name}
              data-testid="attachment-viewer-pdf"
              style={{ width: "100%", height: "70vh", border: "none" }}
            />
          ) : (
            /* Unrenderable type — say so plainly and offer the bytes. */
            <p style={{ fontSize: 13, color: "var(--fg-muted)", textAlign: "center" }}>
              No in-app preview for this file type.
              <br />
              Use Download to open it locally.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
