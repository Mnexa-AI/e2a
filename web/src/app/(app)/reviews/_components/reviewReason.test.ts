import { categoryLabel, holdReasonSummary } from "./reviewReason";

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

describe("categoryLabel", () => {
  it("maps known categories, tolerating _ and - spellings", () => {
    expect(categoryLabel("prompt-injection")).toBe("Prompt injection");
    expect(categoryLabel("prompt_injection")).toBe("Prompt injection");
    expect(categoryLabel("JAILBREAK")).toBe("Jailbreak attempt");
  });
  it("humanizes unknown open-set categories", () => {
    expect(categoryLabel("some-new-threat")).toBe("Some new threat");
  });
  it("stays a string for prototype-key names", () => {
    expect(typeof categoryLabel("constructor")).toBe("string");
    expect(categoryLabel("constructor")).toBe("Constructor");
  });
});
