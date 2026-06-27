import * as react from 'react';
import { ButtonHTMLAttributes, ReactNode, CSSProperties, InputHTMLAttributes, HTMLAttributes } from 'react';

type ButtonVariant = "primary" | "ghost" | "mono";
type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
    /** Visual style. `primary` = ember fill, `ghost` = bordered, `mono` = ink/console. */
    variant?: ButtonVariant;
};
/**
 * Loft button. Three variants drawn entirely from design tokens, so it
 * re-themes automatically under `.dark`. Forwards all native button props.
 */
declare function Button({ variant, className, type, children, ...rest }: ButtonProps): react.JSX.Element;

type ChipTone = "success" | "warn" | "info" | "accent" | "danger" | "neutral";
type ChipProps = {
    children: ReactNode;
    /** Semantic color. Defaults to `neutral`. */
    tone?: ChipTone;
    /** Render in the monospace face (for ids, codes, statuses). */
    mono?: boolean;
    className?: string;
};
/** Small rounded status/label pill, tinted by semantic tone. */
declare function Chip({ children, tone, mono, className, }: ChipProps): react.JSX.Element;

type DotTone = "success" | "warn" | "accent" | "danger" | "neutral";
type DotProps = {
    /** Status color. Defaults to `success`. */
    tone?: DotTone;
};
/** Tiny status dot — decorative, pair it with a text label for meaning. */
declare function Dot({ tone }: DotProps): react.JSX.Element;

type EyebrowProps = {
    children: ReactNode;
    className?: string;
};
/** Small uppercase mono kicker that sits above a heading. */
declare function Eyebrow({ children, className }: EyebrowProps): react.JSX.Element;

type Theme = "system" | "light" | "dark";
type ThemeToggleProps = {
    /** The currently selected theme. */
    value: Theme;
    /** Called when the user picks a different theme. */
    onChange: (theme: Theme) => void;
    className?: string;
};
declare function ThemeToggle({ value, onChange, className }: ThemeToggleProps): react.JSX.Element;

type InkLineKind = "comment" | "prompt" | "string" | "accent" | "plain";
type InkLine = {
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
type InkConsoleProps = {
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
declare function InkConsole({ lines, title, lang, copy, height, className, }: InkConsoleProps): react.JSX.Element;

type LogoVariant = "wordmark" | "mark";
type LogoTone = "color" | "mono" | "ink";
type LogoProps = {
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
declare function Logo({ variant, tone, height, title, className, style, }: LogoProps): react.JSX.Element;

type FieldProps = {
    /** Label rendered above the input. */
    label: string;
    /** Optional helper text below the input. */
    hint?: string;
    value: string;
    onChange: (value: string) => void;
} & Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "onChange">;
/** Labeled text input with an optional hint. Controlled via `value`/`onChange`. */
declare function Field({ label, hint, value, onChange, className, type, ...props }: FieldProps): react.JSX.Element;

type AvatarProps = {
    /** Display name; used for initials and (if no email) the color seed. */
    name?: string;
    /** Email; used as the color seed and for initials when no name is given. */
    email?: string;
    /** Pixel size of the square. Defaults to 24. */
    size?: number;
};
/**
 * Square avatar with a deterministic color from the Loft `--av-1…8` palette,
 * seeded by email (or name), showing the person's initials.
 */
declare function Avatar({ name, email, size }: AvatarProps): react.JSX.Element;

type CollapsibleProps = {
    /** Eyebrow label on the left of the trigger. */
    label: string;
    /** Optional mono meta line on the right of the trigger. */
    meta?: ReactNode;
    defaultOpen?: boolean;
    /** Controlled mode — when provided, `open` overrides internal state. */
    open?: boolean;
    onOpenChange?: (open: boolean) => void;
    children: ReactNode;
};
/** Header-with-chevron disclosure section. Controlled or uncontrolled. */
declare function Collapsible({ label, meta, defaultOpen, open: controlledOpen, onOpenChange, children, }: CollapsibleProps): react.JSX.Element;

type CardProps = HTMLAttributes<HTMLDivElement>;
/**
 * Surface container — the panel background, border, and radius that wrap most
 * content blocks. Forwards native div props; compose freely inside.
 */
declare function Card({ className, children, ...props }: CardProps): react.JSX.Element;

export { Avatar, type AvatarProps, Button, type ButtonProps, type ButtonVariant, Card, type CardProps, Chip, type ChipProps, type ChipTone, Collapsible, type CollapsibleProps, Dot, type DotProps, type DotTone, Eyebrow, type EyebrowProps, Field, type FieldProps, InkConsole, type InkConsoleProps, type InkLine, type InkLineKind, Logo, type LogoProps, type LogoTone, type LogoVariant, type Theme, ThemeToggle, type ThemeToggleProps };
