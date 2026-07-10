// ThreadBubble body-selection precedence: a message's rich HTML (parsed.html)
// is rendered in the sandboxed EmailHtmlBody iframe; otherwise the plain
// parsed.text is shown as escaped text. Pins the fallback order so a regression
// can't silently drop back to rendering raw MIME.

import { render, screen, waitFor } from "@testing-library/react";
import { ThreadBubble } from "./ThreadBubble";
import { getMessageDetail } from "../onboarding/api";
import {
  invalidateAgentMessages,
  invalidateAgentUnread,
} from "../../../lib/swrKeys";
import type { MessageSummary } from "../types";

jest.mock("../onboarding/api", () => ({
  getMessageDetail: jest.fn(),
}));
jest.mock("../../../lib/swrKeys", () => ({
  invalidateAgentMessages: jest.fn(),
  invalidateAgentUnread: jest.fn(),
}));
const mockGet = getMessageDetail as jest.MockedFunction<typeof getMessageDetail>;
const mockInvalidateMessages =
  invalidateAgentMessages as jest.MockedFunction<typeof invalidateAgentMessages>;
const mockInvalidateUnread =
  invalidateAgentUnread as jest.MockedFunction<typeof invalidateAgentUnread>;

// Each test uses a distinct message_id: useSWR keys the body cache by id and
// that cache is process-global, so reusing an id would leak one test's body
// into the next.
function msg(id: string): MessageSummary {
  return {
    message_id: id,
    direction: "inbound",
    from: "james@x.com",
    to: ["support@acme.dev"],
    recipient: "support@acme.dev",
    subject: "Hi",
    status: "",
    created_at: new Date().toISOString(),
  };
}
const CP = { email: "james@x.com", name: "James" };

function inbound(data: Record<string, unknown>) {
  mockGet.mockResolvedValue({ direction: "inbound", data } as never);
}

afterEach(() => {
  mockGet.mockReset();
  mockInvalidateMessages.mockReset();
  mockInvalidateUnread.mockReset();
});

describe("ThreadBubble body precedence", () => {
  it("renders parsed.html in the sandboxed iframe when present", async () => {
    inbound({ parsed: { text: "flat text", html: "<p>rich <b>html</b></p>" }, raw_message: "" });
    render(<ThreadBubble message={msg("msg_html")} counterparty={CP} agentEmail="support@acme.dev" />);
    await waitFor(() => {
      const frame = screen.getByTitle("Email body") as HTMLIFrameElement;
      expect(frame.getAttribute("srcdoc")).toContain("rich <b>html</b>");
    });
    // The flattened text is not also rendered as escaped page text.
    expect(screen.queryByText("flat text")).not.toBeInTheDocument();
  });

  it("falls back to parsed.text (no iframe) when there is no HTML part", async () => {
    inbound({ parsed: { text: "just the plain body" }, raw_message: "" });
    render(<ThreadBubble message={msg("msg_text")} counterparty={CP} agentEmail="support@acme.dev" />);
    await waitFor(() => {
      expect(screen.getByText("just the plain body")).toBeInTheDocument();
    });
    expect(screen.queryByTitle("Email body")).not.toBeInTheDocument();
  });
});

describe("ThreadBubble marks-read cache refresh", () => {
  // Opening a message body flips inbox_status unread → read on the backend.
  // The thread list (bold rows) and the Inboxes unread badge both cache the
  // stale state, so the bubble must revalidate them once the body loads.
  it("invalidates the thread list + unread badge after reading an unread inbound message", async () => {
    inbound({ parsed: { text: "body" }, raw_message: "" });
    const m = { ...msg("msg_unread_inbound"), read_status: "unread" };
    render(<ThreadBubble message={m} counterparty={CP} agentEmail="support@acme.dev" />);
    await waitFor(() => {
      expect(mockInvalidateMessages).toHaveBeenCalledWith("support@acme.dev");
      expect(mockInvalidateUnread).toHaveBeenCalledWith("support@acme.dev");
    });
  });

  it("does not invalidate when the inbound message was already read", async () => {
    inbound({ parsed: { text: "body" }, raw_message: "" });
    const m = { ...msg("msg_read_inbound"), read_status: "read" };
    render(<ThreadBubble message={m} counterparty={CP} agentEmail="support@acme.dev" />);
    // Wait for the body fetch to resolve so onSuccess has had its chance.
    await waitFor(() => expect(screen.getByText("body")).toBeInTheDocument());
    expect(mockInvalidateMessages).not.toHaveBeenCalled();
    expect(mockInvalidateUnread).not.toHaveBeenCalled();
  });

  it("does not invalidate for outbound messages", async () => {
    mockGet.mockResolvedValue({
      direction: "outbound",
      data: { body_text: "sent body", body_html: "" },
    } as never);
    const m: MessageSummary = { ...msg("msg_outbound"), direction: "outbound", read_status: "" };
    render(<ThreadBubble message={m} counterparty={CP} agentEmail="support@acme.dev" />);
    await waitFor(() => expect(screen.getByText("sent body")).toBeInTheDocument());
    expect(mockInvalidateMessages).not.toHaveBeenCalled();
    expect(mockInvalidateUnread).not.toHaveBeenCalled();
  });
});
