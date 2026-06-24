"use client";

import Link from "next/link";
import { useEffect } from "react";

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    // Surface unexpected exceptions for ops to find — Next.js already
    // reports them to its own telemetry layer, this is just a stop-gap
    // until we wire Sentry/PostHog.
    console.error(error);
  }, [error]);

  return (
    <div
      className="min-h-screen flex flex-col items-center justify-center px-6 py-12"
      style={{
        background: "var(--bg)",
        color: "var(--fg)",
        fontFamily: "var(--f-ui)",
      }}
    >
      <div
        className="leading-none"
        style={{
          fontFamily: "var(--f-ui)",
          fontWeight: 700,
          fontSize: "clamp(96px, 16vw, 160px)",
          color: "var(--accent-strong)",
          letterSpacing: "-0.03em",
        }}
      >
        500
      </div>
      <p
        className="mt-2 mb-4"
        style={{
          fontFamily: "var(--f-ui)",
          fontWeight: 600,
          fontSize: "clamp(22px, 3vw, 30px)",
          color: "var(--fg)",
          letterSpacing: "-0.01em",
          lineHeight: 1.2,
        }}
      >
        Something went sideways.
      </p>
      <p
        className="text-[13px] max-w-md text-center mb-6 leading-[1.6]"
        style={{ color: "var(--fg-muted)" }}
      >
        We logged the error. Try again — it might have been a hiccup.
      </p>
      {error.digest && (
        <p
          className="font-mono text-[11px] mb-6"
          style={{ color: "var(--fg-subtle)" }}
        >
          digest: {error.digest}
        </p>
      )}
      <div className="flex flex-wrap gap-2 justify-center">
        <button
          type="button"
          onClick={reset}
          className="inline-flex items-center px-4 py-2.5 text-[14px] font-medium"
          style={{
            background: "var(--accent-fill)",
            color: "var(--accent-fg)",
            borderRadius: "var(--r-md)",
          }}
        >
          Try again
        </button>
        <Link
          href="/inboxes"
          className="inline-flex items-center px-4 py-2.5 text-[14px] font-medium"
          style={{
            background: "var(--bg-panel)",
            color: "var(--fg)",
            border: "1px solid var(--border)",
            borderRadius: "var(--r-md)",
          }}
        >
          Go to dashboard
        </Link>
      </div>
    </div>
  );
}
