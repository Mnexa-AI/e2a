import type { ReactNode } from "react";

export type Theme = "system" | "light" | "dark";

export type ThemeToggleProps = {
  /** The currently selected theme. */
  value: Theme;
  /** Called when the user picks a different theme. */
  onChange: (theme: Theme) => void;
  className?: string;
};

// Three-way segmented control. Originally consumed a ThemeProvider context;
// extracted here as a controlled component so it has no app dependency —
// wire `value`/`onChange` to your own theme state. Icons are 1.6-weight
// strokes to match the rest of the Loft icon set.
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

export function ThemeToggle({ value, onChange, className = "" }: ThemeToggleProps) {
  return (
    <div
      role="radiogroup"
      aria-label="Color theme"
      className={`loft-seg ${className}`.trim()}
    >
      {OPTIONS.map((opt) => {
        const active = value === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={opt.label}
            title={opt.label}
            onClick={() => onChange(opt.value)}
            className="loft-seg__opt"
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
