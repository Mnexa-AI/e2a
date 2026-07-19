import type { HoldReason } from "@/app/components/types";

function humanizeCode(code: string): string {
  const spaced = code.replace(/[_-]/g, " ").trim();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

const THREAT_CATEGORY_LABELS: Record<string, string> = {
  "prompt-injection": "Prompt injection",
  jailbreak: "Jailbreak attempt",
  "data-exfiltration": "Data exfiltration",
  "credential-phishing": "Credential phishing",
  phishing: "Phishing",
  malware: "Malware",
  "social-engineering": "Social engineering",
};

export function categoryLabel(name: string): string {
  const key = name.toLowerCase().replace(/_/g, "-");
  const mapped = THREAT_CATEGORY_LABELS[key];
  return typeof mapped === "string" ? mapped : humanizeCode(name);
}

// The API owns user-facing copy. The queue never reconstructs meaning from
// code or appends technical confidence.
export function holdReasonSummary(
  reason?: HoldReason | null,
): string | null {
  const summary = reason?.summary?.trim();
  return summary || null;
}
