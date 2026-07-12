// Pure helpers for the pending-review draft-edit flow. Kept separate
// from PendingDetailPanel.tsx so they're unit-testable without rendering
// the component.

import type {
  PendingMessageDetail,
} from "../../../components/types";
import type { ApprovePayload } from "../../../components/onboarding/api";

// parseCSV splits a comma-separated address string into a trimmed,
// non-empty list. Mirrors how the textarea recipients fields are
// serialised back to the API.
export function parseCSV(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter((x) => x.length > 0);
}

export function joinCSV(xs?: string[]): string {
  return (xs ?? []).join(", ");
}

// diffApproveEdits compares the reviewer's form state against the
// originally-loaded message and returns ONLY the fields that changed,
// shaped for POST /v1/reviews/{id}/approve.
//
// Rationale for diffing instead of always sending the full draft:
// the server's PendingApprovalEdit.Apply treats a present field as
// "use this value" (including empty string → clear the field). If we
// always sent the full draft, a reviewer who never touched the body
// would risk clearing it on rare edge cases (autosave, browser
// autofill blanking a field, etc.). Sending only-changed-fields keeps
// untouched fields at their agent-authored original.
export function diffApproveEdits(
  current: PendingMessageDetail,
  draft: {
    subject: string;
    bodyText: string;
    bodyHTML: string;
    to: string;
    cc: string;
    bcc: string;
  },
): ApprovePayload {
  const out: ApprovePayload = {};
  if (draft.subject !== (current.subject ?? "")) out.subject = draft.subject;
  if (draft.bodyText !== (current.body_text ?? ""))
    out.text = draft.bodyText;
  if (draft.bodyHTML !== (current.body_html ?? ""))
    out.html = draft.bodyHTML;

  const toDraft = parseCSV(draft.to);
  if (JSON.stringify(toDraft) !== JSON.stringify(current.to ?? []))
    out.to = toDraft;

  const ccDraft = parseCSV(draft.cc);
  if (JSON.stringify(ccDraft) !== JSON.stringify(current.cc ?? []))
    out.cc = ccDraft;

  const bccDraft = parseCSV(draft.bcc);
  if (JSON.stringify(bccDraft) !== JSON.stringify(current.bcc ?? []))
    out.bcc = bccDraft;

  return out;
}
