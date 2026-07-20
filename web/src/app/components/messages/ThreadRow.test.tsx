import { render, screen } from "@testing-library/react";
import { ThreadRow } from "./ThreadRow";
import type { MessageSummary } from "../types";
import type { Thread } from "./threading";

function message(
  id: string,
  direction: MessageSummary["direction"],
  status: string,
  review_status?: string,
): MessageSummary {
  return {
    id,
    direction,
    from: direction === "inbound" ? "james@x.com" : "support@acme.dev",
    to: [direction === "inbound" ? "support@acme.dev" : "james@x.com"],
    recipient: direction === "inbound" ? "support@acme.dev" : "james@x.com",
    subject: "Status update",
    status,
    review_status,
    created_at: `2026-07-19T10:0${id.slice(-1)}:00.000Z`,
  };
}

function thread(messages: MessageSummary[], state: Thread["state"] = "active"): Thread {
  const latest = messages[messages.length - 1];
  return {
    key: "conv:status-test",
    conversationId: "status-test",
    counterparty: { email: "james@x.com", name: "James" },
    subject: "Status update",
    state,
    lastMessageAt: latest.created_at,
    startedAt: messages[0].created_at,
    msgCount: messages.length,
    lastDirection: latest.direction,
    lastPreview: latest.subject,
    messages,
  };
}

function renderRow(value: Thread) {
  render(<ThreadRow thread={value} active={false} onSelect={jest.fn()} />);
}

describe("ThreadRow delivery status", () => {
  it("renders Queued when the latest outbound message was accepted", () => {
    renderRow(thread([message("msg_1", "outbound", "accepted")]));

    expect(screen.getByText("Queued")).toHaveClass("shrink-0", "whitespace-nowrap");
  });

  it("omits Delivered for a settled latest outbound message", () => {
    renderRow(thread([message("msg_2", "outbound", "delivered")]));

    expect(screen.queryByText("Delivered")).not.toBeInTheDocument();
  });

  it("does not surface a historical failure after a newer inbound message", () => {
    renderRow(
      thread([
        message("msg_3", "outbound", "failed"),
        message("msg_4", "inbound", ""),
      ]),
    );

    expect(screen.queryByText("Failed")).not.toBeInTheDocument();
  });

  it("preserves Pending when an older held message exists and the latest is inbound", () => {
    renderRow(
      thread(
        [
          message("msg_5", "outbound", "", "pending_review"),
          message("msg_6", "inbound", ""),
        ],
        "pending",
      ),
    );

    expect(screen.getByText("Pending")).toBeInTheDocument();
  });

  it("renders safely when a thread has no messages", () => {
    const value = thread([message("msg_7", "inbound", "")]);

    expect(() => renderRow({ ...value, messages: [] })).not.toThrow();
  });
});
