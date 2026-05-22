import type { ReactNode } from "react";

export function Eyebrow({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={`font-mono text-[11px] font-semibold uppercase tracking-[0.08em] ${className}`}
      style={{ color: "var(--accent-strong)" }}
    >
      {children}
    </span>
  );
}
