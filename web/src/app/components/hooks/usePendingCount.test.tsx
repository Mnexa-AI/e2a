// usePendingCount refresh contract.
//
// The Sidebar's pending badge needs to stay fresh after the user
// approves / rejects a draft elsewhere in the app. Pin the three
// refresh triggers so a future drive-by doesn't accidentally drop
// any of them:
//   1. pathname change
//   2. document visibilitychange (visible)
//   3. 30s interval

import { renderHook, act, waitFor } from "@testing-library/react";
import { usePendingCount } from "./usePendingCount";

const mockUsePathname = jest.fn();
jest.mock("next/navigation", () => ({
  usePathname: () => mockUsePathname(),
}));

const mockFetch = jest.fn();
global.fetch = mockFetch;

function mockPendingCount(count: number) {
  const messages = Array.from({ length: count }, (_, i) => ({
    id: `msg_${i}`,
    agent_id: "ag_1",
    direction: "outbound",
    subject: `S${i}`,
    to: [],
    status: "pending_approval",
    created_at: new Date().toISOString(),
  }));
  mockFetch.mockResolvedValue({
    ok: true,
    status: 200,
    json: () => Promise.resolve({ messages }),
  });
}

beforeEach(() => {
  mockFetch.mockReset();
  mockUsePathname.mockReturnValue("/dashboard");
});

describe("usePendingCount", () => {
  it("returns null until the first fetch settles, then the count", async () => {
    mockPendingCount(3);
    const { result } = renderHook(() => usePendingCount());
    expect(result.current).toBeNull();
    await waitFor(() => {
      expect(result.current).toBe(3);
    });
  });

  it("refetches when the pathname changes (catches post-approve navigation)", async () => {
    mockPendingCount(2);
    const { result, rerender } = renderHook(() => usePendingCount());
    await waitFor(() => {
      expect(result.current).toBe(2);
    });

    // The user approves a pending draft, sidebar still shows "2".
    // Then they navigate from /dashboard/agents/messages/view to
    // /dashboard/agents/messages — pathname changes, sidebar refetches.
    mockPendingCount(1);
    mockUsePathname.mockReturnValue("/dashboard/agents/messages");
    rerender();
    await waitFor(() => {
      expect(result.current).toBe(1);
    });
  });

  it("refetches when the tab becomes visible again", async () => {
    mockPendingCount(2);
    const { result } = renderHook(() => usePendingCount());
    await waitFor(() => {
      expect(result.current).toBe(2);
    });

    // External mutation (CLI / other tab) drops the count to 0.
    mockPendingCount(0);
    Object.defineProperty(document, "visibilityState", {
      value: "visible",
      configurable: true,
    });
    act(() => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await waitFor(() => {
      expect(result.current).toBe(0);
    });
  });

  it("returns null when the fetch errors (distinguishable from zero)", async () => {
    mockFetch.mockRejectedValue(new Error("network"));
    const { result } = renderHook(() => usePendingCount());
    await waitFor(() => {
      expect(result.current).toBeNull();
    });
  });
});
