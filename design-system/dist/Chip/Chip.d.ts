import type { ReactNode } from "react";
export type ChipTone = "success" | "warn" | "info" | "accent" | "danger" | "neutral";
export type ChipProps = {
    children: ReactNode;
    /** Semantic color. Defaults to `neutral`. */
    tone?: ChipTone;
    /** Render in the monospace face (for ids, codes, statuses). */
    mono?: boolean;
    className?: string;
};
/** Small rounded status/label pill, tinted by semantic tone. */
export declare function Chip({ children, tone, mono, className, }: ChipProps): import("react").JSX.Element;
