import { type ReactNode } from "react";
export type InkLineKind = "comment" | "prompt" | "string" | "accent" | "plain";
export type InkLine = {
    c?: InkLineKind;
    text: string;
    fg?: string;
    node?: undefined;
} | {
    node: ReactNode;
    c?: undefined;
    text?: undefined;
    fg?: undefined;
};
export type InkConsoleProps = {
    /** Lines to render. Each is either tokenized text (`{ text, c }`) or a raw `{ node }`. */
    lines: InkLine[];
    title?: string;
    lang?: string;
    /** Show the copy button (copies all text lines). Defaults to true. */
    copy?: boolean;
    height?: number | string;
    className?: string;
};
/**
 * Agent-native console surface. Renders on the dark "ink" palette regardless
 * of theme, with syntax-tinted lines and an optional copy button.
 */
export declare function InkConsole({ lines, title, lang, copy, height, className, }: InkConsoleProps): import("react").JSX.Element;
