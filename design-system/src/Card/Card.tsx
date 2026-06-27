import type { HTMLAttributes } from "react";

export type CardProps = HTMLAttributes<HTMLDivElement>;

/**
 * Surface container — the panel background, border, and radius that wrap most
 * content blocks. Forwards native div props; compose freely inside.
 */
export function Card({ className = "", children, ...props }: CardProps) {
  return (
    <div className={`loft-card ${className}`.trim()} {...props}>
      {children}
    </div>
  );
}
