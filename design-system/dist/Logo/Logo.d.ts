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
/**
 * The e2a logo. One themeable component covering the wordmark and the boxed
 * monogram; the `color` tone is drawn from Loft tokens, so it adapts to light
 * and dark automatically. `mono` follows `currentColor` for one-color contexts.
 */
export declare function Logo({ variant, tone, height, title, className, style, }: LogoProps): import("react").JSX.Element;
