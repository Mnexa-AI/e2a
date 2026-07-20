// Which attachments the in-app viewer can render, by content type.
//
// Deliberately narrow: only the types a browser renders natively from a
// blob: URL with no plugin and no parsing of our own. Everything else stays a
// download — an honest "we can't show this" beats a broken embed.

export type PreviewKind = "image" | "pdf" | null;

// `content_type` arrives straight off the MIME part, so it may carry
// parameters ("image/jpeg; name=x.jpg") or odd casing.
export function previewKind(contentType?: string): PreviewKind {
  const type = (contentType ?? "").toLowerCase().split(";")[0].trim();
  if (type === "application/pdf") return "pdf";
  // SVG is included: rendered via <img>, which cannot execute the scripts an
  // inline <svg> or an <object> embed would.
  if (type.startsWith("image/")) return "image";
  return null;
}

export function canPreview(contentType?: string): boolean {
  return previewKind(contentType) !== null;
}
