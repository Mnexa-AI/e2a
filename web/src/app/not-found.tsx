"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

export default function NotFound() {
  const pathname = usePathname() ?? "";
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
          fontSize: "clamp(120px, 20vw, 200px)",
          color: "var(--accent-strong)",
          letterSpacing: "-0.03em",
        }}
      >
        404
      </div>
      <p
        className="mt-2 mb-6"
        style={{
          fontFamily: "var(--f-ui)",
          fontWeight: 600,
          fontSize: "clamp(22px, 3vw, 30px)",
          color: "var(--fg)",
          letterSpacing: "-0.01em",
          lineHeight: 1.2,
        }}
      >
        That page isn&apos;t home.
      </p>
      <p
        className="font-mono text-[12px] mb-7 max-w-md text-center break-all"
        style={{ color: "var(--fg-subtle)" }}
      >
        {pathname || "/"}
      </p>
      <Link
        href="/dashboard"
        className="inline-flex items-center gap-2 px-4 py-2.5 text-[14px] font-medium"
        style={{
          background: "var(--accent-fill)",
          color: "var(--accent-fg)",
          borderRadius: "var(--r-md)",
        }}
      >
        Back to dashboard
        <span className="font-mono">→</span>
      </Link>
    </div>
  );
}
