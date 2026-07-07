import { reviewReasonLabel } from "./reviewReason";

describe("reviewReasonLabel", () => {
  it("maps every known coded reason to a friendly label", () => {
    expect(reviewReasonLabel("sender_gate")).toBe(
      "Sender blocked by inbound policy",
    );
    expect(reviewReasonLabel("recipient_gate")).toBe(
      "Recipient blocked by outbound policy",
    );
    expect(reviewReasonLabel("inbound_scan")).toBe(
      "Content flagged by screening scan",
    );
    expect(reviewReasonLabel("outbound_scan")).toBe(
      "Content flagged by screening scan",
    );
    expect(reviewReasonLabel("outbound_send")).toBe("Outbound send blocked");
  });

  it("appends the scan score as a parenthetical when present", () => {
    expect(reviewReasonLabel("inbound_scan", 0.87)).toBe(
      "Content flagged by screening scan (0.87)",
    );
    // Rounds to two decimals.
    expect(reviewReasonLabel("inbound_scan", 0.876)).toBe(
      "Content flagged by screening scan (0.88)",
    );
  });

  it("omits the score for gate holds (nil/undefined score)", () => {
    expect(reviewReasonLabel("sender_gate", null)).toBe(
      "Sender blocked by inbound policy",
    );
    expect(reviewReasonLabel("sender_gate", undefined)).toBe(
      "Sender blocked by inbound policy",
    );
  });

  it("returns null when there is no reason (so the row omits the line)", () => {
    expect(reviewReasonLabel(undefined)).toBeNull();
    expect(reviewReasonLabel(null)).toBeNull();
    expect(reviewReasonLabel("")).toBeNull();
  });

  it("humanizes an unknown open-set code rather than dropping it", () => {
    // The API reason set is open — a code the UI doesn't know yet must still
    // render something legible.
    expect(reviewReasonLabel("some_new_reason")).toBe("Some new reason");
    expect(reviewReasonLabel("some_new_reason", 0.5)).toBe(
      "Some new reason (0.50)",
    );
  });

  it("never returns a non-string for Object.prototype-key codes", () => {
    // A coded reason colliding with a prototype key must not resolve to the
    // inherited function/object (which would crash the React render) — it must
    // fall through to the humanized string.
    for (const key of ["constructor", "toString", "valueOf", "hasOwnProperty"]) {
      const label = reviewReasonLabel(key);
      expect(typeof label).toBe("string");
    }
    expect(reviewReasonLabel("constructor")).toBe("Constructor");
    expect(reviewReasonLabel("__proto__")).toBe("Proto");
  });

  it("renders a legitimate zero score, and would show a score even on a gate reason", () => {
    // 0 is a finite score, not 'absent' — it renders.
    expect(reviewReasonLabel("inbound_scan", 0)).toBe(
      "Content flagged by screening scan (0.00)",
    );
    // The helper is contract-agnostic: if the backend ever attaches a score to
    // a gate reason, it's appended (the backend, not this fn, decides when).
    expect(reviewReasonLabel("sender_gate", 0.4)).toBe(
      "Sender blocked by inbound policy (0.40)",
    );
  });

  it("drops non-finite scores (NaN / Infinity)", () => {
    expect(reviewReasonLabel("inbound_scan", NaN)).toBe(
      "Content flagged by screening scan",
    );
    expect(reviewReasonLabel("inbound_scan", Infinity)).toBe(
      "Content flagged by screening scan",
    );
  });
});
