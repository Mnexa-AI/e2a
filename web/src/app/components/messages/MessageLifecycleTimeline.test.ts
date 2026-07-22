// Canonical lifecycle presentation contract.

import { fireEvent, render, screen } from "@testing-library/react";
import { createElement, type ComponentType } from "react";
import {
  LIFECYCLE_PRESENTATION,
  MessageLifecycleTimeline,
  formatLifecycleDiagnostics,
} from "./MessageLifecycleTimeline";
import { MESSAGE_LIFECYCLE_REASON_CODES } from "../../../lib/messageLifecycle";

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
  it("defines friendly presentation copy for every canonical reason code", () => {
    expect(Object.keys(LIFECYCLE_PRESENTATION).sort()).toEqual(
      [...MESSAGE_LIFECYCLE_REASON_CODES].sort(),
    );
  });

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
    expect(
      screen.getByText(
        "e2a accepted and saved this message. It is waiting for the next delivery step.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Accepted · awaiting next observation")).toBeInTheDocument();
    expect(screen.queryByText("acceptance.outbound_api")).not.toBeInTheDocument();
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
    expect(screen.getByText("Waiting for review")).toBeInTheDocument();
    expect(
      screen.getByText("This message is waiting for approval before e2a sends it."),
    ).toBeInTheDocument();
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
    expect(screen.getByText("Handed off to delivery provider")).toBeInTheDocument();
    expect(
      screen.getByText(
        "e2a successfully handed off the message, and the provider agreed to process it.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/recipient server/i)).not.toBeInTheDocument();
  });

  it("keeps machine outcomes beside the diagnostics instead of across the stage heading", () => {
    renderTimeline([
      transition({
        stage: "queued",
        outcome: "enqueued",
        reason_code: "queue.outbound_submission",
      }),
    ]);

    expect(screen.getByText("Queued for delivery")).toBeInTheDocument();
    expect(
      screen.getByText("The message is waiting to be handed off to the delivery provider."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Enqueued")).not.toBeInTheDocument();
    expect(screen.queryByText("queue.outbound_submission")).not.toBeInTheDocument();
  });

  it("keeps recipient-server acceptance distinct from inbox placement", () => {
    renderTimeline([
      transition({
        stage: "delivery",
        outcome: "delivered",
        reason_code: "delivery.recipient_server_accepted",
      }),
    ]);

    expect(screen.getByText("Accepted by recipient server")).toBeInTheDocument();
    expect(
      screen.getByText(
        "The recipient's mail server accepted the message. This does not confirm inbox placement.",
      ),
    ).toBeInTheDocument();
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
    expect(
      screen.getByText("Include these details when filing feedback or reporting a bug."),
    ).toBeInTheDocument();
    expect(screen.getByText("Transition ID")).toBeInTheDocument();
    expect(screen.getByText("Message ID")).toBeInTheDocument();
    expect(screen.getByText("Direction")).toBeInTheDocument();
    expect(screen.getByText("Stage")).toBeInTheDocument();
    expect(screen.getByText("Occurred at")).toBeInTheDocument();
    expect(screen.getByText("Reconstructed from durable history")).toBeInTheDocument();
    expect(screen.getByText("Retryable")).toBeInTheDocument();
    expect(screen.getByText("smtp_code")).toBeInTheDocument();
    expect(screen.getByText("job_id")).toBeInTheDocument();
  });

  it("formats deterministic support diagnostics without message content", () => {
    const row = transition({
      evidence: { zeta: 2, alpha: { second: true, first: false } },
      correlation_ids: { z_job: "9", a_provider: "1" },
    });

    const diagnostics = formatLifecycleDiagnostics(
      row as Parameters<typeof formatLifecycleDiagnostics>[0],
    );
    expect(diagnostics).toContain('Evidence: {"alpha":{"first":false,"second":true},"zeta":2}');
    expect(diagnostics).toContain('Correlation IDs: {"a_provider":"1","z_job":"9"}');
    expect(diagnostics).not.toMatch(/body|html|raw_message|attachments/i);
  });

  it("copies support diagnostics and confirms success", async () => {
    const writeText = jest.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    renderTimeline([transition()]);

    fireEvent.click(screen.getByRole("button", { name: "Diagnostics" }));
    fireEvent.click(screen.getByRole("button", { name: "Copy diagnostics" }));

    expect(await screen.findByRole("button", { name: "Copied" })).toBeInTheDocument();
    expect(writeText).toHaveBeenCalledWith(
      formatLifecycleDiagnostics(
        transition() as Parameters<typeof formatLifecycleDiagnostics>[0],
      ),
    );
  });

  it("keeps diagnostics visible when clipboard access fails", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: jest.fn().mockRejectedValue(new Error("denied")) },
    });
    renderTimeline([transition()]);

    fireEvent.click(screen.getByRole("button", { name: "Diagnostics" }));
    fireEvent.click(screen.getByRole("button", { name: "Copy diagnostics" }));

    expect(
      await screen.findByRole("button", { name: "Copy failed — try again" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Transition ID")).toBeInTheDocument();
  });

  it("labels the shared lifecycle panel as beta", () => {
    renderTimeline([transition()]);

    const timeline = screen.getByTestId("lifecycle-timeline");
    expect(timeline).toHaveTextContent("Lifecycle");
    expect(timeline).toHaveTextContent("Beta");
  });
});
