// Bridging helpers between the WIRE shape of template data (nested objects,
// the shape the server's renderer resolves dot paths against) and the edit
// page's DISPLAY shape (a flat map keyed by the dotted variable name, one
// text input per variable).
//
// suggested_data from POST /v1/templates/validate is nested — {{user.name}}
// yields {user: {name: "user.name_value"}} — so the page flattens it into
// dotted display keys, and nests the user's flat inputs back into an object
// before posting them as test_data.

import type { SuggestedData } from "./types";

function isPlainObject(v: unknown): v is SuggestedData {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// flattenSuggested turns nested suggested_data into a flat map keyed by
// dotted paths: {user: {name: "x"}} → {"user.name": "x"}. Non-string leaves
// are stringified defensively (the server only emits string placeholders).
export function flattenSuggested(
  nested: SuggestedData,
  prefix = "",
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(nested)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (isPlainObject(value)) {
      Object.assign(out, flattenSuggested(value, path));
    } else {
      out[path] = String(value);
    }
  }
  return out;
}

// nestTestData turns the flat dotted display map back into the nested
// object the renderer expects: {"user.name": "x"} → {user: {name: "x"}}.
// On a conflict (a scalar already claiming an intermediate segment) the
// first writer wins, mirroring the server's suggestPlaceholder.
export function nestTestData(
  flat: Record<string, string>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [dotted, value] of Object.entries(flat)) {
    const segs = dotted.split(".");
    let cur = out;
    let conflict = false;
    for (const seg of segs.slice(0, -1)) {
      const existing = cur[seg];
      if (isPlainObject(existing)) {
        cur = existing as Record<string, unknown>;
        continue;
      }
      if (existing !== undefined) {
        conflict = true; // a scalar already claims this segment
        break;
      }
      const next: Record<string, unknown> = {};
      cur[seg] = next;
      cur = next;
    }
    if (conflict) continue;
    const leaf = segs[segs.length - 1];
    if (cur[leaf] === undefined) {
      cur[leaf] = value;
    }
  }
  return out;
}
