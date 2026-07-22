import useSWR from "swr";
import { unreadPolling } from "../../../lib/livePolling";
import { accountUnreadKey } from "../../../lib/swrKeys";
import { getInboxUnread, listAgents } from "../onboarding/api";
import { loadUnreadCount, useUnreadCount } from "./useUnreadCount";

jest.mock("swr", () => ({
  __esModule: true,
  default: jest.fn(() => ({ data: undefined, error: undefined })),
}));

jest.mock("../onboarding/api", () => ({
  getInboxUnread: jest.fn(),
  listAgents: jest.fn(),
  UNREAD_BADGE_CAP: 99,
}));

const mockUseSWR = useSWR as jest.MockedFunction<typeof useSWR>;
const mockGetInboxUnread = getInboxUnread as jest.MockedFunction<
  typeof getInboxUnread
>;
const mockListAgents = listAgents as jest.MockedFunction<typeof listAgents>;

const agent = (email: string) => ({
  domain: "agents.test",
  email,
  name: email,
  domain_verified: true,
  created_at: "2026-07-22T00:00:00Z",
});

beforeEach(() => {
  jest.clearAllMocks();
  mockUseSWR.mockReturnValue({
    data: undefined,
    error: undefined,
  } as ReturnType<typeof useSWR>);
});

describe("loadUnreadCount", () => {
  it("fetches every inbox in parallel and aggregates unread counts", async () => {
    mockListAgents.mockResolvedValue([
      agent("one@agents.test"),
      agent("two@agents.test"),
    ]);
    let resolveOne!: (value: { count: number; more: boolean }) => void;
    let resolveTwo!: (value: { count: number; more: boolean }) => void;
    mockGetInboxUnread
      .mockImplementationOnce(
        () => new Promise((resolve) => { resolveOne = resolve; }),
      )
      .mockImplementationOnce(
        () => new Promise((resolve) => { resolveTwo = resolve; }),
      );

    const pending = loadUnreadCount();
    await Promise.resolve();
    expect(mockGetInboxUnread).toHaveBeenNthCalledWith(1, "one@agents.test");
    expect(mockGetInboxUnread).toHaveBeenNthCalledWith(2, "two@agents.test");

    resolveOne({ count: 12, more: false });
    resolveTwo({ count: 7, more: false });
    await expect(pending).resolves.toEqual({ count: 19, more: false });
  });

  it("caps the account total at 99 and marks it as more", async () => {
    mockListAgents.mockResolvedValue([
      agent("one@agents.test"),
      agent("two@agents.test"),
    ]);
    mockGetInboxUnread
      .mockResolvedValueOnce({ count: 60, more: false })
      .mockResolvedValueOnce({ count: 50, more: false });

    await expect(loadUnreadCount()).resolves.toEqual({ count: 99, more: true });
  });

  it("propagates a per-agent more flag even below the account cap", async () => {
    mockListAgents.mockResolvedValue([agent("one@agents.test")]);
    mockGetInboxUnread.mockResolvedValue({ count: 8, more: true });

    await expect(loadUnreadCount()).resolves.toEqual({ count: 8, more: true });
  });

  it("returns zero without unread requests for an empty account", async () => {
    mockListAgents.mockResolvedValue([]);

    await expect(loadUnreadCount()).resolves.toEqual({ count: 0, more: false });
    expect(mockGetInboxUnread).not.toHaveBeenCalled();
  });
});

describe("useUnreadCount", () => {
  it("subscribes with the account key, loader, and exact unread polling config", () => {
    expect(useUnreadCount()).toBeNull();

    expect(mockUseSWR).toHaveBeenCalledWith(
      accountUnreadKey,
      loadUnreadCount,
      unreadPolling,
    );
  });

  it("returns null while no data is available", () => {
    mockUseSWR.mockReturnValueOnce({
      data: undefined,
      error: new Error("initial failure"),
    } as ReturnType<typeof useSWR>);

    expect(useUnreadCount()).toBeNull();
  });

  it("keeps stale data when revalidation reports an error", () => {
    const stale = { count: 14, more: false };
    mockUseSWR.mockReturnValueOnce({
      data: stale,
      error: new Error("transient"),
    } as ReturnType<typeof useSWR>);

    expect(useUnreadCount()).toEqual(stale);
  });
});
