import type { ReactNode } from "react";

export type ChipTone =
  | "success"
  | "warn"
  | "info"
  | "accent"
  | "danger"
  | "neutral";

export type ChipProps = {
  children: ReactNode;
  /** Semantic color. Defaults to `neutral`. */
  tone?: ChipTone;
  /** Render in the monospace face (for ids, codes, statuses). */
  mono?: boolean;
  className?: string;
};

/** Small rounded status/label pill, tinted by semantic tone. */
export function Chip({
  children,
  tone = "neutral",
  mono = false,
  className = "",
}: ChipProps) {
  return (
    <span
      className={`loft-chip loft-chip--${tone}${mono ? " loft-chip--mono" : ""} ${className}`.trim()}
    >
      {children}
    </span>
  );
}
