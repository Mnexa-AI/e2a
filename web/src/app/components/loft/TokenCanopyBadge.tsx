// The Token Canopy endorsement lockup — the parent-brand attribution that
// sits under a product name ("e2a · by Token Canopy").
//
// The mark is the real brand glyph (design/logo/final/logo-glyph.svg in the
// tokencanopy repo — the live-oak silhouette traced from oak2_crest.jpg),
// copied to public/ rather than redrawn: an approximation of a logo is a
// different logo.
//
// It is painted with a CSS mask instead of <img> so the colour comes from
// --moss and follows the theme. The source file is filled with canopy green
// (#2B4033), which all but vanishes on the dark theme's near-black surface.
//
// Sized at 16px, not the 14px of the surrounding text: the glyph is a
// detailed silhouette with a canopy and root system, and below ~16px it
// closes up into an indistinct blob.

const CANOPY_URL = "https://tokencanopy.com";
const GLYPH_PX = 16;

// The oak mark on its own, for lockups that set their own wordmark.
export function TokenCanopyGlyph({
  size = GLYPH_PX,
  tone = "var(--moss)",
}: {
  size?: number;
  tone?: string;
}) {
  return (
    <span
      aria-hidden
      data-testid="token-canopy-glyph"
      style={{
        width: size,
        height: size,
        flexShrink: 0,
        display: "inline-block",
        background: tone,
        WebkitMaskImage: "url(/token-canopy-glyph.svg)",
        maskImage: "url(/token-canopy-glyph.svg)",
        WebkitMaskRepeat: "no-repeat",
        maskRepeat: "no-repeat",
        WebkitMaskPosition: "center",
        maskPosition: "center",
        WebkitMaskSize: "contain",
        maskSize: "contain",
      }}
    />
  );
}

export function TokenCanopyBadge({
  href = CANOPY_URL,
  className,
}: {
  // Pass null to render as plain text (e.g. somewhere already inside a link,
  // where nesting anchors would be invalid).
  href?: string | null;
  className?: string;
}) {
  const content = (
    <>
      <TokenCanopyGlyph />
      by Token Canopy
    </>
  );

  const style: React.CSSProperties = {
    fontSize: 11,
    letterSpacing: "0.04em",
    color: "var(--fg-muted)",
    textDecoration: "none",
  };

  if (href === null) {
    return (
      <span
        data-testid="token-canopy-badge"
        className={`inline-flex items-center gap-1.5 ${className ?? ""}`}
        style={style}
      >
        {content}
      </span>
    );
  }

  return (
    <a
      data-testid="token-canopy-badge"
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center gap-1.5 transition ${className ?? ""}`}
      style={style}
    >
      {content}
    </a>
  );
}
