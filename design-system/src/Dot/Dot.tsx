export type DotTone = "success" | "warn" | "accent" | "danger" | "neutral";

export type DotProps = {
  /** Status color. Defaults to `success`. */
  tone?: DotTone;
};

/** Tiny status dot — decorative, pair it with a text label for meaning. */
export function Dot({ tone = "success" }: DotProps) {
  return <span aria-hidden className={`loft-dot loft-dot--${tone}`} />;
}
