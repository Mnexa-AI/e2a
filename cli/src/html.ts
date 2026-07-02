// Derive a plain-text fallback from an HTML email body, so `send --html-file`
// without `--body` always produces a text part (multipart emails should never
// ship an empty text alternative). Crude tag-stripping is fine for this — the
// HTML part is what mail clients actually render.

const NAMED_ENTITIES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
  nbsp: " ",
};

function decodeEntities(s: string): string {
  return s.replace(/&(#x?[0-9a-f]+|[a-z]+);/gi, (match, code: string) => {
    if (code.startsWith("#")) {
      const n =
        code[1]?.toLowerCase() === "x"
          ? parseInt(code.slice(2), 16)
          : parseInt(code.slice(1), 10);
      try {
        return Number.isFinite(n) ? String.fromCodePoint(n) : match;
      } catch {
        return match;
      }
    }
    return NAMED_ENTITIES[code.toLowerCase()] ?? match;
  });
}

export function htmlToText(html: string): string {
  let t = html.replace(/<(script|style)\b[\s\S]*?<\/\1\s*>/gi, " ");
  // Block-level closers and <br> become line breaks so structure survives.
  t = t.replace(/<(?:br|\/p|\/div|\/li|\/tr|\/h[1-6])\s*\/?>/gi, "\n");
  t = t.replace(/<[^>]+>/g, " ");
  t = decodeEntities(t);
  t = t.replace(/\n\s*\n\s*/g, "\n\n");
  t = t.replace(/[ \t]+/g, " ");
  return t.trim();
}
