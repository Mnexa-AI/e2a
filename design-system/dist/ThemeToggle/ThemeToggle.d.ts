export type Theme = "system" | "light" | "dark";
export type ThemeToggleProps = {
    /** The currently selected theme. */
    value: Theme;
    /** Called when the user picks a different theme. */
    onChange: (theme: Theme) => void;
    className?: string;
};
export declare function ThemeToggle({ value, onChange, className }: ThemeToggleProps): import("react").JSX.Element;
