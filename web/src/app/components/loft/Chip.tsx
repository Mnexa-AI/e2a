import type { ReactNode } from "react";

export type ChipTone =
  | "success"
  | "warn"
  | "info"
  | "accent"
  | "danger"
  | "neutral";

const tones: Record<ChipTone, { background: string; color: string }> = {
  success: { background: "var(--success-bg)", color: "var(--success)" },
  warn: { background: "var(--warn-bg)", color: "var(--warn-strong)" },
  info: { background: "var(--info-bg)", color: "var(--info-strong)" },
  accent: { background: "var(--accent-soft)", color: "var(--accent-strong)" },
  danger: { background: "var(--danger-bg)", color: "var(--danger-strong)" },
  neutral: { background: "var(--bg-elev)", color: "var(--fg-muted)" },
};

export type ChipProps = {
  children: ReactNode;
  tone?: ChipTone;
  mono?: boolean;
  className?: string;
};

export function Chip({
  children,
  tone = "neutral",
  mono = false,
  className = "",
}: ChipProps) {
  const t = tones[tone];
  return (
    <span
      className={`inline-flex items-center gap-[5px] px-2 py-[2px] rounded-full text-[11px] font-semibold ${
        mono ? "font-mono tracking-[0.02em]" : "font-sans"
      } ${className}`}
      style={{ background: t.background, color: t.color }}
    >
      {children}
    </span>
  );
}
