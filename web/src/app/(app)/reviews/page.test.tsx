// Pending queue contract: the page must subscribe to the shared
// `pendingMessagesKey` SWR cache so a single fetch is shared with
// the Sidebar badge (usePendingCount). In /v1 the queue is aggregated
// client-side from GET /v1/agents + per-agent outbound message lists,
// keeping rows whose `review_status === "pending_review"`. NOTE: on the
// real wire (MessageSummaryView) the pending state is in `review_status`,
// and `status` is the (empty) delivery rollup — filtering on `status`
// would surface nothing.

import {
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import { mutate } from "swr";
import PendingPage from "./page";
import { pendingMessagesKey } from "../../../lib/swrKeys";

jest.mock("next/navigation", () => ({
  useSearchParams: () => ({ get: () => null }),
  useRouter: () => ({ push: jest.fn(), replace: jest.fn() }),
}));

const mockFetch = jest.fn();
global.fetch = mockFetch;

const AGENT_EMAIL = "ag_1@agents.e2a.dev";

// A ReviewView row from GET /v1/reviews (the account-scoped review queue).
const SAMPLE_ROW = {
  id: "msg_1",
  agent: AGENT_EMAIL,
  direction: "outbound",
  from: AGENT_EMAIL,
  to: ["alice@example.com"],
  subject: "Sample pending subject",
  review_status: "pending_review",
  created_at: "2026-05-23T00:00:00Z",
};

// Stage GET /v1/reviews (one account-scoped call; no per-agent fan-out).
function stagePendingFetch() {
  mockFetch.mockImplementation((url: string) =>
    url === "/v1/reviews"
      ? Promise.resolve({
          ok: true,
          status: 200,
          text: () =>
            Promise.resolve(JSON.stringify({ items: [SAMPLE_ROW], next_cursor: null })),
        })
      : Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("nf") }),
  );
}

beforeEach(async () => {
  mockFetch.mockReset();
  // Default cache leaks across tests in the same file; nuke it so each
  // test starts in the "no data yet" state.
  await mutate(() => true, undefined, { revalidate: false });
});

describe("PendingPage SWR subscription", () => {
  // The SAMPLE_ROW staged here is the REAL wire shape for a held draft:
  // `status:""` + `review_status:"pending_review"`. This is also the
  // Bug 1 regression — the pre-fix queue filtered on
  // `status === "pending_review"`, which would have DROPPED this row
  // and rendered the empty state. The first `waitFor` below (the row's
  // subject is visible, i.e. NOT the empty state) fails against the old
  // `status` filter and passes only with the `review_status` filter.
  it("renders a review item from /v1/reviews and reflects external mutate() to pendingMessagesKey (proves it's an SWR subscriber, not local state)", async () => {
    stagePendingFetch();

    render(<PendingPage />);

    await waitFor(() => {
      expect(screen.getByText("Sample pending subject")).toBeInTheDocument();
    });
    // Bug 1 guard: the held draft (status:"") is surfaced, not the
    // empty state.
    expect(
      screen.queryByText(/Nothing waiting for review/),
    ).not.toBeInTheDocument();

    // External mutate to the shared key — if PendingPage is subscribed
    // via useSWR(pendingMessagesKey), it re-renders against the new
    // data without any further fetch. If it kept local useState (the
    // pre-migration pattern), this mutate would be invisible to it.
    await act(async () => {
      await mutate(pendingMessagesKey, [], { revalidate: false });
    });

    await waitFor(() => {
      expect(screen.queryByText("Sample pending subject")).not.toBeInTheDocument();
    });
    expect(
      screen.getByText(/Nothing waiting for review/),
    ).toBeInTheDocument();
  });
});
