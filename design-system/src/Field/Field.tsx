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
export function Field({
  label,
  hint,
  value,
  onChange,
  className = "",
  type = "text",
  ...props
}: FieldProps) {
  return (
    <label className={`loft-field ${className}`.trim()}>
      <span className="loft-field__label">{label}</span>
      <input
        {...props}
        type={type}
        className="loft-field__input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      {hint && <span className="loft-field__hint">{hint}</span>}
    </label>
  );
}
