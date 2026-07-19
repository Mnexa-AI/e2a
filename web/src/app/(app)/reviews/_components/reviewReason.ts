import type { HoldReason } from "@/app/components/types";

// The API owns user-facing copy. Keeping this tiny boundary makes it explicit
// that the collapsed queue never reconstructs meaning from code or appends
// technical confidence.
export function holdReasonSummary(
  reason?: HoldReason | null,
): string | null {
  const summary = reason?.summary?.trim();
  return summary || null;
}
