// Pending queue contract: the page must subscribe to the shared
// `pendingMessagesKey` SWR cache so a single fetch is shared with
// the Sidebar badge (usePendingCount). Before the SWR migration the
// page kept its own useState/useEffect copy and double-fetched the
// /api/v1/messages/pending endpoint on every load.

import {
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import { mutate } from "swr";
import PendingPage from "./page";
import { pendingMessagesKey } from "../../../../lib/swrKeys";

jest.mock("next/navigation", () => ({
  useSearchParams: () => ({ get: () => null }),
  useRouter: () => ({ push: jest.fn(), replace: jest.fn() }),
}));

const mockFetch = jest.fn();
global.fetch = mockFetch;

const SAMPLE = {
  id: "msg_1",
  agent_id: "ag_1",
  direction: "outbound",
  subject: "Sample pending subject",
  to: ["alice@example.com"],
  status: "pending_approval",
  approval_expires_at: "2099-01-01T00:00:00Z",
  created_at: "2026-05-23T00:00:00Z",
};

beforeEach(async () => {
  mockFetch.mockReset();
  // Default cache leaks across tests in the same file; nuke it so each
  // test starts in the "no data yet" state.
  await mutate(() => true, undefined, { revalidate: false });
});

describe("PendingPage SWR subscription", () => {
  it("reflects external mutate() to pendingMessagesKey (proves the page is a SWR subscriber, not local-state)", async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ messages: [SAMPLE] }),
    });

    render(<PendingPage />);

    await waitFor(() => {
      expect(screen.getByText("Sample pending subject")).toBeInTheDocument();
    });

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
      screen.getByText(/No messages are waiting for approval/),
    ).toBeInTheDocument();
  });
});
