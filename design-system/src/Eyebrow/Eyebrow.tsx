import type { ReactNode } from "react";

export type EyebrowProps = {
  children: ReactNode;
  className?: string;
};

/** Small uppercase mono kicker that sits above a heading. */
export function Eyebrow({ children, className = "" }: EyebrowProps) {
  return (
    <span className={`loft-eyebrow ${className}`.trim()}>{children}</span>
  );
}
