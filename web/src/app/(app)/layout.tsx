"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { useAuth } from "../components/AuthProvider";
import { SignInLink } from "../components/SignInLink";
import { Sidebar } from "../components/loft/Sidebar";

export default function AppLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const { user, loading } = useAuth();
  const [mobileNavOpen, setMobileNavOpen] = useState(false);

  const closeMobileNav = useCallback(() => setMobileNavOpen(false), []);

  useEffect(() => {
    if (!mobileNavOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") closeMobileNav();
    };
    document.addEventListener("keydown", onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [mobileNavOpen, closeMobileNav]);

  if (loading) {
    return (
      <div
        className="min-h-screen flex items-center justify-center"
        style={{ background: "var(--bg)", color: "var(--fg)" }}
      >
        <p className="text-[13px]" style={{ color: "var(--fg-muted)" }}>
          Loading...
        </p>
      </div>
    );
  }

  if (!user) {
    return (
      <div
        className="min-h-screen flex items-center justify-center px-6"
        style={{ background: "var(--bg)", color: "var(--fg)" }}
      >
        <div className="text-center">
          <p className="mb-4 text-[14px]" style={{ color: "var(--fg-muted)" }}>
            Sign in to access this page.
          </p>
          <SignInLink
            className="inline-block px-4 py-2 text-[13px] font-medium transition"
            style={{
              background: "var(--accent-fill)",
              color: "var(--accent-fg)",
              borderRadius: "var(--r-md)",
            }}
          >
            Sign in with Google
          </SignInLink>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen" style={{ background: "var(--bg)" }}>
      {/* Desktop sidebar */}
      <Sidebar />

      {/* Mobile slide-in sidebar */}
      {mobileNavOpen && (
        <>
          <button
            type="button"
            aria-label="Close menu"
            onClick={closeMobileNav}
            className="md:hidden fixed inset-0 z-40"
            style={{ background: "rgba(26,23,20,0.4)" }}
          />
          {/*
            Delegated click — close the sheet when the user taps any nav
            link inside it. We can't reach into the Sidebar primitive for
            per-link onClick handlers, so this catches every <a> in the
            tree on the way up.
          */}
          <div
            className="md:hidden fixed inset-y-0 left-0 z-50 w-[280px]"
            onClick={(e) => {
              if ((e.target as HTMLElement).closest("a")) closeMobileNav();
            }}
          >
            <Sidebar className="flex flex-col" />
          </div>
        </>
      )}

      <main className="flex-1 min-w-0 flex flex-col">
        {/* Mobile header */}
        <div
          className="md:hidden flex items-center justify-between px-4 py-3 sticky top-0 z-30"
          style={{
            background: "var(--bg-panel)",
            borderBottom: "1px solid var(--border)",
          }}
        >
          <Link
            href="/"
            className="flex items-center gap-2"
            style={{ color: "var(--fg)" }}
          >
            <span
              className="flex items-center justify-center font-mono font-bold text-[12px]"
              style={{
                width: 28,
                height: 28,
                borderRadius: 6,
                background: "var(--fg)",
                color: "var(--bg)",
                letterSpacing: "-0.04em",
              }}
            >
              e2a
            </span>
          </Link>
          <button
            type="button"
            onClick={() => setMobileNavOpen(true)}
            aria-label="Open menu"
            className="inline-flex items-center justify-center"
            style={{
              width: 36,
              height: 36,
              borderRadius: "var(--r-md)",
              border: "1px solid var(--border)",
              background: "var(--bg-panel)",
              color: "var(--fg)",
            }}
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden
            >
              <line x1="3" y1="6" x2="21" y2="6" />
              <line x1="3" y1="12" x2="21" y2="12" />
              <line x1="3" y1="18" x2="21" y2="18" />
            </svg>
          </button>
        </div>

        <div className="flex-1 overflow-auto">{children}</div>
      </main>
    </div>
  );
}
