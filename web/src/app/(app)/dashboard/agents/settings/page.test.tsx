// Per-agent Settings page contract.
//
// Covers: required-param error, agent fetch, the three section
// editors (mode switch / webhook URL / HITL) wire up to PUT
// /api/dashboard/agents/{email}, and the danger-zone delete confirm
// flow routes back to /dashboard on success.

import { render, screen, waitFor, fireEvent } from "../../../../../test-utils/swr";
import userEvent from "@testing-library/user-event";
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

  it("renders the three section editors for a cloud, verified agent", async () => {
    setSearchParams({ email: baseAgent.email });
    mockAgent(baseAgent);

    render(<AgentSettingsPage />);

    await waitFor(() => {
      expect(screen.getByTestId("agent-settings")).toBeInTheDocument();
    });

    // Mode section + AgentModeSwitcher button
    expect(screen.getByText(/Delivery mode/i)).toBeInTheDocument();
    expect(screen.getByText(/Switch to local/i)).toBeInTheDocument();

    // Webhook section + URL display
    expect(screen.getByText("Webhook", { selector: "span" })).toBeInTheDocument();
    expect(screen.getByText("https://acme.com/hook")).toBeInTheDocument();

    // HITL section. The editor starts in a collapsed summary ("HITL:
    // Disabled / Edit"); the "Require human approval" copy only
    // appears after Edit is clicked. Assert the summary state here —
    // clicking Edit is covered by the existing editor's own tests.
    expect(screen.getByText(/Human-in-the-loop approvals/i)).toBeInTheDocument();
    // Match within the HITL section to avoid colliding with the
    // dashboard nav's "Disabled" copy elsewhere on the page (none
    // exists on this page, but be explicit).
    expect(screen.getByText("HITL:")).toBeInTheDocument();

    // Danger zone
    expect(screen.getByTestId("danger-zone")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Delete agent/i })).toBeInTheDocument();
  });

  it("hides the Webhook section for a local agent", async () => {
    setSearchParams({ email: "bot@agents.e2a.dev" });
    mockAgent({
      ...baseAgent,
      email: "bot@agents.e2a.dev",
      agent_mode: "local",
      webhook_url: "",
    });

    render(<AgentSettingsPage />);

    await waitFor(() => {
      expect(screen.getByTestId("agent-settings")).toBeInTheDocument();
    });
    // The "Webhook" section header should NOT render for local agents
    // — the editor is cloud-only.
    expect(screen.queryByText("Webhook", { selector: "span" })).not.toBeInTheDocument();
    // Mode section still present.
    expect(screen.getByText(/Delivery mode/i)).toBeInTheDocument();
  });

  it("clicking 'Switch to local' on a cloud agent PUTs agent_mode=local", async () => {
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
      expect(screen.getByText(/Switch to local/i)).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText(/Switch to local/i));

    await waitFor(() => {
      expect(putBody).not.toBeNull();
    });
    expect(putBody).toBe(JSON.stringify({ agent_mode: "local", webhook_url: "" }));
  });

  it("editing the webhook URL PUTs webhook_url", async () => {
    setSearchParams({ email: baseAgent.email });
    const user = userEvent.setup();
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
      expect(screen.getByText("https://acme.com/hook")).toBeInTheDocument();
    });

    // The Webhook editor has its own "Edit" button (separate from the
    // HITL Edit). Scope by reading the URL display's parent paragraph.
    const webhookP = screen.getByText("https://acme.com/hook").closest("p")!;
    fireEvent.click(webhookP.querySelector("button")!);
    const input = screen.getByDisplayValue("https://acme.com/hook");
    await user.clear(input);
    await user.type(input, "https://acme.com/v2");
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(putBody).not.toBeNull();
    });
    expect(putBody).toBe(JSON.stringify({ webhook_url: "https://acme.com/v2" }));
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
        url === `/api/dashboard/agents/${encodeURIComponent(baseAgent.email)}` &&
        init?.method === "DELETE"
      ) {
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
