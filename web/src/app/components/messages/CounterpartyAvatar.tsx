// Square avatar with a deterministic --av-1..8 background.
//
// Hashed from the counterparty email so the same address always renders
// the same color across the dashboard — useful for scanning the activity
// table by sender. We use a simple FNV-ish hash because the input is
// short and we only need a stable bucket in [1, 8].

function hashTo8(input: string): number {
  let h = 0;
  for (let i = 0; i < input.length; i++) {
    h = (h * 31 + input.charCodeAt(i)) | 0;
  }
  return ((h % 8) + 8) % 8 + 1;
}

function initials(email: string, name?: string): string {
  if (name) {
    const parts = name.trim().split(/\s+/);
    if (parts.length >= 2) return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
    return parts[0].slice(0, 2).toUpperCase();
  }
  const local = email.split("@")[0] || email;
  return local.slice(0, 2).toUpperCase();
}

export type CounterpartyAvatarProps = {
  email: string;
  name?: string;
  size?: number;
};

export function CounterpartyAvatar({
  email,
  name,
  size = 22,
}: CounterpartyAvatarProps) {
  const bucket = hashTo8(email.toLowerCase());
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
      {initials(email, name)}
    </span>
  );
}
