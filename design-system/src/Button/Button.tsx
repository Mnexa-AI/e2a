import type { ButtonHTMLAttributes } from "react";

export type ButtonVariant = "primary" | "ghost" | "mono";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  /** Visual style. `primary` = ember fill, `ghost` = bordered, `mono` = ink/console. */
  variant?: ButtonVariant;
};

/**
 * Loft button. Three variants drawn entirely from design tokens, so it
 * re-themes automatically under `.dark`. Forwards all native button props.
 */
export function Button({
  variant = "primary",
  className = "",
  type = "button",
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      className={`loft-btn loft-btn--${variant} ${className}`.trim()}
      {...rest}
    >
      {children}
    </button>
  );
}
