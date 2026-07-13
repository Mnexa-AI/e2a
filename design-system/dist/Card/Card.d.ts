import type { HTMLAttributes } from "react";
export type CardProps = HTMLAttributes<HTMLDivElement>;
/**
 * Surface container — the panel background, border, and radius that wrap most
 * content blocks. Forwards native div props; compose freely inside.
 */
export declare function Card({ className, children, ...props }: CardProps): import("react").JSX.Element;
