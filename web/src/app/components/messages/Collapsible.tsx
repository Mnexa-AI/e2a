"use client";

// Header-with-chevron section primitive used by the focus page's
// "Full headers" and "Lifecycle" reveals. Mirrors the design mock:
// 12px / 18px header padding, rotating ▶ glyph, eyebrow label on the
// left, mono meta line on the right, optional bottom divider when open.
//
// Honors `prefers-reduced-motion` — the chevron rotation collapses to
// 0ms instead of the default 120ms transition.

import { useState, useSyncExternalStore, type ReactNode } from "react";
import { Eyebrow } from "@e2a/ui";

export type CollapsibleProps = {
  label: string;
  meta?: ReactNode;
  defaultOpen?: boolean;
  /** Controlled mode — when provided, `open` overrides internal state. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  children: ReactNode;
};

// useSyncExternalStore is the idiomatic way to subscribe to a browser-
// owned value (here, the matchMedia query) without effect ping-pong or
// the React 19 "setState in effect" lint warning. The store function
// triple — subscribe / getSnapshot / getServerSnapshot — gives React
// everything it needs to keep React state in sync with the media query.
function usePrefersReducedMotion(): boolean {
  return useSyncExternalStore(
    subscribeReducedMotion,
    getReducedMotionSnapshot,
    getReducedMotionServerSnapshot,
  );
}

function subscribeReducedMotion(onChange: () => void): () => void {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return () => {};
  }
  const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
  mq.addEventListener("change", onChange);
  return () => mq.removeEventListener("change", onChange);
}
function getReducedMotionSnapshot(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
    return false;
  }
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}
function getReducedMotionServerSnapshot(): boolean {
  return false;
}

export function Collapsible({
  label,
  meta,
  defaultOpen = false,
  open: controlledOpen,
  onOpenChange,
  children,
}: CollapsibleProps) {
  const [uncontrolled, setUncontrolled] = useState(defaultOpen);
  const open = controlledOpen ?? uncontrolled;
  const reduced = usePrefersReducedMotion();

  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setUncontrolled(next);
    onOpenChange?.(next);
  };

  return (
    <section
      style={{
        background: "var(--bg-panel)",
        border: "1px solid var(--border)",
        borderRadius: "var(--r-lg)",
        overflow: "hidden",
      }}
    >
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="w-full flex items-center gap-2.5 text-left"
        style={{
          padding: "12px 18px",
          background: "transparent",
          border: "none",
          borderBottom: open ? "1px solid var(--border-sub)" : "none",
          cursor: "pointer",
        }}
      >
        <span
          aria-hidden
          className="inline-block"
          style={{
            transform: open ? "rotate(90deg)" : "rotate(0deg)",
            transition: reduced ? "none" : "transform 120ms ease",
            color: "var(--fg-subtle)",
            fontSize: 10,
          }}
        >
          ▶
        </span>
        <Eyebrow>{label}</Eyebrow>
        <span className="flex-1" />
        {meta && (
          <span
            style={{
              fontFamily: "var(--f-mono)",
              fontSize: 11,
              color: "var(--fg-subtle)",
            }}
          >
            {meta}
          </span>
        )}
      </button>
      {open && children}
    </section>
  );
}
