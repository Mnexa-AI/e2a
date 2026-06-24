"use client";

import Link from "next/link";

export function AgentsEmptyState() {
  return (
    <div
      className="p-12 text-center"
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
      }}
    >
      <h3
        className="mb-2"
        style={{
          fontFamily: "var(--f-ui)",
          fontSize: 22,
          fontWeight: 700,
          letterSpacing: "-0.012em",
          color: "var(--fg)",
        }}
      >
        No inboxes yet
      </h3>
      <p
        className="mb-6 max-w-md mx-auto text-[14px] leading-[1.6]"
        style={{ color: "var(--fg-muted)" }}
      >
        Create an inbox — an email address for your agent — on a shared e2a
        domain or your own custom domain.
      </p>
      <div className="flex items-center justify-center gap-3 flex-wrap">
        <Link
          href="/get-started"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium px-4 py-2 transition"
          style={{
            background: "var(--accent-fill)",
            color: "var(--accent-fg)",
            borderRadius: "var(--r-md)",
          }}
        >
          Create your first inbox
          <span className="font-mono">→</span>
        </Link>
        <Link
          href="/domains"
          className="inline-flex items-center px-4 py-2 text-[13px] font-medium transition"
          style={{
            background: "var(--bg-panel)",
            color: "var(--fg)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
          }}
        >
          Set up a domain
        </Link>
      </div>
    </div>
  );
}
