import { type ReactNode } from "react";
export type CollapsibleProps = {
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
export declare function Collapsible({ label, meta, defaultOpen, open: controlledOpen, onOpenChange, children, }: CollapsibleProps): import("react").JSX.Element;
