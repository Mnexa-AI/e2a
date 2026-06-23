// Per-agent Settings page contract.
//
// Covers: required-param error, agent fetch, the three section
// editors (mode switch / webhook URL / HITL) wire up to PUT
// /api/dashboard/agents/{email}, and the danger-zone delete confirm
// flow (DELETE /v1/agents/{email}?confirm=DELETE) routes back to
// /dashboard on success.

import { render, screen, waitFor, fireEvent } from "../../../../../test-utils/swr";
import AgentSettingsPage from "./page";

const mockUseSearchParams = jest.fn();
const mockRouterPush = jest.fn();

jest.mock("next/navigation", () => ({
  useSearchParams: () => mockUseSearchParams(),
  useRouter: () => ({ push: mockRouterPush }),
}));

jest.mock("next/link", () => {
  return function MockLink({
    href,
    children,
    ...rest
  }: {
    href: string;
    children: React.ReactNode;
    [k: string]: unknown;
  }) {
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  };
});

const mockFetch = jest.fn();
global.fetch = mockFetch;

function setSearchParams(params: Record<string, string>) {
  mockUseSearchParams.mockReturnValue({
    get: (k: string) => params[k] ?? null,
  });
}

const baseAgent = {
  id: "ag_test",
  domain: "acme.com",
  email: "support@acme.com",
  name: "support",
  webhook_url: "https://acme.com/hook",
  agent_mode: "cloud",
  domain_verified: true,
  public: false,
  created_at: "2026-02-15T00:00:00Z",
  hitl_enabled: false,
  hitl_ttl_seconds: 604800,
  hitl_expiration_action: "reject" as const,
};

function mockAgent(agent: typeof baseAgent) {
  mockFetch.mockImplementation((url: string, init?: RequestInit) => {
    if (url === "/api/dashboard/agents" && !init?.method) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ agents: [agent] }),
      });
    }
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
}

beforeEach(() => {
  mockFetch.mockReset();
  mockRouterPush.mockReset();
  jest.spyOn(window, "confirm").mockReturnValue(true);
});

afterEach(() => {
  jest.restoreAllMocks();
});

describe("AgentSettingsPage", () => {
  it("surfaces a clear error when ?email is missing", async () => {
    setSearchParams({});
    render(<AgentSettingsPage />);
    expect(screen.getByText(/Missing \?email= query parameter/)).toBeInTheDocument();
  });

  it("renders the review-queue section and danger zone for a verified agent", async () => {
    setSearchParams({ email: baseAgent.email });
    mockAgent(baseAgent);

    render(<AgentSettingsPage />);

    await waitFor(() => {
      expect(screen.getByTestId("agent-settings")).toBeInTheDocument();
    });

    // Review queue (HITL TTL) section + collapsed editor summary.
    expect(screen.getByText("Review queue")).toBeInTheDocument();
    expect(screen.getByText(/Review window:/)).toBeInTheDocument();

    // The retired Mode + per-agent Webhook sections must not render.
    expect(screen.queryByText(/Delivery mode/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Switch to local/i)).not.toBeInTheDocument();
    expect(screen.queryByText("https://acme.com/hook")).not.toBeInTheDocument();

    // Danger zone
    expect(screen.getByTestId("danger-zone")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Delete agent/i })).toBeInTheDocument();
  });

  it("editing the review-queue TTL PUTs hitl_ttl_seconds + hitl_expiration_action", async () => {
    setSearchParams({ email: baseAgent.email });
    let putBody: string | null = null;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [baseAgent] }),
        });
      }
      if (
        url === `/api/dashboard/agents/${encodeURIComponent(baseAgent.email)}` &&
        init?.method === "PUT"
      ) {
        putBody = (init.body as string) ?? "";
        return Promise.resolve({ ok: true, status: 204 });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<AgentSettingsPage />);
    await waitFor(() => {
      expect(screen.getByText(/Review window:/)).toBeInTheDocument();
    });

    // Open the editor, pick the "1 hour" preset, save.
    fireEvent.click(screen.getByText("Edit"));
    fireEvent.click(screen.getByText("1 hour"));
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(putBody).not.toBeNull();
    });
    expect(putBody).toBe(
      JSON.stringify({ hitl_ttl_seconds: 3600, hitl_expiration_action: "reject" }),
    );
  });

  it("clicking 'Delete agent' DELETEs and routes back to /dashboard", async () => {
    setSearchParams({ email: baseAgent.email });
    let deleted = false;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [baseAgent] }),
        });
      }
      if (
        url === `/v1/agents/${encodeURIComponent(baseAgent.email)}?confirm=DELETE` &&
        init?.method === "DELETE"
      ) {
        deleted = true;
        return Promise.resolve({ ok: true, status: 204, text: () => Promise.resolve("") });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<AgentSettingsPage />);
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Delete agent/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /Delete agent/i }));

    await waitFor(() => {
      expect(deleted).toBe(true);
    });
    expect(mockRouterPush).toHaveBeenCalledWith("/dashboard");
  });

  it("aborts deletion when the confirm prompt is cancelled", async () => {
    setSearchParams({ email: baseAgent.email });
    (window.confirm as jest.Mock).mockReturnValue(false);

    let deleted = false;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [baseAgent] }),
        });
      }
      if (init?.method === "DELETE") {
        deleted = true;
        return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve("") });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<AgentSettingsPage />);
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Delete agent/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /Delete agent/i }));

    // Give any in-flight handler a tick to fire (it shouldn't, but make
    // the negative assertion deterministic).
    await new Promise((r) => setTimeout(r, 10));
    expect(deleted).toBe(false);
    expect(mockRouterPush).not.toHaveBeenCalled();
  });
});
