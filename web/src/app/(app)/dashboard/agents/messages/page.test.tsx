// Inbox page contract: thread list renders, selection via hash, pending
// callout fires when a thread has a pending draft, empty state, "load
// older" button.

import { render, screen, waitFor, within } from "../../../../../test-utils/swr";
import userEvent from "@testing-library/user-event";
import AgentInboxPage from "./page";
import type { MessageSummary } from "../../../../components/types";

const mockUseSearchParams = jest.fn();
const mockRouterPush = jest.fn();

jest.mock("next/navigation", () => ({
  useSearchParams: () => mockUseSearchParams(),
  useRouter: () => ({ push: mockRouterPush }),
}));

jest.mock("next/link", () => {
  return function MockLink({ href, children, ...rest }: { href: string; children: React.ReactNode; [k: string]: unknown }) {
    return <a href={href} {...rest}>{children}</a>;
  };
});

const mockFetch = jest.fn();
global.fetch = mockFetch;

Object.assign(navigator, { clipboard: { writeText: jest.fn() } });

function setSearchParams(params: Record<string, string>) {
  mockUseSearchParams.mockReturnValue({
    get: (k: string) => params[k] ?? null,
  });
}

const NOW = new Date("2026-05-24T12:00:00Z");
const minutesAgo = (n: number) =>
  new Date(NOW.getTime() - n * 60_000).toISOString();

const PENDING_REPLY: MessageSummary = {
  message_id: "msg_pending",
  direction: "outbound",
  from: "support@acme.io",
  to: ["maya@stripe.com"],
  recipient: "maya@stripe.com",
  subject: "Re: Q3 contract renewal",
  conversation_id: "conv_K3p9aQ",
  status: "",
  hitl_status: "pending_approval",
  created_at: minutesAgo(13),
  size_bytes: 1200,
};

const PARENT_INBOUND: MessageSummary = {
  message_id: "msg_parent",
  direction: "inbound",
  from: "maya@stripe.com",
  to: ["support@acme.io"],
  recipient: "support@acme.io",
  subject: "Q3 contract renewal",
  conversation_id: "conv_K3p9aQ",
  status: "unread",
  created_at: minutesAgo(25),
  size_bytes: 4200,
};

const ORPHAN_INBOUND: MessageSummary = {
  message_id: "msg_solo",
  direction: "inbound",
  from: "ci@github.com",
  to: ["support@acme.io"],
  recipient: "support@acme.io",
  subject: "PR #2841 merged",
  status: "read",
  created_at: minutesAgo(180),
  size_bytes: 12400,
};

function mockMessages(messages: MessageSummary[]) {
  mockFetch.mockImplementation((url: string) => {
    if (url.includes("/api/v1/agents/") && url.includes("/messages")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ messages }),
      });
    }
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
}

beforeEach(() => {
  jest.useFakeTimers().setSystemTime(NOW);
  mockFetch.mockReset();
  mockRouterPush.mockReset();
  // Reset URL hash between tests.
  if (typeof window !== "undefined") {
    window.history.replaceState(null, "", window.location.pathname);
  }
});

afterEach(() => {
  jest.useRealTimers();
});

describe("AgentInboxPage", () => {
  it("renders thread rows grouped by conversation_id", async () => {
    setSearchParams({ email: "support@acme.io" });
    mockMessages([PENDING_REPLY, PARENT_INBOUND, ORPHAN_INBOUND]);

    render(<AgentInboxPage />);

    await waitFor(() => {
      expect(screen.getAllByTestId("thread-row")).toHaveLength(2);
    });
    // Pending thread sorts to the top.
    const rows = screen.getAllByTestId("thread-row");
    expect(rows[0].dataset.threadKey).toBe("conv:conv_K3p9aQ");
    expect(rows[1].dataset.threadKey).toBe("orphan:msg_solo");
  });

  it("orphan inbound (no conversation_id) renders as a single-message thread", async () => {
    setSearchParams({ email: "support@acme.io" });
    mockMessages([ORPHAN_INBOUND]);

    render(<AgentInboxPage />);

    await waitFor(() => {
      expect(screen.getAllByTestId("thread-row")).toHaveLength(1);
    });
    // Subject appears in both the list row and the detail header; we
    // care that *some* element renders it.
    expect(screen.getAllByText("PR #2841 merged").length).toBeGreaterThan(0);
    // Synthetic thread key — used by the URL fragment when selected.
    expect(screen.getByTestId("thread-row").dataset.threadKey).toBe("orphan:msg_solo");
  });

  it("pending callout appears in the thread detail when a thread is pending", async () => {
    setSearchParams({ email: "support@acme.io" });
    mockMessages([PENDING_REPLY, PARENT_INBOUND]);

    render(<AgentInboxPage />);

    await waitFor(() => {
      expect(screen.getByTestId("pending-callout")).toBeInTheDocument();
    });
    expect(screen.getByText(/Outbound reply waiting on your approval/)).toBeInTheDocument();
  });

  it("clicking the pending callout navigates to the focus page with that message id", async () => {
    setSearchParams({ email: "support@acme.io" });
    mockMessages([PENDING_REPLY, PARENT_INBOUND]);
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });

    render(<AgentInboxPage />);
    await waitFor(() => {
      expect(screen.getByTestId("pending-callout")).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /Review →/ }));

    expect(mockRouterPush).toHaveBeenCalledWith(
      expect.stringContaining("/dashboard/agents/messages/view"),
    );
    expect(mockRouterPush.mock.calls[0][0]).toContain("id=msg_pending");
  });

  it("renders the empty state when there are no messages", async () => {
    setSearchParams({ email: "support@acme.io" });
    mockMessages([]);

    render(<AgentInboxPage />);

    await waitFor(() => {
      expect(screen.getByTestId("thread-list-empty")).toBeInTheDocument();
    });
  });

  it("URL fragment selects the matching thread on first render", async () => {
    setSearchParams({ email: "support@acme.io" });
    mockMessages([PENDING_REPLY, PARENT_INBOUND, ORPHAN_INBOUND]);

    // Pre-set the hash before render — useSyncExternalStore picks it up.
    window.history.replaceState(null, "", "#orphan:msg_solo");

    render(<AgentInboxPage />);

    await waitFor(() => {
      expect(screen.getAllByTestId("thread-row")).toHaveLength(2);
    });
    const detail = screen.getByTestId("thread-detail");
    // The selected thread's subject renders in both the detail header
    // (h2) and the bubble button. Assert at least one match.
    expect(within(detail).getAllByText("PR #2841 merged").length).toBeGreaterThan(0);
  });
});
