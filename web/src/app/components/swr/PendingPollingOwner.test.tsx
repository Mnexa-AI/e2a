import useSWR from "swr";
import { pendingPolling } from "../../../lib/livePolling";
import { pendingMessagesKey } from "../../../lib/swrKeys";
import { listPendingMessages } from "../onboarding/api";
import { PendingPollingOwner } from "./PendingPollingOwner";

jest.mock("swr", () => ({
  __esModule: true,
  default: jest.fn(() => ({ data: undefined, error: undefined })),
}));

jest.mock("../onboarding/api", () => ({
  listPendingMessages: jest.fn(),
}));

it("owns the shared pending-list polling interval", () => {
  expect(PendingPollingOwner()).toBeNull();

  expect(useSWR).toHaveBeenCalledWith(
    pendingMessagesKey,
    listPendingMessages,
    pendingPolling,
  );
});
