"use client";

import { useState, useSyncExternalStore, type ReactNode } from "react";
import { Eyebrow } from "../Eyebrow/Eyebrow";

export type CollapsibleProps = {
  /** Eyebrow label on the left of the trigger. */
  label: string;
  /** Optional mono meta line on the right of the trigger. */
  meta?: ReactNode;
  defaultOpen?: boolean;
  /** Controlled mode — when provided, `open` overrides internal state. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  children: ReactNode;
};

// useSyncExternalStore subscribes to the matchMedia query without effect
// ping-pong — keeps the chevron's animation honest to prefers-reduced-motion.
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

/** Header-with-chevron disclosure section. Controlled or uncontrolled. */
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
    <section className="loft-collapsible">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className={`loft-collapsible__trigger${open ? " loft-collapsible__trigger--open" : ""}`}
      >
        <span
          aria-hidden
          className="loft-collapsible__chevron"
          style={{
            transform: open ? "rotate(90deg)" : "rotate(0deg)",
            transition: reduced ? "none" : "transform 120ms ease",
          }}
        >
          ▶
        </span>
        <Eyebrow>{label}</Eyebrow>
        <span className="loft-collapsible__spacer" />
        {meta && <span className="loft-collapsible__meta">{meta}</span>}
      </button>
      {open && children}
    </section>
  );
}
