// Contract for deriveStatusChip — the
// (direction, hitl_status, webhook_status, inbox_status) → chip tone
// mapping. Mocking-free pure-function test so the README's status
// chip table has a corresponding assertion in code.

import { deriveStatusChip } from "./MessageStatusChip";

describe("deriveStatusChip", () => {
  it("outbound pending_approval → warn 'Pending' with dot", () => {
    expect(
      deriveStatusChip({ direction: "outbound", hitl_status: "pending_approval" }),
    ).toEqual({ tone: "warn", label: "Pending", dot: true });
  });

  it("outbound sent → success 'Sent'", () => {
    expect(deriveStatusChip({ direction: "outbound", hitl_status: "sent" })).toEqual({
      tone: "success",
      label: "Sent",
    });
  });

  it("outbound rejected → danger 'Rejected'", () => {
    expect(
      deriveStatusChip({ direction: "outbound", hitl_status: "rejected" }),
    ).toEqual({ tone: "danger", label: "Rejected" });
  });

  it("outbound expired_approved → success 'Sent (auto)'", () => {
    expect(
      deriveStatusChip({ direction: "outbound", hitl_status: "expired_approved" }),
    ).toEqual({ tone: "success", label: "Sent (auto)" });
  });

  it("outbound expired_rejected → danger 'Auto-rejected'", () => {
    expect(
      deriveStatusChip({ direction: "outbound", hitl_status: "expired_rejected" }),
    ).toEqual({ tone: "danger", label: "Auto-rejected" });
  });

  it("webhook_status='failed' overrides outbound hitl_status with danger 'Failed'", () => {
    // Even a 'sent' message gets Failed if the webhook gave up — the
    // delivery is what the dashboard surfaces, not the send state.
    expect(
      deriveStatusChip({
        direction: "outbound",
        hitl_status: "sent",
        webhook_status: "failed",
      }),
    ).toEqual({ tone: "danger", label: "Failed", dot: true });
  });

  // Theoretical edge case the data model can't currently produce
  // (delivery only fires post-approval), but pin the precedence so a
  // future change to that invariant doesn't silently downgrade the
  // alarm. webhook_status='failed' wins over hitl_status='pending_approval':
  // an op who sees Failed knows something needs investigation, where
  // Pending would just look like normal queue depth.
  it("webhook_status='failed' dominates HITL pending_approval", () => {
    expect(
      deriveStatusChip({
        direction: "outbound",
        hitl_status: "pending_approval",
        webhook_status: "failed",
      }),
    ).toEqual({ tone: "danger", label: "Failed", dot: true });
  });

  it("inbound unread → info 'Unread' (no dot — passive label)", () => {
    expect(
      deriveStatusChip({ direction: "inbound", inbox_status: "unread" }),
    ).toEqual({ tone: "info", label: "Unread" });
  });

  it("inbound read → neutral 'Read'", () => {
    expect(
      deriveStatusChip({ direction: "inbound", inbox_status: "read" }),
    ).toEqual({ tone: "neutral", label: "Read" });
  });
});
