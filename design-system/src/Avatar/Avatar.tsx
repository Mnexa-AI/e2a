export type AvatarProps = {
  /** Display name; used for initials and (if no email) the color seed. */
  name?: string;
  /** Email; used as the color seed and for initials when no name is given. */
  email?: string;
  /** Pixel size of the square. Defaults to 24. */
  size?: number;
};

// Deterministic bucket in [1, 8] from a short string (FNV-ish). The same
// seed always maps to the same --av color, so a person reads consistently
// across the UI.
function hashTo8(input: string): number {
  let h = 0;
  for (let i = 0; i < input.length; i++) {
    h = (h * 31 + input.charCodeAt(i)) | 0;
  }
  return (((h % 8) + 8) % 8) + 1;
}

function initials(name?: string, email?: string): string {
  const trimmed = name?.trim();
  if (trimmed) {
    const parts = trimmed.split(/\s+/);
    if (parts.length >= 2) {
      return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
    }
    return parts[0].slice(0, 2).toUpperCase();
  }
  const local = (email ?? "").split("@")[0] || email || "?";
  return local.slice(0, 2).toUpperCase();
}

/**
 * Square avatar with a deterministic color from the Loft `--av-1…8` palette,
 * seeded by email (or name), showing the person's initials.
 */
export function Avatar({ name, email, size = 24 }: AvatarProps) {
  const seed = (email || name || "").toLowerCase();
  const bucket = hashTo8(seed);
  return (
    <span
      aria-hidden
      style={{
        width: size,
        height: size,
        borderRadius: 4,
        background: `var(--av-${bucket})`,
        color: "#fff",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        fontSize: Math.round(size * 0.42),
        fontWeight: 700,
        flexShrink: 0,
        letterSpacing: "0.02em",
      }}
    >
      {initials(name, email)}
    </span>
  );
}
