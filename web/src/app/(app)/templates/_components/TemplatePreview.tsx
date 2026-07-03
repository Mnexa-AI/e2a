"use client";

import { useState } from "react";
import { forceDarkPreview } from "../_lib/preview";

// Rendered-email preview: subject line, an HTML-part / text-part tab
// switch, and a light/dark toggle. The HTML part renders in a sandboxed
// iframe via srcdoc (sandbox="" — no scripts, no navigation) so template
// markup can never touch the dashboard document. Dark mode rewrites the
// document's `@media (prefers-color-scheme: dark)` condition in the
// display copy (see forceDarkPreview) because color-scheme alone cannot
// force that media query inside the iframe.

type Tab = "html" | "text";

export function TemplatePreview({
  subject,
  textBody,
  htmlBody,
}: {
  subject: string;
  textBody: string;
  htmlBody?: string;
}) {
  const hasHTML = Boolean(htmlBody);
  const [tab, setTab] = useState<Tab>(hasHTML ? "html" : "text");
  const [dark, setDark] = useState(false);
  // If the html part disappears (e.g. the user clears it in the editor
  // between previews) fall back to the text tab.
  const activeTab: Tab = tab === "html" && !hasHTML ? "text" : tab;

  const tabButton = (key: Tab, label: string, disabled = false) => {
    const active = activeTab === key;
    return (
      <button
        key={key}
        type="button"
        onClick={() => setTab(key)}
        disabled={disabled}
        aria-pressed={active}
        className="px-3 py-1 text-[12px] font-medium transition disabled:opacity-40 disabled:cursor-not-allowed"
        style={{
          borderRadius: 999,
          background: active ? "var(--fg)" : "var(--bg-panel)",
          color: active ? "var(--bg)" : "var(--fg-muted)",
          border: active ? "1px solid var(--fg)" : "1px solid var(--border)",
        }}
      >
        {label}
      </button>
    );
  };

  return (
    <div>
      {/* Subject line */}
      <div
        className="flex items-baseline gap-2 mb-3 px-3 py-2"
        style={{
          background: "var(--bg-elev)",
          border: "1px solid var(--border-sub)",
          borderRadius: "var(--r-md)",
        }}
      >
        <span
          className="font-mono text-[10px] uppercase shrink-0"
          style={{ color: "var(--fg-subtle)", letterSpacing: "0.08em" }}
        >
          Subject
        </span>
        <span
          className="text-[13px] font-medium break-words min-w-0"
          style={{ color: "var(--fg)" }}
        >
          {subject}
        </span>
      </div>

      {/* Tab switch + theme toggle */}
      <div className="flex items-center gap-2 mb-2 flex-wrap">
        {tabButton("html", "HTML part", !hasHTML)}
        {tabButton("text", "Plain-text part")}
        <span className="flex-1" />
        <button
          type="button"
          onClick={() => setDark((d) => !d)}
          aria-pressed={dark}
          className="px-3 py-1 text-[12px] font-medium transition"
          style={{
            borderRadius: 999,
            background: "var(--bg-panel)",
            color: "var(--fg-muted)",
            border: "1px solid var(--border)",
          }}
        >
          {dark ? "Dark ✓" : "Dark"}
        </button>
      </div>

      {activeTab === "html" && htmlBody ? (
        <iframe
          title="Rendered HTML preview"
          sandbox=""
          srcDoc={dark ? forceDarkPreview(htmlBody) : htmlBody}
          className="w-full"
          style={{
            height: 520,
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
            // Solid canvas behind the document so a transparent-bodied
            // template doesn't show the dashboard theme through.
            background: dark ? "#16181D" : "#FFFFFF",
            colorScheme: dark ? "dark" : "light",
          }}
        />
      ) : (
        <pre
          className="font-mono text-[12px] p-4 overflow-x-auto whitespace-pre-wrap"
          style={{
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
            background: dark ? "#16181D" : "#FFFFFF",
            color: dark ? "#E8EAF0" : "#1F2430",
            maxHeight: 520,
            overflowY: "auto",
            margin: 0,
          }}
        >
          {textBody}
        </pre>
      )}
    </div>
  );
}
