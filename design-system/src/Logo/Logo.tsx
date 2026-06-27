import type { CSSProperties } from "react";

export type LogoVariant = "wordmark" | "mark";
export type LogoTone = "color" | "mono" | "ink";

export type LogoProps = {
  /** `wordmark` = the "e2a" lockup; `mark` = the boxed "2" monogram. */
  variant?: LogoVariant;
  /**
   * `color` — foreground + ember accent, theme-aware.
   * `mono`  — single `currentColor` (inherits text color).
   * `ink`   — light lockup on the dark ink panel.
   */
  tone?: LogoTone;
  /** Rendered height in px; width derives from the aspect ratio. */
  height?: number;
  /** Accessible label. */
  title?: string;
  className?: string;
  style?: CSSProperties;
};

// Shared with the brand SVGs in web/public — Geist first, then graceful fallbacks.
const FONT =
  "var(--f-ui), 'Inter', ui-sans-serif, system-ui, -apple-system, 'Helvetica Neue', Arial, sans-serif";

// CSS custom properties only resolve in the `style` (CSS) layer, not in SVG
// presentation attributes — so token fills go through `style`, not `fill=""`.
const fillStyle = (value: string): CSSProperties => ({ fill: value });

/**
 * The e2a logo. One themeable component covering the wordmark and the boxed
 * monogram; the `color` tone is drawn from Loft tokens, so it adapts to light
 * and dark automatically. `mono` follows `currentColor` for one-color contexts.
 */
export function Logo({
  variant = "wordmark",
  tone = "color",
  height,
  title = "e2a",
  className,
  style,
}: LogoProps) {
  const mono = tone === "mono";

  if (variant === "mark") {
    const h = height ?? 32;
    return (
      <svg
        role="img"
        aria-label={title}
        className={className}
        style={style}
        width={h}
        height={h}
        viewBox="0 0 256 256"
      >
        <rect
          width="256"
          height="256"
          rx="56"
          style={
            mono
              ? { fill: "none", stroke: "currentColor", strokeWidth: 12 }
              : { fill: "var(--ink)" }
          }
        />
        <text
          x="128"
          y="178"
          textAnchor="middle"
          fontWeight={700}
          fontSize={200}
          letterSpacing={-12}
          style={{ ...fillStyle(mono ? "currentColor" : "var(--ink-fg)"), fontFamily: FONT }}
        >
          2
        </text>
      </svg>
    );
  }

  // wordmark — aspect ratio 640 × 200 = 3.2
  const h = height ?? 24;
  const w = h * 3.2;
  const ink = tone === "ink";
  const textFill = mono ? "currentColor" : ink ? "var(--ink-fg)" : "var(--fg)";
  const twoFill = mono ? "currentColor" : "var(--accent)";
  return (
    <svg
      role="img"
      aria-label={title}
      className={className}
      style={style}
      width={w}
      height={h}
      viewBox="0 0 640 200"
    >
      {ink && <rect width="640" height="200" style={fillStyle("var(--ink)")} />}
      <text
        x="320"
        y="148"
        textAnchor="middle"
        fontWeight={600}
        fontSize={176}
        letterSpacing={-12}
        style={{ ...fillStyle(textFill), fontFamily: FONT }}
      >
        <tspan>e</tspan>
        <tspan style={fillStyle(twoFill)}>2</tspan>
        <tspan>a</tspan>
      </text>
    </svg>
  );
}
