import { deriveStatusChip } from "./MessageStatusChip";

describe("deriveStatusChip", () => {
  it.each([
    ["accepted", "info", "Queued", true],
    ["sending", "info", "Sending", true],
    ["deferred", "warn", "Delayed", true],
    ["failed", "danger", "Failed", true],
    ["bounced", "danger", "Bounced", true],
    ["complained", "danger", "Complaint", true],
    ["sent", "success", "Sent", false],
    ["delivered", "success", "Delivered", false],
  ] as const)(
    "maps outbound delivery_status=%s to %s %s",
    (delivery_status, tone, label, attention) => {
      expect(deriveStatusChip({ direction: "outbound", delivery_status })).toEqual({
        tone,
        label,
        attention,
      });
    },
  );

  it("gives pending review precedence over delivery state", () => {
    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "delivered",
        review_status: "pending_review",
      }),
    ).toEqual({
      tone: "warn",
      label: "Pending review",
      dot: true,
      attention: true,
    });
  });

  it.each([
    ["review_rejected", "Rejected"],
    ["review_expired_rejected", "Auto-rejected"],
  ] as const)("gives %s precedence over delivery state", (review_status, label) => {
    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "delivered",
        review_status,
      }),
    ).toEqual({ tone: "danger", label, attention: true });
  });

  it("lets delivery_status=accepted override collapsed review_status=sent", () => {
    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "accepted",
        review_status: "sent",
      }),
    ).toEqual({ tone: "info", label: "Queued", attention: true });
  });

  it.each([
    ["sent", "Sent"],
    ["review_expired_approved", "Sent (auto)"],
  ] as const)("falls back from review_status=%s when delivery state is absent", (review_status, label) => {
    expect(deriveStatusChip({ direction: "outbound", review_status })).toEqual({
      tone: "success",
      label,
      attention: false,
    });
  });

  it("surfaces an unknown non-empty delivery status as a neutral raw label", () => {
    expect(
      deriveStatusChip({ direction: "outbound", delivery_status: "new_status" }),
    ).toEqual({ tone: "neutral", label: "new_status", attention: false });
  });

  it("returns null for outbound messages without lifecycle state", () => {
    expect(deriveStatusChip({ direction: "outbound" })).toBeNull();
  });

  it("preserves inbound unread as a passive info chip", () => {
    expect(
      deriveStatusChip({ direction: "inbound", inbox_status: "unread" }),
    ).toEqual({ tone: "info", label: "Unread", attention: false });
  });

  it("preserves inbound read as a passive neutral chip", () => {
    expect(
      deriveStatusChip({ direction: "inbound", inbox_status: "read" }),
    ).toEqual({ tone: "neutral", label: "Read", attention: false });
  });
});
