"use client";

// A selectable option card used by the onboarding choosers (setup method,
// address type). Neutral border by default — a "Recommended" chip in the
// content marks the suggested option, NOT a standing accent border (which
// reads as "stuck selected"). Hover or selection adds the accent border + an
// elevated background. Inline `background`/`border` win over Tailwind `hover:`
// utilities, so the hover state is driven from React state. Border width is a
// constant 2px in both states so hovering never shifts layout.

import { useState } from "react";

export function SelectableCard({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  const [hovered, setHovered] = useState(false);
  const accent = active || hovered;
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className="relative text-left transition focus:outline-none"
      style={{
        background: hovered ? "var(--bg-elev)" : "var(--bg-panel)",
        border: accent ? "2px solid var(--accent)" : "2px solid var(--border)",
        borderRadius: "var(--r-lg)",
        padding: 18,
        cursor: "pointer",
      }}
    >
      {children}
    </button>
  );
}
