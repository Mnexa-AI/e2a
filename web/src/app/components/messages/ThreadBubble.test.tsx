// ThreadBubble body-selection precedence: a message's rich HTML (parsed.html)
// is rendered in the sandboxed EmailHtmlBody iframe; otherwise the plain
// parsed.text is shown as escaped text. Pins the fallback order so a regression
// can't silently drop back to rendering raw MIME.

import { render, screen, waitFor } from "@testing-library/react";
import { ThreadBubble } from "./ThreadBubble";
import { getMessageDetail } from "../onboarding/api";
import type { MessageSummary } from "../types";

jest.mock("../onboarding/api", () => ({
  getMessageDetail: jest.fn(),
}));
const mockGet = getMessageDetail as jest.MockedFunction<typeof getMessageDetail>;

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

afterEach(() => mockGet.mockReset());

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
