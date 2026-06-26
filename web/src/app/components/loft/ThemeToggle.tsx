"use client";

import type { ReactNode } from "react";
import { useTheme, type Theme } from "../ThemeProvider";

// Three-way segmented control mirroring the ThemeProvider's Theme union.
// "system" follows the OS `prefers-color-scheme`; "light"/"dark" pin it.
// Icons are 1.6-weight strokes to match the sidebar nav glyphs.
const OPTIONS: { value: Theme; label: string; icon: ReactNode }[] = [
  {
    value: "system",
    label: "System theme",
    icon: (
      <>
        <rect x="3" y="4" width="18" height="12" rx="2" />
        <path d="M8 20h8M12 16v4" />
      </>
    ),
  },
  {
    value: "light",
    label: "Light theme",
    icon: (
      <>
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
      </>
    ),
  },
  {
    value: "dark",
    label: "Dark theme",
    icon: <path d="M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z" />,
  },
];

export function ThemeToggle() {
  const { theme, setTheme } = useTheme();

  return (
    <div
      role="radiogroup"
      aria-label="Color theme"
      className="flex items-center gap-0.5 p-0.5"
      style={{
        border: "1px solid var(--border-sub)",
        borderRadius: "var(--r-md)",
      }}
    >
      {OPTIONS.map((opt) => {
        const active = theme === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={opt.label}
            title={opt.label}
            onClick={() => setTheme(opt.value)}
            className="flex-1 flex items-center justify-center py-1.5 transition"
            style={{
              borderRadius: "calc(var(--r-md) - 2px)",
              color: active ? "var(--fg)" : "var(--fg-muted)",
              background: active ? "var(--bg-elev)" : "transparent",
              boxShadow: active ? "inset 0 0 0 1px var(--border)" : "none",
            }}
          >
            <svg
              width="15"
              height="15"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden
            >
              {opt.icon}
            </svg>
          </button>
        );
      })}
    </div>
  );
}
