export type AvatarProps = {
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
export declare function Avatar({ name, email, size }: AvatarProps): import("react").JSX.Element;
