// 20×20 colored tile with an arrow indicating message direction.
// Inbound = success-bg + ↙ arrow; outbound = info-bg + ↗ arrow.
// The arrow path is a single SVG with conditional <path>s so the
// component stays a single render path.

export type Direction = "inbound" | "outbound";

export function MessageDirectionIcon({
  direction,
  size = 14,
}: {
  direction: Direction;
  size?: number;
}) {
  const isIn = direction === "inbound";
  const fg = isIn ? "var(--success)" : "var(--info-strong)";
  const bg = isIn ? "var(--success-bg)" : "var(--info-bg)";
  return (
    <span
      aria-label={direction}
      style={{
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        width: size + 6,
        height: size + 6,
        borderRadius: 4,
        background: bg,
        color: fg,
        flexShrink: 0,
      }}
    >
      <svg
        width={size}
        height={size}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth={2}
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        {isIn ? (
          <>
            <path d="M6 6l12 12" />
            <path d="M18 12v6h-6" />
          </>
        ) : (
          <>
            <path d="M6 18L18 6" />
            <path d="M9 6h9v9" />
          </>
        )}
      </svg>
    </span>
  );
}
