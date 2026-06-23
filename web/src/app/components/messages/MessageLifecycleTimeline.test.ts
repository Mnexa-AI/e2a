// Lifecycle step derivation contract.

import { deriveLifecycleSteps } from "./MessageLifecycleTimeline";

describe("deriveLifecycleSteps", () => {
  it("pending_approval w/ inbound parent → 4 steps, 3rd is current, 4th pending", () => {
    const steps = deriveLifecycleSteps({
      status: "pending_review",
      draftedAt: "2026-05-24T14:18:09Z",
      inboundReceivedAt: "2026-05-24T14:05:12Z",
    });
    expect(steps.map((s) => s.label)).toEqual([
      "Inbound received",
      "Agent drafted reply",
      "Held for review",
      "Sent to recipient",
    ]);
    expect(steps[2].current).toBe(true);
    expect(steps[3].pending).toBe(true);
  });

  it("plain outbound (no inbound parent) → 3 steps", () => {
    const steps = deriveLifecycleSteps({
      status: "pending_review",
      draftedAt: "2026-05-24T14:18:09Z",
    });
    expect(steps.map((s) => s.label)).toEqual([
      "Agent drafted reply",
      "Held for review",
      "Sent to recipient",
    ]);
  });

  it("sent → final step is success 'Sent to recipient'", () => {
    const steps = deriveLifecycleSteps({
      status: "sent",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T14:25:00Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Sent to recipient");
    expect(last.kind).toBe("success");
    expect(last.pending).toBeUndefined();
  });

  it("expired_approved → 'auto-approved' caption on the final step", () => {
    const steps = deriveLifecycleSteps({
      status: "review_expired_approved",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T15:18:09Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Sent to recipient");
    expect(last.caption).toContain("auto-approved");
  });

  it("rejected → 'Rejected' final step", () => {
    const steps = deriveLifecycleSteps({
      status: "review_rejected",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T14:22:00Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Rejected");
    expect(last.caption).toContain("by reviewer");
  });

  it("expired_rejected → 'Rejected' final step, 'auto-rejected' caption", () => {
    const steps = deriveLifecycleSteps({
      status: "review_expired_rejected",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T15:18:09Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Rejected");
    expect(last.caption).toContain("auto-rejected");
  });

  it("hitlEnabled=false → skips the Held step and uses draftedAt as the delivered timestamp", () => {
    const steps = deriveLifecycleSteps({
      status: "sent",
      draftedAt: "2026-05-24T14:18:09Z",
      hitlEnabled: false,
    });
    expect(steps.map((s) => s.label)).toEqual([
      "Agent drafted reply",
      "Sent to recipient",
    ]);
    const sent = steps[steps.length - 1];
    expect(sent.caption).toMatch(/delivered$/);
    // Caption must carry a real timestamp, not the "—" placeholder
    // that this fix is replacing.
    expect(sent.caption).not.toMatch(/^—/);
  });

  it("hitlEnabled=false w/ inbound parent → Inbound + Drafted + Sent, no Held", () => {
    const steps = deriveLifecycleSteps({
      status: "sent",
      draftedAt: "2026-05-24T14:18:09Z",
      inboundReceivedAt: "2026-05-24T14:05:12Z",
      hitlEnabled: false,
    });
    expect(steps.map((s) => s.label)).toEqual([
      "Inbound received",
      "Agent drafted reply",
      "Sent to recipient",
    ]);
  });

  it("Held step is always present; status drives current/terminal caption", () => {
    const pending = deriveLifecycleSteps({
      status: "pending_review",
      draftedAt: "2026-05-24T14:18:09Z",
    });
    const held = pending.find((s) => s.label === "Held for review");
    expect(held?.current).toBe(true);
    expect(held?.caption).toBe("awaiting reviewer");

    const sent = deriveLifecycleSteps({
      status: "sent",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T14:25:00Z",
    });
    expect(sent.find((s) => s.label === "Held for review")?.caption).toContain("resolved");
  });
});
