// Focus-page contract: renders for outbound pending + inbound,
// Headers collapsible respects ?headers=1, Approve calls the API +
// redirects, ⌘↵ triggers Approve, missing params surface an error.

import { render, screen, waitFor } from "../../../../../../test-utils/swr";
import { render as rawRender } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { mutate } from "swr";
import AgentMessageFocusPage from "./page";
import { agentsKey, pendingMessageKey } from "../../../../../../lib/swrKeys";

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

// Spy on the SWR cache invalidation helpers so we can assert which
// caches the page touches without standing up a full SWR cache. We
// still want the real key shapes (used elsewhere in the module via
// `as const` tuples), so we keep `requireActual` and override only
// the side-effect functions we want to observe.
const mockInvalidateAgentMessages = jest.fn();
jest.mock("../../../../../../lib/swrKeys", () => {
  const actual = jest.requireActual("../../../../../../lib/swrKeys");
  return {
    ...actual,
    invalidateAgentMessages: (email: string) =>
      mockInvalidateAgentMessages(email),
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

const OUTBOUND_PENDING = {
  id: "msg_pending",
  agent_id: "ag_1",
  direction: "outbound",
  subject: "Re: Q3 contract renewal",
  type: "reply",
  conversation_id: "conv_K3p9aQ",
  to: ["maya@stripe.com"],
  cc: [],
  bcc: [],
  status: "pending_approval",
  approval_expires_at: new Date(NOW.getTime() + 47 * 60_000).toISOString(),
  created_at: minutesAgo(13),
  email_message_id: "msg_b3n1xz",
  body_text: "Hi Maya,\n\nThanks for sending over the renewal draft…\n\nBest,\nAcme Support",
  edited: false,
  inbound: {
    sender: "maya@stripe.com",
    subject: "Q3 contract renewal",
    created_at: minutesAgo(25),
    auth_headers: { "X-E2A-Auth-Verified": "true" },
  },
};

const INBOUND_DETAIL = {
  message_id: "msg_in1",
  from: "maya@stripe.com",
  to: ["support@acme.io"],
  cc: [],
  reply_to: [],
  recipient: "support@acme.io",
  subject: "Q3 contract renewal",
  conversation_id: "conv_K3p9aQ",
  status: "read",
  created_at: minutesAgo(25),
  auth_headers: { "X-E2A-Auth-Verified": "true", "Received-SPF": "pass" },
  raw_message: btoa(
    "From: maya@stripe.com\r\n" +
      "To: support@acme.io\r\n" +
      "Subject: Q3 contract renewal\r\n" +
      "\r\n" +
      "Attached is the renewal contract for Q3.",
  ),
};

function mockOutboundOnly(payload: Record<string, unknown>) {
  mockFetch.mockImplementation((url: string) => {
    if (url.includes("/api/v1/messages/") && !url.includes("/agents/")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve(payload),
      });
    }
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
}

function mockInboundFallback(inbound: Record<string, unknown>) {
  // Outbound lookup 404s → page falls back to inbound endpoint.
  mockFetch.mockImplementation((url: string) => {
    if (url.includes("/api/v1/messages/") && !url.includes("/agents/")) {
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    }
    if (url.includes("/api/v1/agents/") && url.includes("/messages/")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve(inbound),
      });
    }
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
}

function mockApproveSuccess() {
  let calls = 0;
  mockFetch.mockImplementation((url: string, init?: RequestInit) => {
    if (url.endsWith("/approve") && init?.method === "POST") {
      calls++;
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ status: "sent", message_id: "msg_pending" }),
      });
    }
    if (url.includes("/api/v1/messages/msg_pending") && !url.includes("/approve") && !url.includes("/reject")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve(OUTBOUND_PENDING),
      });
    }
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
  return () => calls;
}

beforeEach(() => {
  jest.useFakeTimers().setSystemTime(NOW);
  mockFetch.mockReset();
  mockRouterPush.mockReset();
  mockInvalidateAgentMessages.mockReset();
});

afterEach(() => {
  jest.useRealTimers();
});

describe("AgentMessageFocusPage", () => {
  it("renders the outbound pending detail with subject, identity row, and action card", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    mockOutboundOnly(OUTBOUND_PENDING);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    expect(screen.getByTestId("message-focus").dataset.direction).toBe("outbound");
    expect(screen.getByTestId("message-focus").dataset.status).toBe("pending_approval");
    expect(screen.getByRole("heading", { name: /Re: Q3 contract renewal/ })).toBeInTheDocument();
    expect(screen.getByTestId("action-card")).toBeInTheDocument();
    expect(screen.getByText(/Awaiting your approval/)).toBeInTheDocument();
  });

  it("renders the headers section open when ?headers=1", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", headers: "1" });
    mockOutboundOnly(OUTBOUND_PENDING);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    // Collapsible button has aria-expanded="true" when open.
    const headerButton = screen.getByRole("button", { name: /Full headers/i });
    expect(headerButton).toHaveAttribute("aria-expanded", "true");
  });

  it("falls back to the inbound endpoint when outbound returns 404", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_in1" });
    mockInboundFallback(INBOUND_DETAIL);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    expect(screen.getByTestId("message-focus").dataset.direction).toBe("inbound");
    // No action card on inbound — that's outbound-pending-only.
    expect(screen.queryByTestId("action-card")).not.toBeInTheDocument();
    // The inbound body was extracted from the raw_message base64.
    expect(screen.getByText(/Attached is the renewal contract/)).toBeInTheDocument();
  });

  // Regression: GET /agents/{email}/messages/{id} flips inbox_status
  // unread → read as a server-side side effect. The focus page must
  // invalidate the per-agent inbox SWR cache when that happens, or
  // the inbox view will keep showing the row as unread until
  // window-focus revalidation eventually catches up. Pre-SWR this
  // was free (every navigation refetched the inbox); post-SWR it
  // needs an explicit cache invalidation.
  it("invalidates the per-agent inbox cache after a successful inbound load", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_in1" });
    mockInboundFallback(INBOUND_DETAIL);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(mockInvalidateAgentMessages).toHaveBeenCalledWith(
        "support@acme.io",
      );
    });
  });

  it("clicking Approve POSTs to /api/v1/agents/{email}/messages/{id}/approve and redirects to the thread", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    const countCalls = mockApproveSuccess();
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });

    render(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /Approve & send/i }));

    await waitFor(() => {
      expect(countCalls()).toBe(1);
    });
    expect(mockRouterPush).toHaveBeenCalledWith(
      expect.stringContaining("/dashboard/agents/messages?email=support%40acme.io"),
    );
  });

  it("⌘↵ keyboard shortcut triggers Approve when status is pending", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    const countCalls = mockApproveSuccess();

    render(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });

    // Fire ⌘↵ at the document — the focus page binds the listener
    // there to make the shortcut work regardless of focus position.
    const ev = new KeyboardEvent("keydown", { key: "Enter", metaKey: true });
    document.dispatchEvent(ev);

    await waitFor(() => {
      expect(countCalls()).toBe(1);
    });
  });

  it("surfaces a clear error when ?email or ?id is missing", async () => {
    setSearchParams({});

    render(<AgentMessageFocusPage />);

    expect(screen.getByText(/Missing \?email= or \?id=/)).toBeInTheDocument();
  });

  // M1 regression test: a 500 from the outbound endpoint must NOT fall
  // through to the inbound endpoint — that masked the real server
  // error as "Message not found" in the earlier implementation.
  it("surfaces the outbound error message when the outbound endpoint returns 500", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_unknown" });
    mockFetch.mockImplementation((url: string) => {
      if (url.includes("/api/v1/messages/") && !url.includes("/agents/")) {
        return Promise.resolve({
          ok: false,
          status: 500,
          text: () => Promise.resolve("internal server error"),
        });
      }
      // Inbound mock — should NOT be reached on a non-404.
      return Promise.resolve({
        ok: false,
        status: 404,
        text: () => Promise.resolve("not found"),
      });
    });

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByText(/internal server error/)).toBeInTheDocument();
    });
    // Verify inbound endpoint was never called.
    const calls = mockFetch.mock.calls.map(([url]: [string]) => url);
    expect(calls.some((u) => u.includes("/api/v1/agents/"))).toBe(false);
  });

  it("submits the edited body_text in the approve overrides when Edit + Approve is used", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });
    let approveBody: string | null = null;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/messages/msg_pending") && !url.endsWith("/approve") && !url.endsWith("/reject")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve(OUTBOUND_PENDING),
        });
      }
      if (url.endsWith("/approve") && init?.method === "POST") {
        approveBody = (init.body as string) || "";
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ status: "sent", message_id: "msg_pending" }),
        });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });

    // Click "edit draft" link in the body card footer to enter edit mode.
    await user.click(screen.getByText(/^edit draft$/i));
    // Textarea now exists; type a different body.
    const textarea = screen.getByRole("textbox");
    await user.clear(textarea);
    await user.type(textarea, "Edited body");

    await user.click(screen.getByRole("button", { name: /Approve & send/i }));

    await waitFor(() => {
      expect(approveBody).not.toBeNull();
    });
    expect(approveBody).toContain('"body_text":"Edited body"');
  });

  // Regression for H3: previously the draft-body textarea was seeded
  // only from SWR's onSuccess callback. onSuccess fires after a real
  // fetch — not on a cache hit served within dedupingInterval. If
  // another surface (e.g. PendingDetailPanel) populated
  // pendingMessageKey(id) just before the user navigated here, the
  // focus page would render data from cache, skip the fetcher, never
  // call onSuccess, and leave draftBody as "" — the reviewer would
  // click Edit and see a blank textarea instead of the agent's body.
  // The effect-based seed runs on every data change (including the
  // synchronous cache-hit case), so warm-cache navigation seeds too.
  //
  // To genuinely reproduce the bug condition (vs asserting the
  // post-fix shape on a fetch path that pre-fix code would also have
  // passed), we render WITHOUT the test-utils/swr fresh-Map wrapper,
  // use the module-level SWR cache, pre-seed pendingMessageKey(id)
  // before mounting, and assert mockFetch was never called for the
  // outbound endpoint. A pre-fix implementation (onSuccess-only seed)
  // would observe data via the cache but never fire onSuccess, so
  // the textarea would be empty and the assertion would fail.
  it("seeds the textarea from pre-populated SWR cache without firing a fetch (true cache-hit regression)", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    // Pre-seed both caches the page subscribes to. The
    // jest.setup.ts afterEach nukes the module-level cache between
    // tests, so these seeds are isolated to this test. The agents
    // cache is now read by LifecycleSection to decide whether to
    // render the HITL step — without seeding it the page would
    // trigger a /api/dashboard/agents fetch and defeat the cache-
    // hit reproduction below.
    await mutate(
      pendingMessageKey("msg_pending"),
      OUTBOUND_PENDING,
      { revalidate: false },
    );
    await mutate(
      agentsKey,
      [{ id: "ag_1", email: "support@acme.io", hitl_enabled: true }],
      { revalidate: false },
    );
    // Fetch must NOT be called: any call indicates the page hit the
    // network rather than the cache, defeating the bug reproduction.
    mockFetch.mockImplementation(() => {
      throw new Error(
        "fetch was called — cache hit did not happen, test reproduces nothing",
      );
    });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });

    // Bypass test-utils/swr — it provides a fresh-Map SWRConfig
    // per render, which would isolate this test's mounted hooks
    // from the seed we just placed on the module-level cache.
    rawRender(<AgentMessageFocusPage />);

    // Page renders against the seeded cache synchronously; action
    // card appears without a fetch.
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });
    // Assert no outbound fetch happened.
    expect(mockFetch).not.toHaveBeenCalled();

    // The H3 fix's invariant: textarea is seeded from cache-resolved
    // data, even though onSuccess never fired.
    await user.click(screen.getByText(/^edit draft$/i));
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    expect(textarea.value).toContain("Thanks for sending over the renewal draft");
  });

  // Regression: navigating from message A to message B via ?id= must
  // reset the inner per-message state (draftBody, editingDraft,
  // hasUserEditedRef, rejectReason). Before keying FocusContent by
  // `${email}|${id}`, an edit-in-progress on A would bleed into B's
  // view and the user would see A's stale draft superimposed on B.
  it("resets per-message state when ?id changes (no draft bleed across navigation)", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });

    const OTHER = {
      ...OUTBOUND_PENDING,
      id: "msg_other",
      subject: "Different subject",
      body_text: "Different body content",
    };
    mockFetch.mockImplementation((url: string) => {
      if (url.includes("/api/v1/messages/msg_pending") && !url.includes("/agents/")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve(OUTBOUND_PENDING),
        });
      }
      if (url.includes("/api/v1/messages/msg_other") && !url.includes("/agents/")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve(OTHER),
        });
      }
      return Promise.resolve({
        ok: false,
        status: 404,
        text: () => Promise.resolve("not found"),
      });
    });

    const { rerender } = render(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });

    // Enter edit mode and type a stale draft body on message A.
    await user.click(screen.getByText(/^edit draft$/i));
    const textareaA = screen.getByRole("textbox");
    await user.clear(textareaA);
    await user.type(textareaA, "stale draft body");
    expect(screen.getByDisplayValue(/stale draft body/)).toBeInTheDocument();

    // Navigate to message B by updating the URL params and rerendering.
    setSearchParams({ email: "support@acme.io", id: "msg_other" });
    rerender(<AgentMessageFocusPage />);

    // Message B's body must show, and the stale draft from A must not.
    await waitFor(() => {
      expect(screen.getByText(/Different body content/)).toBeInTheDocument();
    });
    expect(screen.queryByDisplayValue(/stale draft body/)).not.toBeInTheDocument();
  });

  it("Reject confirm flow posts the reason and redirects to the thread", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });
    let rejectBody: string | null = null;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/api/v1/messages/msg_pending") && !url.endsWith("/approve") && !url.endsWith("/reject")) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve(OUTBOUND_PENDING),
        });
      }
      if (url.endsWith("/reject") && init?.method === "POST") {
        rejectBody = (init.body as string) || "";
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ status: "rejected", message_id: "msg_pending" }),
        });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });

    // First click → opens the inline confirm prompt with a reason field.
    await user.click(screen.getByRole("button", { name: /^Reject$/ }));
    const reasonInput = screen.getByPlaceholderText(/Reason for rejection/i);
    await user.type(reasonInput, "off-topic");
    // Second click on Confirm reject fires the API.
    await user.click(screen.getByRole("button", { name: /Confirm reject/i }));

    await waitFor(() => {
      expect(rejectBody).not.toBeNull();
    });
    expect(rejectBody).toContain('"reason":"off-topic"');
    expect(mockRouterPush).toHaveBeenCalledWith(
      expect.stringContaining("/dashboard/agents/messages?email=support%40acme.io"),
    );
  });
});
