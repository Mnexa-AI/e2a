import { render } from "@testing-library/react";
import useSWR from "swr";
import { pendingMessagesKey } from "../../../lib/swrKeys";
import { listPendingMessages } from "../../components/onboarding/api";
import PendingPage from "./page";

jest.mock("swr", () => ({
  __esModule: true,
  default: jest.fn(() => ({ data: [], error: undefined, isLoading: false })),
  mutate: jest.fn(),
}));

jest.mock("next/navigation", () => ({
  useSearchParams: () => ({ get: () => null }),
  useRouter: () => ({ replace: jest.fn() }),
}));

jest.mock("../../components/onboarding/api", () => ({
  listPendingMessages: jest.fn(),
}));

jest.mock("../../components/loft/PageShell", () => ({
  PageShell: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

jest.mock("./_components/PendingRow", () => ({
  PendingRow: () => null,
}));

it("subscribes to pending messages without owning a polling interval", () => {
  render(<PendingPage />);

  expect(useSWR).toHaveBeenCalledWith(
    pendingMessagesKey,
    listPendingMessages,
  );

  listPendingMessages();
  expect(listPendingMessages).toHaveBeenCalledTimes(1);
});
