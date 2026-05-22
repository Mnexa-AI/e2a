import type { ButtonHTMLAttributes, CSSProperties } from "react";

export type ButtonVariant = "primary" | "ghost" | "mono";

const variantStyle: Record<ButtonVariant, CSSProperties> = {
  primary: {
    fontFamily: "var(--f-ui)",
    fontSize: 13,
    fontWeight: 500,
    padding: "8px 16px",
    borderRadius: "var(--r-md)",
    background: "var(--accent-fill)",
    color: "var(--accent-fg)",
    border: "none",
  },
  ghost: {
    fontFamily: "var(--f-ui)",
    fontSize: 12,
    fontWeight: 500,
    padding: "7px 12px",
    borderRadius: "var(--r-md)",
    background: "var(--bg-panel)",
    color: "var(--fg)",
    border: "1px solid var(--border)",
  },
  mono: {
    fontFamily: "var(--f-mono)",
    fontSize: 11,
    fontWeight: 500,
    padding: "4px 8px",
    borderRadius: "var(--r-sm)",
    background: "var(--ink-elev)",
    color: "var(--ink-fg-muted)",
    border: "1px solid var(--ink-border)",
  },
};

const variantGap: Record<ButtonVariant, string> = {
  primary: "gap-1.5",
  ghost: "gap-1.5",
  mono: "gap-[5px]",
};

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
};

export function Button({
  variant = "primary",
  className = "",
  style,
  type = "button",
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      className={`inline-flex items-center justify-center ${variantGap[variant]} cursor-pointer transition disabled:opacity-50 disabled:cursor-not-allowed ${className}`}
      style={{ ...variantStyle[variant], ...style }}
      {...rest}
    >
      {children}
    </button>
  );
}
