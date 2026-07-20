import useSWR from "swr";
import { pendingPolling } from "../../../lib/livePolling";
import { pendingMessagesKey } from "../../../lib/swrKeys";
import { listPendingMessages } from "../onboarding/api";
import { usePendingCount } from "./usePendingCount";

jest.mock("swr", () => ({
  __esModule: true,
  default: jest.fn(() => ({ data: [], error: undefined })),
}));

jest.mock("../onboarding/api", () => ({
  listPendingMessages: jest.fn(),
}));

const mockUseSWR = useSWR as jest.MockedFunction<typeof useSWR>;
const mockListPendingMessages = listPendingMessages as jest.MockedFunction<
  typeof listPendingMessages
>;

it("uses the shared pending polling policy", () => {
  usePendingCount();

  expect(mockUseSWR).toHaveBeenCalledWith(
    pendingMessagesKey,
    expect.any(Function),
    pendingPolling,
  );

  const fetcher = mockUseSWR.mock.calls[0][1] as () => unknown;
  fetcher();
  expect(mockListPendingMessages).toHaveBeenCalledTimes(1);
});
