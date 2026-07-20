import useSWR from "swr";
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

it("subscribes to pending messages without owning a polling interval", () => {
  usePendingCount();

  expect(mockUseSWR).toHaveBeenCalledWith(
    pendingMessagesKey,
    listPendingMessages,
  );

  listPendingMessages();
  expect(mockListPendingMessages).toHaveBeenCalledTimes(1);
});

it("keeps the last successful count when revalidation fails", () => {
  mockUseSWR.mockReturnValueOnce({
    data: [{ id: "msg_1" }, { id: "msg_2" }],
    error: new Error("transient"),
  } as ReturnType<typeof useSWR>);

  expect(usePendingCount()).toBe(2);
});
