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

// GET /v1/agents/{email}/protection response — the review-queue editor
// reads holds.{ttl_seconds,on_expiry} from here (the beta protection API).
const PROTECTION = {
  inbound: { gate: { policy: "open", action: "flag" }, scan: { sensitivity: "off" } },
  outbound: { gate: { policy: "open", action: "flag" }, scan: { sensitivity: "off" } },
  holds: { ttl_seconds: 604800, on_expiry: "reject" },
};

function mockAgent(agent: typeof baseAgent) {
  mockFetch.mockImplementation((url: string, init?: RequestInit) => {
    if (url === "/v1/agents" && !init?.method) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ items: [agent] }),
      });
    }
    if (url === `/v1/agents/${encodeURIComponent(agent.email)}/protection` && !init?.method) {
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(PROTECTION) });
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

  it("renders the protection section and danger zone for a verified agent", async () => {
    setSearchParams({ email: baseAgent.email });
    mockAgent(baseAgent);

    render(<AgentSettingsPage />);

    await waitFor(() => {
      expect(screen.getByTestId("agent-settings")).toBeInTheDocument();
    });

    // Protection (beta) section + its gate/scan/holds controls — gated on
    // the protection fetch, so wait for it to settle.
    await waitFor(() => {
      expect(screen.getByText("Protection")).toBeInTheDocument();
    });
    expect(screen.getByText("Beta")).toBeInTheDocument();
    // Gate + scan + holds controls are all exposed.
    expect(screen.getByText("Who may send to this inbox")).toBeInTheDocument();
    expect(screen.getByText("Who this inbox may send to")).toBeInTheDocument();
    expect(screen.getAllByText("Content scan sensitivity").length).toBe(2);
    expect(screen.getByText("Approval window")).toBeInTheDocument();

    // The retired Mode + per-agent Webhook sections must not render.
    expect(screen.queryByText(/Delivery mode/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Switch to local/i)).not.toBeInTheDocument();
    expect(screen.queryByText("https://acme.com/hook")).not.toBeInTheDocument();

    // Danger zone
    expect(screen.getByTestId("danger-zone")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Delete inbox/i })).toBeInTheDocument();
  });

  it("saving the protection editor PUTs the full config (gates + scan + holds)", async () => {
    setSearchParams({ email: baseAgent.email });
    const protectionUrl = `/v1/agents/${encodeURIComponent(baseAgent.email)}/protection`;
    let putBody: string | null = null;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/v1/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ items: [baseAgent] }),
        });
      }
      if (url === protectionUrl && !init?.method) {
        return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(PROTECTION) });
      }
      if (url === protectionUrl && init?.method === "PUT") {
        putBody = (init.body as string) ?? "";
        return Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve("") });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<AgentSettingsPage />);
    await waitFor(() => {
      expect(screen.getByText("Approval window")).toBeInTheDocument();
    });

    // The form is always editable: pick the "1 hour" window, then save.
    fireEvent.click(screen.getByText("1 hour"));
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(putBody).not.toBeNull();
    });
    // Wholesale PUT: holds reflect the picked window; gates + scan come
    // through from the loaded config (open / flag / off).
    const body = JSON.parse(putBody!);
    expect(body.holds).toEqual({ ttl_seconds: 3600, on_expiry: "reject" });
    expect(body.inbound.gate.policy).toBe("open");
    expect(body.inbound.scan.sensitivity).toBe("off");
    expect(body.outbound.gate.policy).toBe("open");
  });

  it("clicking 'Delete inbox' DELETEs and routes back to /dashboard", async () => {
    setSearchParams({ email: baseAgent.email });
    let deleted = false;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/v1/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ items: [baseAgent] }),
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
      expect(screen.getByRole("button", { name: /Delete inbox/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /Delete inbox/i }));

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
      if (url === "/v1/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ items: [baseAgent] }),
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
      expect(screen.getByRole("button", { name: /Delete inbox/i })).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /Delete inbox/i }));

    // Give any in-flight handler a tick to fire (it shouldn't, but make
    // the negative assertion deterministic).
    await new Promise((r) => setTimeout(r, 10));
    expect(deleted).toBe(false);
    expect(mockRouterPush).not.toHaveBeenCalled();
  });
});
