export type DotTone = "success" | "warn" | "accent" | "danger" | "neutral";

const colors: Record<DotTone, string> = {
  success: "var(--success)",
  warn: "var(--warn)",
  accent: "var(--accent)",
  danger: "var(--danger)",
  neutral: "var(--fg-subtle)",
};

export function Dot({ tone = "success" }: { tone?: DotTone }) {
  return (
    <span
      aria-hidden
      className="inline-block w-[7px] h-[7px] rounded-full"
      style={{ background: colors[tone] }}
    />
  );
}
