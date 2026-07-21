// Focus-page contract: renders for outbound pending + inbound,
// Headers collapsible respects ?headers=1, Approve calls the API +
// redirects, ⌘↵ triggers Approve, missing params surface an error.
//
// In /v1 there's one agent-scoped detail endpoint
// (GET /v1/agents/{address}/messages/{id} → MessageView). That detail
// shape carries `direction` and `review_status`. URL copies remain only as a
// compatibility fallback for older deep links and cached payloads.

import { render, screen, waitFor, within } from "../../../../../../test-utils/swr";
import { render as rawRender } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { mutate } from "swr";
import AgentMessageFocusPage from "./page";
import { PendingRow } from "../../../../reviews/_components/PendingRow";
import { ThreadBubble } from "../../../../../components/messages/ThreadBubble";
import type {
  MessageSummary,
  PendingMessageSummary,
} from "../../../../../components/types";
import { agentsKey, messageDetailKey } from "../../../../../../lib/swrKeys";

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
const mockInvalidateMessageDetail = jest.fn();
jest.mock("../../../../../../lib/swrKeys", () => {
  const actual = jest.requireActual("../../../../../../lib/swrKeys");
  return {
    ...actual,
    invalidateAgentMessages: (email: string) =>
      mockInvalidateAgentMessages(email),
    invalidateMessageDetail: (id: string) => mockInvalidateMessageDetail(id),
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

const AGENT_EMAIL = "support@acme.io";

// REAL MessageView wire (GET /v1/agents/{address}/messages/{id}) for a
// held outbound draft. Body comes through `body.text`.
const OUTBOUND_PENDING = {
  id: "msg_pending",
  direction: "outbound",
  header_from: AGENT_EMAIL,
  envelope_from: null,
  verified_domain: null,
  authentication: null,
  to: ["maya@stripe.com"],
  cc: [],
  reply_to: [],
  delivered_to: "maya@stripe.com",
  subject: "Re: Q3 contract renewal",
  conversation_id: "conv_K3p9aQ",
  read_status: "",
  review_status: "pending_review",
  created_at: minutesAgo(13),
  body: {
    text: "Hi Maya,\n\nThanks for sending over the renewal draft…\n\nBest,\nAcme Support",
  },
};

const INBOUND_DETAIL = {
  id: "msg_in1",
  direction: "inbound",
  header_from: "maya@stripe.com",
  envelope_from: "bounce@stripe.com",
  verified_domain: "stripe.com",
  authentication: {
    spf: { status: "pass", domain: "stripe.com", aligned: true },
    dkim: [],
    dmarc: { status: "pass", domain: "stripe.com", policy: "reject", aligned_by: ["spf"] },
  },
  to: [AGENT_EMAIL],
  cc: [],
  reply_to: [],
  delivered_to: AGENT_EMAIL,
  subject: "Q3 contract renewal",
  conversation_id: "conv_K3p9aQ",
  read_status: "read",
  review_status: "",
  created_at: minutesAgo(25),
  raw_message: btoa(
    "From: maya@stripe.com\r\n" +
      "To: support@acme.io\r\n" +
      "Subject: Q3 contract renewal\r\n" +
      "\r\n" +
      "Attached is the renewal contract for Q3.",
  ),
};

// True if the URL targets the agent-scoped detail GET (not an
// approve/reject sub-resource).
function isDetailGet(url: string): boolean {
  return (
    url.includes("/v1/agents/") &&
    url.includes("/messages/") &&
    !url.endsWith("/approve") &&
    !url.endsWith("/reject")
  );
}

function jsonText(body: unknown, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    text: () => Promise.resolve(JSON.stringify(body)),
  });
}

// Stage the single detail GET to return a given MessageView.
function mockDetail(payload: Record<string, unknown>) {
  mockFetch.mockImplementation((url: string) => {
    if (isDetailGet(url)) return jsonText(payload);
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
}

function mockApproveSuccess() {
  let calls = 0;
  mockFetch.mockImplementation((url: string, init?: RequestInit) => {
    if (url.endsWith("/approve") && init?.method === "POST") {
      calls++;
      return jsonText({ status: "sent", message_id: "msg_pending" });
    }
    if (isDetailGet(url) && url.includes("msg_pending")) {
      return jsonText(OUTBOUND_PENDING);
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
  mockInvalidateMessageDetail.mockReset();
});

afterEach(() => {
  jest.useRealTimers();
});

describe("AgentMessageFocusPage", () => {
  it("renders the outbound pending detail with subject, identity row, and action card", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    mockDetail(OUTBOUND_PENDING);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    expect(screen.getByTestId("message-focus").dataset.direction).toBe("outbound");
    expect(screen.getByTestId("message-focus").dataset.status).toBe("pending_review");
    expect(screen.getByRole("heading", { name: /Re: Q3 contract renewal/ })).toBeInTheDocument();
    expect(screen.getByTestId("action-card")).toBeInTheDocument();
    expect(screen.getByText(/Awaiting your approval/)).toBeInTheDocument();
  });

  it("renders the headers section open when ?headers=1", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1", headers: "1" });
    mockDetail(OUTBOUND_PENDING);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    // Collapsible button has aria-expanded="true" when open.
    const headerButton = screen.getByRole("button", { name: /Full headers/i });
    expect(headerButton).toHaveAttribute("aria-expanded", "true");
  });

  it("renders an inbound message (?direction=inbound) via the agent-scoped detail endpoint", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_in1", direction: "inbound" });
    mockDetail(INBOUND_DETAIL);

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

  it("renders DMARC fail as a danger state in the inbound headers", async () => {
    setSearchParams({ email: AGENT_EMAIL, id: "msg_dmarc_fail", direction: "inbound", headers: "1" });
    mockDetail({
      ...INBOUND_DETAIL,
      id: "msg_dmarc_fail",
      verified_domain: null,
      authentication: {
        ...INBOUND_DETAIL.authentication,
        dmarc: {
          status: "fail",
          domain: "stripe.com",
          policy: "reject",
          aligned_by: [],
        },
      },
    });

    render(<AgentMessageFocusPage />);

    const verdict = await screen.findByText("DMARC: fail");
    expect(verdict).toHaveStyle({ color: "var(--danger-strong)" });
    expect(screen.queryByText("Auth verified")).not.toBeInTheDocument();
  });

  it("shows the authentication badge only for a verified domain", async () => {
    setSearchParams({ email: AGENT_EMAIL, id: "msg_verified", direction: "inbound" });
    mockDetail({ ...INBOUND_DETAIL, id: "msg_verified", verified_domain: "stripe.com" });

    render(<AgentMessageFocusPage />);

    expect(await screen.findByText("Auth verified")).toBeInTheDocument();
  });

  it("scrubs authentication identity fields in the copyable header view", async () => {
    setSearchParams({ email: AGENT_EMAIL, id: "msg_dirty_auth", direction: "inbound", headers: "1" });
    mockDetail({
      ...INBOUND_DETAIL,
      id: "msg_dirty_auth",
      authentication: {
        ...INBOUND_DETAIL.authentication,
        spf: { status: "pass", domain: "stripe.com\r\nInjected", aligned: true },
        dkim: [{ status: "pass", domain: "stripe.com\u0000Injected", selector: "s1\nInjected", aligned: true }],
      },
    });

    render(<AgentMessageFocusPage />);

    expect(await screen.findByText("SPF: pass · stripe.com Injected")).toBeInTheDocument();
    expect(screen.getByText("DKIM: pass · stripe.com Injected · s1 Injected")).toBeInTheDocument();
  });

  it("renders missing SMTP authentication as an explicit warning", async () => {
    setSearchParams({ email: AGENT_EMAIL, id: "msg_providerless", direction: "inbound", headers: "1" });
    mockDetail({
      ...INBOUND_DETAIL,
      id: "msg_providerless",
      authentication: null,
      verified_domain: null,
    });

    render(<AgentMessageFocusPage />);

    const verdict = await screen.findByText(
      "DMARC: not evaluated — no authenticating inbound SMTP peer",
    );
    expect(verdict).toHaveStyle({ color: "var(--warn-strong)" });
  });

  it("prefers the backend-parsed text over the raw MIME body for inbound", async () => {
    // Regression (#294): inbound rows carry the clean body in `parsed.text`
    // (text/plain or HTML→text, quoted-printable decoded). The bug rendered the
    // raw MIME body instead — showing literal <div>/<br> markup and `=` QP
    // soft-breaks.
    setSearchParams({ email: "support@acme.io", id: "msg_in2", direction: "inbound" });
    mockDetail({
      ...INBOUND_DETAIL,
      id: "msg_in2",
      parsed: { text: "Decoded body via parsed.text" },
      raw_message: btoa(
        "Content-Type: text/html\r\n" +
          "Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
          "<div>Raw MIME body should not show<br>=</div>",
      ),
    });

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    expect(screen.getByText(/Decoded body via parsed\.text/)).toBeInTheDocument();
    expect(screen.queryByText(/Raw MIME body should not show/)).not.toBeInTheDocument();
  });

  it("renders an HTML inbound body in a sandboxed iframe, not as raw markup", async () => {
    // parsed.html carries the decoded text/html part; the body card renders it
    // sanitized inside a sandboxed <iframe>, never as escaped <div>/<br> text.
    setSearchParams({ email: "support@acme.io", id: "msg_in3", direction: "inbound" });
    mockDetail({
      ...INBOUND_DETAIL,
      id: "msg_in3",
      parsed: { text: "Hello there", html: "<div>Hello <b>there</b></div>" },
    });

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    const frame = screen.getByTitle("Email body") as HTMLIFrameElement;
    expect(frame.getAttribute("sandbox")).toBe("allow-same-origin allow-popups");
    expect(frame.srcdoc).toContain("Hello <b>there</b>");
    // No literal markup leaked into the page as text.
    expect(screen.queryByText(/<div>Hello/)).not.toBeInTheDocument();
  });

  // Regression: GET /agents/{email}/messages/{id} flips inbox_status
  // unread → read as a server-side side effect. The focus page must
  // invalidate the per-agent inbox SWR cache when that happens, or
  // the inbox view will keep showing the row as unread until
  // window-focus revalidation eventually catches up. Pre-SWR this
  // was free (every navigation refetched the inbox); post-SWR it
  // needs an explicit cache invalidation.
  it("invalidates the per-agent inbox cache after a successful inbound load", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_in1", direction: "inbound" });
    mockDetail(INBOUND_DETAIL);

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

  it("clicking Approve POSTs to /v1/reviews/{id}/approve and redirects to the thread", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
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
      expect.stringContaining("/inboxes/messages?email=support%40acme.io"),
    );
  });

  // The other half of the invalidation contract: swrKeys.test.ts proves the
  // predicate matches the real key, but nothing proved this CALL SITE passes
  // the message id. It's an easy thing to get wrong (every neighbouring
  // invalidation in refreshAfterMutation is keyed by `email`) and it fails
  // silently — an approved message would keep rendering its stale
  // "Pending review" detail with no error anywhere.
  it("approve invalidates the message-detail entry by message id, not by inbox", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    mockApproveSuccess();
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });

    render(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(screen.getByTestId("action-card")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: /Approve & send/i }));

    await waitFor(() => {
      expect(mockInvalidateMessageDetail).toHaveBeenCalledWith("msg_pending");
    });
  });

  it("⌘↵ keyboard shortcut triggers Approve when status is pending", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
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

  it("deep link uses the detail payload direction and review status", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending" });
    mockDetail(OUTBOUND_PENDING);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    expect(screen.getByTestId("message-focus").dataset.direction).toBe("outbound");
    expect(screen.getByTestId("message-focus").dataset.status).toBe("pending_review");
    expect(screen.getByTestId("action-card")).toBeInTheDocument();
  });

  it("keeps accepting redundant direction and pending query parameters", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    mockDetail(OUTBOUND_PENDING);

    render(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    expect(screen.getByTestId("message-focus").dataset.direction).toBe("outbound");
    expect(screen.getByTestId("message-focus").dataset.status).toBe("pending_review");
    expect(screen.getByTestId("action-card")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Approve & send/i })).toBeInTheDocument();
  });

  it("surfaces a clear error when ?email or ?id is missing", async () => {
    setSearchParams({});

    render(<AgentMessageFocusPage />);

    expect(screen.getByText(/Missing \?email= or \?id=/)).toBeInTheDocument();
  });

  // A 5xx from the detail endpoint surfaces the server's error message
  // directly (no silent fallback masking it as "not found").
  it("surfaces the error message when the detail endpoint returns 500", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_unknown" });
    mockFetch.mockImplementation((url: string) => {
      if (isDetailGet(url)) {
        return Promise.resolve({
          ok: false,
          status: 500,
          text: () => Promise.resolve("internal server error"),
        });
      }
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
  });

  it("submits the edited body_text in the approve overrides when Edit + Approve is used", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });
    let approveBody: string | null = null;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (isDetailGet(url) && url.includes("msg_pending")) {
        return jsonText(OUTBOUND_PENDING);
      }
      if (url.endsWith("/approve") && init?.method === "POST") {
        approveBody = (init.body as string) || "";
        return jsonText({ status: "sent", message_id: "msg_pending" });
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
    expect(approveBody).toContain('"text":"Edited body"');
  });

  // Regression for H3: previously the draft-body textarea was seeded
  // only from SWR's onSuccess callback. onSuccess fires after a real
  // fetch — not on a cache hit served within dedupingInterval. If
  // another surface (e.g. the review queue) populated
  // messageDetailKey(id) just before the user navigated here, the
  // focus page would render data from cache, skip the fetcher, never
  // call onSuccess, and leave draftBody as "" — the reviewer would
  // click Edit and see a blank textarea instead of the agent's body.
  // The effect-based seed runs on every data change (including the
  // synchronous cache-hit case), so warm-cache navigation seeds too.
  //
  // To genuinely reproduce the bug condition (vs asserting the
  // post-fix shape on a fetch path that pre-fix code would also have
  // passed), we render WITHOUT the test-utils/swr fresh-Map wrapper,
  // use the module-level SWR cache, pre-seed messageDetailKey(id)
  // before mounting, and assert mockFetch was never called for the
  // outbound endpoint. A pre-fix implementation (onSuccess-only seed)
  // would observe data via the cache but never fire onSuccess, so
  // the textarea would be empty and the assertion would fail.
  it("seeds the textarea from pre-populated SWR cache without firing a fetch (true cache-hit regression)", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    // Pre-seed both caches the page subscribes to. The
    // jest.setup.ts afterEach nukes the module-level cache between
    // tests, so these seeds are isolated to this test. The agents
    // cache is now read by LifecycleSection to decide whether to
    // render the HITL step — without seeding it the page would
    // trigger a /api/inboxes fetch and defeat the cache-
    // hit reproduction below.
    // The cached value is the RAW wire (what both detail endpoints
    // return); the page projects it. Seeding a projection here would no
    // longer match what any fetcher writes.
    await mutate(
      messageDetailKey("msg_pending"),
      { ...OUTBOUND_PENDING, id: "msg_pending" },
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

  // Regression: the review queue and this focus page show the SAME
  // message through DIFFERENT endpoints — the review read
  // (GET /v1/reviews/{id}, the only one carrying hold_reason) and the
  // agent-scoped read (GET /v1/agents/{address}/messages/{id}, the only
  // one that flips unread → read). Both share one per-message cache entry.
  //
  // They used to cache their own PROJECTED shapes under that shared key:
  // the queue wrote a flat PendingMessageDetail, this page expected
  // { direction, data }. So expanding a held message in Pending and then
  // clicking through here handed this page the queue's shape — `direction`
  // was set but `data` was undefined, so reading `msg.data.conversation_id`
  // threw to the 500 error boundary. Caching the raw wire makes the shape
  // uniform no matter which surface fills the entry first.
  //
  // Both components render against the module-level SWR cache (no
  // fresh-Map wrapper), so the second genuinely reads what the first wrote
  // — reintroducing a projection in either fetcher fails this test.
  it("renders after the review queue cached the same message (cross-surface shape regression)", async () => {
    const REVIEW_WIRE = {
      ...OUTBOUND_PENDING,
      review_status: "pending_review",
      hold_reason: {
        type: "gate",
        code: "recipient_gate",
        summary: "One or more recipients aren't allowed by the inbox policy.",
      },
    };
    mockFetch.mockImplementation((url: string) => {
      if (url === "/v1/reviews/msg_pending") return jsonText(REVIEW_WIRE);
      if (isDetailGet(url) && url.includes("msg_pending"))
        return jsonText(OUTBOUND_PENDING);
      return Promise.resolve({
        ok: false,
        status: 404,
        text: () => Promise.resolve("not found"),
      });
    });

    const summary: PendingMessageSummary = {
      id: "msg_pending",
      agent_email: AGENT_EMAIL,
      direction: "outbound",
      subject: OUTBOUND_PENDING.subject,
      to: OUTBOUND_PENDING.to,
      status: "pending_review",
      created_at: OUTBOUND_PENDING.created_at,
    };

    // 1. The review queue fills the shared entry first.
    rawRender(
      <PendingRow
        summary={summary}
        expanded
        onToggle={() => {}}
        onResolved={() => {}}
      />,
    );
    await waitFor(() => {
      expect(
        screen.getByText(/Thanks for sending over the renewal draft/),
      ).toBeInTheDocument();
    });

    // 2. "Review →" lands here, reading that same entry.
    setSearchParams({
      email: AGENT_EMAIL,
      id: "msg_pending",
      direction: "outbound",
      pending: "1",
    });
    rawRender(<AgentMessageFocusPage />);

    await waitFor(() => {
      expect(screen.getByTestId("message-focus")).toBeInTheDocument();
    });
    // The outbound projection resolved: subject and approve/reject render
    // rather than the error boundary.
    expect(screen.getByTestId("action-card")).toBeInTheDocument();
    expect(
      screen.getAllByText(OUTBOUND_PENDING.subject).length,
    ).toBeGreaterThan(0);
  });

  // The REVERSE of the test above, and the ordering that actually happens
  // most often (open a message, then go to Pending). Two things are pinned
  // here and they fail independently:
  //
  //  1. Shape. The focus page fills the shared entry first; the review row
  //     then reads what IT wrote. Before the fix each surface cached its own
  //     projection, so the row would find the focus page's { direction, data }
  //     wrapper where it expected a flat detail and render "(empty body)".
  //
  //  2. Graceful degradation. The two endpoints return different supersets:
  //     only GET /v1/reviews/{id} populates hold_reason. A wire written by
  //     the agent-scoped read therefore has none, and the "Why this message
  //     was held" banner survives only because PendingRow falls back to the
  //     summary row's hold_reason. This was confirmed by hand in a browser
  //     and had no test.
  //
  // The review endpoint is staged to fail and asserted un-called, so the
  // banner provably comes from the summary fallback and not from a
  // revalidation quietly refilling the entry with the superset.
  it("the review row renders on a cache entry the focus page filled (reverse ordering, hold reason via summary fallback)", async () => {
    // Own id: SWR's request-dedup window is keyed globally and outlives the
    // cache reset in jest.setup.ts, so a key another test already fetched
    // would never fetch here and the entry would stay empty.
    const id = "msg_pending_reverse";
    const WIRE = { ...OUTBOUND_PENDING, id };
    mockFetch.mockImplementation((url: string) => {
      if (isDetailGet(url) && url.includes(id)) return jsonText(WIRE);
      return Promise.resolve({
        ok: false,
        status: 500,
        text: () =>
          Promise.resolve("the review read must not supply hold_reason here"),
      });
    });

    // 1. The focus page fills the shared entry (no hold_reason on this wire).
    setSearchParams({
      email: AGENT_EMAIL,
      id,
      direction: "outbound",
      pending: "1",
    });
    const { container: focusEl } = rawRender(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(within(focusEl).getByTestId("message-focus")).toBeInTheDocument();
    });

    // 2. The user opens Pending; the row reads that same entry.
    const summary: PendingMessageSummary = {
      id,
      agent_email: AGENT_EMAIL,
      direction: "outbound",
      subject: WIRE.subject,
      to: WIRE.to,
      status: "pending_review",
      created_at: WIRE.created_at,
      hold_reason: {
        type: "gate",
        code: "recipient_gate",
        summary: "One or more recipients aren't allowed by the inbox policy.",
      },
    };
    const { container } = rawRender(
      <PendingRow
        summary={summary}
        expanded
        onToggle={() => {}}
        onResolved={() => {}}
      />,
    );
    // Scope queries to the row — the focus page is still mounted and renders
    // the same body/subject text.
    const row = within(container);

    // Shape: the row projected the focus page's wire into its own view.
    await waitFor(() => {
      expect(
        row.getByText(/Thanks for sending over the renewal draft/),
      ).toBeInTheDocument();
    });
    // Degradation: the banner renders off summary.hold_reason.
    expect(row.getByText("Why this message was held")).toBeInTheDocument();
    expect(
      row.getAllByText(/recipients aren't allowed by the inbox policy/).length,
    ).toBeGreaterThan(0);
    // …and provably not off a review-endpoint revalidation.
    expect(
      mockFetch.mock.calls.some((c) =>
        String(c[0]).startsWith("/v1/reviews/"),
      ),
    ).toBe(false);
  });

  // ThreadBubble is the THIRD surface on the shared per-message entry, and
  // the easiest one to regress: it shares both the key AND the endpoint with
  // the focus page, so a projection cached here reads back as valid-looking
  // data everywhere except the surface that expects the other shape. The
  // component's own test file mocks getMessageDetailWire, so nothing there
  // exercises the real key or the real cache — these two do.
  // A distinct id per cross-surface test: SWR's request-dedup window is
  // keyed globally and outlives the cache reset in jest.setup.ts, so reusing
  // an id another test already fetched suppresses the fetch here and the
  // entry never fills.
  const inboundSummary = (id: string): MessageSummary => ({
    id,
    direction: "inbound",
    from: "maya@stripe.com",
    to: [AGENT_EMAIL],
    recipient: AGENT_EMAIL,
    subject: "Q3 contract renewal",
    status: "read",
    created_at: INBOUND_DETAIL.created_at,
  });

  it("the focus page renders on a cache entry ThreadBubble filled", async () => {
    const id = "msg_bubble_first";
    mockDetail({
      ...INBOUND_DETAIL,
      id,
      parsed: { text: "thread bubble body" },
    });

    // Wait for the bubble's BODY, not just its container: the body is the
    // first thing that proves the fetch resolved and the shared entry is
    // actually filled before the focus page subscribes to it.
    const { container: bubbleEl } = rawRender(
      <ThreadBubble
        message={inboundSummary(id)}
        counterparty={{ email: "maya@stripe.com", name: "Maya" }}
        agentEmail={AGENT_EMAIL}
      />,
    );
    await waitFor(() => {
      expect(
        within(bubbleEl).getByText(/thread bubble body/),
      ).toBeInTheDocument();
    });

    setSearchParams({ email: AGENT_EMAIL, id, direction: "inbound" });
    const { container } = rawRender(<AgentMessageFocusPage />);
    const focus = within(container);

    await waitFor(() => {
      expect(focus.getByTestId("message-focus")).toBeInTheDocument();
    });
    // The inbound projection resolved off the bubble's cached wire — a
    // cached projection would leave the focus page's body card empty.
    expect(focus.getByText(/thread bubble body/)).toBeInTheDocument();
  });

  it("ThreadBubble renders on a cache entry the focus page filled", async () => {
    const id = "msg_focus_first";
    mockDetail({ ...INBOUND_DETAIL, id, parsed: { text: "focus page body" } });

    setSearchParams({ email: AGENT_EMAIL, id, direction: "inbound" });
    const { container: focusEl } = rawRender(<AgentMessageFocusPage />);
    await waitFor(() => {
      expect(within(focusEl).getByTestId("message-focus")).toBeInTheDocument();
    });

    const { container } = rawRender(
      <ThreadBubble
        message={inboundSummary(id)}
        counterparty={{ email: "maya@stripe.com", name: "Maya" }}
        agentEmail={AGENT_EMAIL}
      />,
    );

    await waitFor(() => {
      expect(within(container).getByText(/focus page body/)).toBeInTheDocument();
    });
  });

  // Regression: navigating from message A to message B via ?id= must
  // reset the inner per-message state (draftBody, editingDraft,
  // hasUserEditedRef, rejectReason). Before keying FocusContent by
  // `${email}|${id}`, an edit-in-progress on A would bleed into B's
  // view and the user would see A's stale draft superimposed on B.
  it("resets per-message state when ?id changes (no draft bleed across navigation)", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });

    const OTHER = {
      ...OUTBOUND_PENDING,
      id: "msg_other",
      subject: "Different subject",
      body: { text: "Different body content" },
    };
    mockFetch.mockImplementation((url: string) => {
      if (isDetailGet(url) && url.includes("msg_pending")) {
        return jsonText(OUTBOUND_PENDING);
      }
      if (isDetailGet(url) && url.includes("msg_other")) {
        return jsonText(OTHER);
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
    setSearchParams({ email: "support@acme.io", id: "msg_other", direction: "outbound", pending: "1" });
    rerender(<AgentMessageFocusPage />);

    // Message B's body must show, and the stale draft from A must not.
    await waitFor(() => {
      expect(screen.getByText(/Different body content/)).toBeInTheDocument();
    });
    expect(screen.queryByDisplayValue(/stale draft body/)).not.toBeInTheDocument();
  });

  it("Reject confirm flow posts the reason and redirects to the thread", async () => {
    setSearchParams({ email: "support@acme.io", id: "msg_pending", direction: "outbound", pending: "1" });
    const user = userEvent.setup({ advanceTimers: jest.advanceTimersByTime });
    let rejectBody: string | null = null;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (isDetailGet(url) && url.includes("msg_pending")) {
        return jsonText(OUTBOUND_PENDING);
      }
      if (url.endsWith("/reject") && init?.method === "POST") {
        rejectBody = (init.body as string) || "";
        return jsonText({ status: "review_rejected", message_id: "msg_pending" });
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
      expect.stringContaining("/inboxes/messages?email=support%40acme.io"),
    );
  });
});
