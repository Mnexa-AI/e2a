import { fireEvent, render, screen } from "../../../test-utils/swr";
import { getMessageLifecycle } from "../../../lib/messageLifecycle";
import { MessageLifecycleData } from "./MessageLifecycleTimeline";

jest.mock("../../../lib/messageLifecycle", () => ({
  ...jest.requireActual("../../../lib/messageLifecycle"),
  getMessageLifecycle: jest.fn(),
}));

const mockGetLifecycle = getMessageLifecycle as jest.MockedFunction<
  typeof getMessageLifecycle
>;

const transition = (id: string, reason: "acceptance.outbound_api" | "submission.upstream_accepted") => ({
  id,
  message_id: "msg_pages",
  direction: "outbound" as const,
  recipient: "person@example.com",
  stage: reason === "acceptance.outbound_api" ? "accepted" as const : "submission" as const,
  outcome: "accepted" as const,
  reason_code: reason,
  retryable: false,
  evidence: {},
  correlation_ids: {},
  occurred_at: reason === "acceptance.outbound_api" ? "2026-07-22T12:00:00Z" : "2026-07-22T12:01:00Z",
  reconstructed: false,
});

afterEach(() => mockGetLifecycle.mockReset());

describe("MessageLifecycleData", () => {
  it("loads subsequent cursor pages without losing earlier observations", async () => {
    mockGetLifecycle
      .mockResolvedValueOnce({
        items: [transition("mlt_1", "acceptance.outbound_api")],
        next_cursor: "cursor_2",
      })
      .mockResolvedValueOnce({
        items: [transition("mlt_2", "submission.upstream_accepted")],
        next_cursor: null,
      });

    render(<MessageLifecycleData email="agent@example.com" messageId="msg_pages" />);

    expect(await screen.findByText("Accepted by e2a")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /load more observations/i }));
    expect(await screen.findByText("Handed off to delivery provider")).toBeInTheDocument();
    expect(screen.getByText("Accepted by e2a")).toBeInTheDocument();
    expect(mockGetLifecycle).toHaveBeenLastCalledWith(
      "agent@example.com",
      "msg_pages",
      { cursor: "cursor_2", limit: 100 },
    );
  });

  it("renders honest empty and unavailable states", async () => {
    mockGetLifecycle.mockResolvedValueOnce({ items: [], next_cursor: null });
    const empty = render(<MessageLifecycleData email="agent@example.com" messageId="msg_empty" />);
    expect(await screen.findByText(/No lifecycle observations/)).toBeInTheDocument();
    empty.unmount();

    mockGetLifecycle.mockRejectedValueOnce(new Error("offline"));
    render(<MessageLifecycleData email="agent@example.com" messageId="msg_error" />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Lifecycle unavailable");
  });
});
