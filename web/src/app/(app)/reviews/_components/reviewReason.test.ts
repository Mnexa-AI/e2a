import { holdReasonSummary } from "./reviewReason";

describe("holdReasonSummary", () => {
  it("returns the server-provided summary", () => {
    expect(
      holdReasonSummary({
        type: "gate",
        code: "sender_gate",
        summary: "This sender isn't allowed by the inbox policy.",
      }),
    ).toBe("This sender isn't allowed by the inbox policy.");
  });

  it("does not append confidence to the collapsed summary", () => {
    expect(
      holdReasonSummary({
        type: "scan",
        code: "inbound_scan",
        summary: "Content screening found a potential risk.",
        confidence: 0.92,
      }),
    ).toBe("Content screening found a potential risk.");
  });

  it("returns null when there is no reason (so the row omits the line)", () => {
    expect(holdReasonSummary(undefined)).toBeNull();
    expect(holdReasonSummary(null)).toBeNull();
  });
});
