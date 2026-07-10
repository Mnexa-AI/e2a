// AgentCard unread badge (Inboxes list, Option A). The card fires a
// per-inbox probe (getInboxUnread) and renders a red count badge. These
// pin the count/"99+"/hidden-at-zero rendering and the graceful-degrade
// on probe failure.

import { render, screen, waitFor } from "../../../../test-utils/swr";
import { AgentCard } from "./AgentCard";
import { getInboxUnread } from "../../../components/onboarding/api";
import type { DashboardAgent } from "../../../components/types";

jest.mock("next/link", () => {
  return function MockLink({
    href,
    children,
    ...props
  }: {
    href: string;
    children: React.ReactNode;
    [key: string]: unknown;
  }) {
    return (
      <a href={href} {...props}>
        {children}
      </a>
    );
  };
});

// Mock the probe but keep the real UNREAD_BADGE_CAP the component reads.
jest.mock("../../../components/onboarding/api", () => ({
  getInboxUnread: jest.fn(),
  UNREAD_BADGE_CAP: 99,
}));
const mockUnread = getInboxUnread as jest.MockedFunction<typeof getInboxUnread>;

const agent: DashboardAgent = {
  id: "ag_billing",
  domain: "acme.dev",
  email: "billing@acme.dev",
  name: "billing",
  domain_verified: true,
  created_at: "2026-05-10T00:00:00Z",
};

afterEach(() => mockUnread.mockReset());

it("renders the unread count as a red badge", async () => {
  mockUnread.mockResolvedValue({ count: 3, more: false });
  render(<AgentCard agent={agent} />);
  const badge = await screen.findByTitle("3 unread");
  expect(badge).toHaveTextContent("3");
});

it("caps the badge at 99+ when there are more unread than the cap", async () => {
  mockUnread.mockResolvedValue({ count: 99, more: true });
  render(<AgentCard agent={agent} />);
  const badge = await screen.findByTitle("99+ unread");
  expect(badge).toHaveTextContent("99+");
});

it("renders no badge when the inbox has zero unread", async () => {
  mockUnread.mockResolvedValue({ count: 0, more: false });
  render(<AgentCard agent={agent} />);
  // The identity still renders...
  await waitFor(() => expect(screen.getByText("billing@acme.dev")).toBeInTheDocument());
  // ...but no unread badge.
  expect(screen.queryByText("unread messages", { exact: false })).not.toBeInTheDocument();
});

it("degrades to no badge when the probe rejects", async () => {
  mockUnread.mockRejectedValue(new Error("boom"));
  render(<AgentCard agent={agent} />);
  await waitFor(() => expect(screen.getByText("billing@acme.dev")).toBeInTheDocument());
  expect(screen.queryByText("unread messages", { exact: false })).not.toBeInTheDocument();
});
