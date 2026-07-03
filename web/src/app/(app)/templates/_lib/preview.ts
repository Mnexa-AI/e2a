// Client-side preview helpers for the templates surface.
//
// The starter gallery previews a master by substituting each
// {{var}} / {{{var}}} in the VERBATIM sources fetched from
// GET /v1/starter-templates/{alias} with the variable's catalog
// `example`. This is a display-only approximation of the server-side
// engine (internal/emailtemplate) — flat variables only, {{x}} is
// HTML-escaped in the HTML part, {{{x}}} inserts raw. The layout source
// is always the master returned by the API, never a local re-creation.

const RAW_VAR = /\{\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}\}/g;
const ESC_VAR = /\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}/g;

export function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/**
 * Substitute template variables into a source string. Missing variables
 * render as empty strings (matching the engine). Set `escape` for HTML
 * parts so {{x}} slots are HTML-escaped while {{{x}}} slots stay raw.
 */
export function substituteVars(
  source: string,
  vars: Record<string, string>,
  opts?: { escape?: boolean },
): string {
  // Triple-brace (raw) first — otherwise the double-brace pattern would
  // partially consume `{{{x}}}` and leave stray braces behind.
  return source
    .replace(RAW_VAR, (_m, name: string) => vars[name] ?? "")
    .replace(ESC_VAR, (_m, name: string) => {
      const v = vars[name] ?? "";
      return opts?.escape ? escapeHTML(v) : v;
    });
}

/**
 * Force a preview document into dark mode.
 *
 * Setting `color-scheme: dark` on the iframe (or a wrapper) does NOT flip
 * `@media (prefers-color-scheme: dark)` inside the document — that query
 * follows the OS/browser preference and can't be forced from CSS alone.
 * So for the display copy we mechanically rewrite the media condition to
 * `@media all`, which makes the masters' dark-mode overrides (they use
 * !important) apply unconditionally. We also inject `color-scheme: dark`
 * so documents without explicit dark styles at least get UA dark
 * defaults. This transform is display-only; the stored/copied source is
 * untouched.
 */
export function forceDarkPreview(html: string): string {
  const rewritten = html.replace(
    /@media\s*\(\s*prefers-color-scheme\s*:\s*dark\s*\)/gi,
    "@media all",
  );
  const darkRoot = "<style>:root{color-scheme:dark}</style>";
  if (/<\/head>/i.test(rewritten)) {
    return rewritten.replace(/<\/head>/i, `${darkRoot}</head>`);
  }
  return darkRoot + rewritten;
}

/** Build a name→example map from a starter's variable catalog. */
export function exampleData(
  variables: Array<{ name: string; example: string }>,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const v of variables) out[v.name] = v.example;
  return out;
}
