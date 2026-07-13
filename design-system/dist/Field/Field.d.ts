import type { InputHTMLAttributes } from "react";
export type FieldProps = {
    /** Label rendered above the input. */
    label: string;
    /** Optional helper text below the input. */
    hint?: string;
    value: string;
    onChange: (value: string) => void;
} & Omit<InputHTMLAttributes<HTMLInputElement>, "value" | "onChange">;
/** Labeled text input with an optional hint. Controlled via `value`/`onChange`. */
export declare function Field({ label, hint, value, onChange, className, type, ...props }: FieldProps): import("react").JSX.Element;
