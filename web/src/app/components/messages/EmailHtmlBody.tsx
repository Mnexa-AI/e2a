"use client";

// Renders an untrusted email HTML body safely. Two layers of defense:
//   1. DOMPurify strips scripts, event handlers, and dangerous tags/attrs.
//   2. The cleaned markup is rendered inside a sandboxed <iframe srcdoc> with
//      NO allow-scripts — so even if something slipped past the sanitizer it
//      cannot execute, touch cookies, or reach the dashboard DOM.
// Remote images are blocked by default (tracking-pixel / read-receipt defense,
// the Gmail convention) and loaded only when the user opts in.
//
// The iframe keeps `allow-same-origin` (so we can measure its content height to
// auto-size) and `allow-popups` (so sanitized links can open in a new tab via
// the injected `<base target="_blank">`). It deliberately omits allow-scripts
// and allow-forms.

import DOMPurify from "dompurify";
import { useEffect, useMemo, useRef, useState } from "react";

// sanitizeEmail cleans the raw email HTML. When showImages is false it also
// neutralizes remote image sources (img src/srcset and CSS url() backgrounds),
// reporting whether anything was actually blocked so the caller can offer a
// "Load images" affordance only when it matters.
function sanitizeEmail(
  html: string,
  showImages: boolean,
): { clean: string; blockedRemote: boolean } {
  let blockedRemote = false;

  DOMPurify.addHook("afterSanitizeAttributes", (node) => {
    const el = node as Element;
    // Force links to open in a new tab and drop the referrer/opener.
    if (el.tagName === "A") {
      el.setAttribute("target", "_blank");
      el.setAttribute("rel", "noopener noreferrer nofollow");
    }
    if (!showImages) {
      // Strip every remote-resource carrier so the "Load images" banner is
      // honest (blockedRemote reflects what was actually present). This is the
      // belt; the CSP injected in wrapDocument is the authoritative block that
      // also covers CSS-escaped url() and any vector missed here.
      for (const attr of ["src", "srcset", "background", "poster"] as const) {
        if (el.getAttribute?.(attr)) {
          blockedRemote = true;
          el.removeAttribute(attr);
        }
      }
      const style = el.getAttribute?.("style");
      if (style && /url\s*\(/i.test(style)) {
        blockedRemote = true;
        el.setAttribute("style", style.replace(/url\s*\([^)]*\)/gi, "none"));
      }
    }
  });

  const clean = DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ["script", "iframe", "object", "embed", "form", "input", "button", "base"],
    ADD_ATTR: ["target"],
  });

  DOMPurify.removeHook("afterSanitizeAttributes");
  return { clean: String(clean), blockedRemote };
}

// wrapDocument builds the full srcdoc: a forced-light surface (email HTML
// assumes a white background regardless of the dashboard theme), responsive
// images, and a base target so links open in a new tab.
//
// The Content-Security-Policy is the AUTHORITATIVE control, not the DOMPurify
// hook: `default-src 'none'` blocks scripts/frames/objects/fetches outright,
// and when images are blocked `img-src data:` (no http/https) defeats every
// remote-resource vector at once — <img>, CSS url() (including CSS-escaped
// forms), <td background>, <video poster>, <source srcset>, web fonts — so the
// tracking-pixel block can't be bypassed by markup the hook didn't anticipate.
function wrapDocument(innerHTML: string, showImages: boolean): string {
  const csp = showImages
    ? "default-src 'none'; img-src http: https: data:; media-src http: https: data:; style-src 'unsafe-inline'; font-src http: https: data:"
    : "default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:";
  return (
    "<!doctype html><html><head><meta charset=\"utf-8\">" +
    "<meta http-equiv=\"Content-Security-Policy\" content=\"" + csp + "\">" +
    "<meta name=\"referrer\" content=\"no-referrer\">" +
    "<base target=\"_blank\">" +
    "<style>" +
    "html,body{margin:0;padding:0;}" +
    "body{background:#fff;color:#1a1a1a;" +
    "font:13.5px/1.6 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;" +
    "word-break:break-word;overflow-wrap:anywhere;}" +
    "img{max-width:100%;height:auto;}" +
    "table{max-width:100%;}" +
    "a{color:#2563eb;}" +
    "</style></head><body>" +
    innerHTML +
    "</body></html>"
  );
}

export function EmailHtmlBody({ html }: { html: string }) {
  const [showImages, setShowImages] = useState(false);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  const { clean, blockedRemote } = useMemo(
    () => sanitizeEmail(html, showImages),
    [html, showImages],
  );
  const srcDoc = useMemo(() => wrapDocument(clean, showImages), [clean, showImages]);

  // Auto-size the iframe to its content. Needs allow-same-origin to read the
  // content document; re-measures on reflow (e.g. late-loading images).
  useEffect(() => {
    const frame = iframeRef.current;
    if (!frame) return;
    let observer: ResizeObserver | undefined;
    const fit = () => {
      try {
        const doc = frame.contentDocument;
        if (doc?.documentElement) {
          frame.style.height = `${doc.documentElement.scrollHeight}px`;
        }
      } catch {
        /* cross-origin guard; ignore */
      }
    };
    const onLoad = () => {
      fit();
      try {
        const doc = frame.contentDocument;
        if (doc?.documentElement && typeof ResizeObserver !== "undefined") {
          observer = new ResizeObserver(fit);
          observer.observe(doc.documentElement);
        }
      } catch {
        /* ignore */
      }
    };
    frame.addEventListener("load", onLoad);
    fit();
    return () => {
      frame.removeEventListener("load", onLoad);
      observer?.disconnect();
    };
  }, [srcDoc]);

  return (
    <div>
      {blockedRemote && !showImages && (
        <div
          className="flex items-center"
          style={{
            gap: 10,
            marginBottom: 10,
            padding: "7px 11px",
            fontSize: 12,
            color: "var(--fg-muted)",
            background: "var(--bg-elev)",
            border: "1px solid var(--border-sub)",
            borderRadius: "var(--r-sm)",
          }}
        >
          <span>Remote images blocked to protect your privacy.</span>
          <button
            type="button"
            onClick={() => setShowImages(true)}
            style={{
              color: "var(--accent-strong)",
              background: "transparent",
              border: "none",
              padding: 0,
              cursor: "pointer",
              fontWeight: 600,
            }}
          >
            Load images
          </button>
        </div>
      )}
      <iframe
        ref={iframeRef}
        title="Email body"
        sandbox="allow-same-origin allow-popups"
        srcDoc={srcDoc}
        style={{
          display: "block",
          width: "100%",
          border: "none",
          background: "#fff",
          borderRadius: "var(--r-sm)",
        }}
      />
    </div>
  );
}
