// Lifecycle step derivation contract.

import { deriveLifecycleSteps } from "./MessageLifecycleTimeline";

describe("deriveLifecycleSteps", () => {
  it("pending_approval w/ inbound parent → 4 steps, 3rd is current, 4th pending", () => {
    const steps = deriveLifecycleSteps({
      status: "pending_approval",
      draftedAt: "2026-05-24T14:18:09Z",
      inboundReceivedAt: "2026-05-24T14:05:12Z",
      ttlHint: "TTL 1h",
    });
    expect(steps.map((s) => s.label)).toEqual([
      "Inbound received",
      "Agent drafted reply",
      "Held for HITL approval",
      "Sent to recipient",
    ]);
    expect(steps[2].current).toBe(true);
    expect(steps[3].pending).toBe(true);
  });

  it("plain outbound (no inbound parent) → 3 steps", () => {
    const steps = deriveLifecycleSteps({
      status: "pending_approval",
      draftedAt: "2026-05-24T14:18:09Z",
    });
    expect(steps.map((s) => s.label)).toEqual([
      "Agent drafted reply",
      "Held for HITL approval",
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
      status: "expired_approved",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T15:18:09Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Sent to recipient");
    expect(last.caption).toContain("auto-approved");
  });

  it("rejected → 'Rejected' final step", () => {
    const steps = deriveLifecycleSteps({
      status: "rejected",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T14:22:00Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Rejected");
    expect(last.caption).toContain("by reviewer");
  });

  it("expired_rejected → 'Rejected' final step, 'auto-rejected' caption", () => {
    const steps = deriveLifecycleSteps({
      status: "expired_rejected",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T15:18:09Z",
    });
    const last = steps[steps.length - 1];
    expect(last.label).toBe("Rejected");
    expect(last.caption).toContain("auto-rejected");
  });

  it("Held step is always present; status drives current/terminal caption", () => {
    const pending = deriveLifecycleSteps({
      status: "pending_approval",
      draftedAt: "2026-05-24T14:18:09Z",
      ttlHint: "TTL 1h",
    });
    expect(pending.find((s) => s.label === "Held for HITL approval")?.caption).toContain("TTL 1h");

    const sent = deriveLifecycleSteps({
      status: "sent",
      draftedAt: "2026-05-24T14:18:09Z",
      reviewedAt: "2026-05-24T14:25:00Z",
    });
    expect(sent.find((s) => s.label === "Held for HITL approval")?.caption).not.toContain("TTL");
  });
});
