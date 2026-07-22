// Canonical lifecycle presentation contract.

import { fireEvent, render, screen } from "@testing-library/react";
import { createElement, type ComponentType } from "react";
import { MessageLifecycleTimeline } from "./MessageLifecycleTimeline";

const transition = (overrides: Record<string, unknown> = {}) => ({
  id: "mlt_1",
  message_id: "msg_1",
  direction: "outbound",
  recipient: "person@example.com",
  stage: "accepted",
  outcome: "accepted",
  reason_code: "acceptance.outbound_api",
  retryable: false,
  evidence: {},
  correlation_ids: {},
  occurred_at: "2026-07-22T12:00:00Z",
  reconstructed: false,
  ...overrides,
});

const CanonicalTimeline = MessageLifecycleTimeline as unknown as ComponentType<{
  transitions: Array<Record<string, unknown>>;
}>;
const renderTimeline = (transitions: Array<Record<string, unknown>>) =>
  render(createElement(CanonicalTimeline, { transitions }));

describe("MessageLifecycleTimeline canonical observations", () => {
  it("renders observations as an overflow-safe horizontal timeline", () => {
    renderTimeline([
      transition(),
      transition({
        id: "mlt_2",
        stage: "queued",
        outcome: "enqueued",
        reason_code: "queue.outbound_submission",
        occurred_at: "2026-07-22T12:01:00Z",
      }),
    ]);

    const observations = screen.getByRole("list", {
      name: "Message lifecycle observations",
    });
    expect(observations).toHaveStyle({
      display: "grid",
      gridAutoFlow: "column",
      overflowX: "auto",
    });
  });

  it("shows an accepted-only message without claiming it was sent", () => {
    renderTimeline([transition()]);

    expect(screen.getByText("Accepted by e2a")).toBeInTheDocument();
    expect(screen.getByText("Accepted · awaiting next observation")).toBeInTheDocument();
    expect(screen.queryByText(/sent to recipient/i)).not.toBeInTheDocument();
  });

  it("distinguishes pending review from upstream acceptance", () => {
    renderTimeline([
      transition(),
      transition({
        id: "mlt_2",
        stage: "review",
        outcome: "pending",
        reason_code: "review.hold_created",
        occurred_at: "2026-07-22T12:01:00Z",
      }),
    ]);

    expect(screen.getByText("Pending review")).toBeInTheDocument();
    expect(screen.getByText("Held for review")).toBeInTheDocument();
  });

  it("calls provider acceptance sent while keeping recipient-server delivery distinct", () => {
    renderTimeline([
      transition(),
      transition({
        id: "mlt_2",
        stage: "submission",
        outcome: "accepted",
        reason_code: "submission.upstream_accepted",
        correlation_ids: { provider_message_id: "provider-123" },
        occurred_at: "2026-07-22T12:02:00Z",
      }),
    ]);

    expect(screen.getByText("Sent")).toBeInTheDocument();
    expect(screen.getByText("Accepted by upstream provider")).toBeInTheDocument();
    expect(screen.queryByText(/recipient server/i)).not.toBeInTheDocument();
  });

  it("reveals safe evidence, correlation ids, retryability, and reconstruction on demand", () => {
    renderTimeline([
      transition({
        reconstructed: true,
        retryable: true,
        evidence: { smtp_code: 451 },
        correlation_ids: { job_id: "42" },
      }),
    ]);

    expect(screen.queryByText("smtp_code")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /diagnostics/i }));
    expect(screen.getByText("Reconstructed from durable history")).toBeInTheDocument();
    expect(screen.getByText("Retryable")).toBeInTheDocument();
    expect(screen.getByText("smtp_code")).toBeInTheDocument();
    expect(screen.getByText("job_id")).toBeInTheDocument();
  });
});
