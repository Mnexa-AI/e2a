import { render, screen, waitFor, fireEvent, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import DashboardPage from "./page";

// ── Mocks ────────────────────────────────────────────────

jest.mock("../../components/AuthProvider", () => ({
  useAuth: () => ({ user: { email: "user@example.com" } }),
}));

jest.mock("next/link", () => {
  return function MockLink({ href, children, ...props }: { href: string; children: React.ReactNode; [key: string]: unknown }) {
    return <a href={href} {...props}>{children}</a>;
  };
});

const mockFetch = jest.fn();
global.fetch = mockFetch;

Object.assign(navigator, {
  clipboard: { writeText: jest.fn() },
});

// ── Fixtures ─────────────────────────────────────────────

const hitlDefaults = {
  hitl_enabled: false,
  hitl_ttl_seconds: 604800,
  hitl_expiration_action: "reject" as const,
};

const localAgent = {
  id: "ag_local",
  domain: "agents.e2a.dev",
  email: "bot@agents.e2a.dev",
  name: "bot",
  webhook_url: "",
  agent_mode: "local",
  domain_verified: true,
  public: false,
  created_at: "2026-01-01T00:00:00Z",
  ...hitlDefaults,
};

const cloudAgent = {
  id: "ag_cloud",
  domain: "mail.acme.com",
  email: "support@mail.acme.com",
  name: "support",
  webhook_url: "https://acme.com/webhook",
  agent_mode: "cloud",
  domain_verified: true,
  public: false,
  created_at: "2026-02-15T00:00:00Z",
  ...hitlDefaults,
};

const unverifiedAgent = {
  id: "ag_unv",
  domain: "pending.com",
  email: "info@pending.com",
  name: "info",
  webhook_url: "",
  agent_mode: "local",
  domain_verified: false,
  public: false,
  created_at: "2026-03-01T00:00:00Z",
  ...hitlDefaults,
};

function mockAgentList(agents: typeof localAgent[]) {
  mockFetch.mockImplementation((url: string) => {
    if (url === "/api/dashboard/agents") {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ agents }),
      });
    }
    return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
  });
}

beforeEach(() => {
  mockFetch.mockReset();
  jest.spyOn(window, "confirm").mockReturnValue(true);
});

afterEach(() => {
  jest.restoreAllMocks();
});

// ── Empty state ──────────────────────────────────────────

describe("empty state", () => {
  it("shows onboarding entry points when no agents exist", async () => {
    mockAgentList([]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("No agents yet")).toBeInTheDocument();
    });
    expect(screen.getByText("Create your first agent")).toBeInTheDocument();
    expect(screen.getByText("Set up a domain")).toBeInTheDocument();

    // Check links
    const createLink = screen.getByText("Create your first agent").closest("a");
    expect(createLink).toHaveAttribute("href", "/get-started");

    const domainLink = screen.getByText("Set up a domain").closest("a");
    expect(domainLink).toHaveAttribute("href", "/domains");
  });
});

// ── Local agent card ─────────────────────────────────────

describe("local agent card", () => {
  it("renders mode badge as local and shows connect action", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });

    expect(screen.getByText("Local")).toBeInTheDocument();
    expect(screen.getByText("Shared")).toBeInTheDocument();
    expect(screen.getByText("Verified")).toBeInTheDocument();
    expect(screen.getByText("Connect")).toBeInTheDocument();
    expect(screen.getByText("Switch to cloud")).toBeInTheDocument();
  });

  it("does not show webhook summary for local agent", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });

    expect(screen.queryByText("Webhook:")).not.toBeInTheDocument();
  });

  it("shows connect instructions when Connect is clicked", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Connect")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Connect"));

    await waitFor(() => {
      expect(screen.getByText(/OpenClaw, Claude Code, Gemini CLI/)).toBeInTheDocument();
    });
  });
});

// ── Cloud agent card ─────────────────────────────────────

describe("cloud agent card", () => {
  it("renders mode badge as cloud and shows webhook summary", async () => {
    mockAgentList([cloudAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("support@mail.acme.com")).toBeInTheDocument();
    });

    expect(screen.getByText("Cloud")).toBeInTheDocument();
    expect(screen.getByText("Custom")).toBeInTheDocument();
    expect(screen.getByText("https://acme.com/webhook")).toBeInTheDocument();
    expect(screen.getByText("Connect")).toBeInTheDocument();
    expect(screen.getByText("Switch to local")).toBeInTheDocument();
  });

  it("shows connect instructions with webhook guidance when Connect is clicked", async () => {
    mockAgentList([cloudAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Connect")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Connect"));

    await waitFor(() => {
      expect(screen.getByText(/Install the e2a skill to set up your webhook endpoint/)).toBeInTheDocument();
    });
  });
});

// ── Switch local -> cloud ────────────────────────────────

describe("switch local to cloud", () => {
  it("opens webhook form and submits correct payload", async () => {
    const user = userEvent.setup();

    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [localAgent] }),
        });
      }
      if (url === `/api/dashboard/agents/${encodeURIComponent(localAgent.email)}` && init?.method === "PUT") {
        return Promise.resolve({ ok: true, status: 204 });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Switch to cloud")).toBeInTheDocument();
    });

    // First click opens the form
    fireEvent.click(screen.getByText("Switch to cloud"));

    await waitFor(() => {
      expect(screen.getByPlaceholderText("https://example.com/webhook")).toBeInTheDocument();
    });

    // Type webhook URL
    await user.type(screen.getByPlaceholderText("https://example.com/webhook"), "https://myapp.com/hook");

    // Click submit button (there are now two "Switch to cloud" — the one inside the form)
    const switchButtons = screen.getAllByText("Switch to cloud");
    fireEvent.click(switchButtons[switchButtons.length - 1]);

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        `/api/dashboard/agents/${encodeURIComponent(localAgent.email)}`,
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ agent_mode: "cloud", webhook_url: "https://myapp.com/hook" }),
        }),
      );
    });
  });

  it("shows error when webhook URL is missing", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Switch to cloud")).toBeInTheDocument();
    });

    // Open form
    fireEvent.click(screen.getByText("Switch to cloud"));

    await waitFor(() => {
      expect(screen.getByPlaceholderText("https://example.com/webhook")).toBeInTheDocument();
    });

    // Click submit without entering URL
    const switchButtons = screen.getAllByText("Switch to cloud");
    fireEvent.click(switchButtons[switchButtons.length - 1]);

    await waitFor(() => {
      expect(screen.getByText("Enter a valid HTTPS URL")).toBeInTheDocument();
    });
  });
});

// ── Switch cloud -> local ────────────────────────────────

describe("switch cloud to local", () => {
  it("submits correct payload", async () => {
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [cloudAgent] }),
        });
      }
      if (url === `/api/dashboard/agents/${encodeURIComponent(cloudAgent.email)}` && init?.method === "PUT") {
        return Promise.resolve({ ok: true, status: 204 });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Switch to local")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Switch to local"));

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        `/api/dashboard/agents/${encodeURIComponent(cloudAgent.email)}`,
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ agent_mode: "local", webhook_url: "" }),
        }),
      );
    });
  });
});

// ── Edit webhook ─────────────────────────────────────────

describe("edit webhook", () => {
  it("cloud agent webhook can be edited", async () => {
    const user = userEvent.setup();

    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [cloudAgent] }),
        });
      }
      if (url === `/api/dashboard/agents/${encodeURIComponent(cloudAgent.email)}` && init?.method === "PUT") {
        return Promise.resolve({ ok: true, status: 204 });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<DashboardPage />);

    // Two "Edit" buttons on the page now: WebhookEditor + HITLEditor.
    // Scope the click to the one inside the Webhook line.
    await waitFor(() => {
      expect(screen.getByText("Webhook:")).toBeInTheDocument();
    });

    const webhookEditBtn = within(screen.getByText("Webhook:").closest("p")!)
      .getByText("Edit");
    fireEvent.click(webhookEditBtn);

    // Input should be pre-filled with current URL
    const input = screen.getByDisplayValue("https://acme.com/webhook");
    expect(input).toBeInTheDocument();

    // Clear and type new URL
    await user.clear(input);
    await user.type(input, "https://acme.com/v2/webhook");

    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        `/api/dashboard/agents/${encodeURIComponent(cloudAgent.email)}`,
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ webhook_url: "https://acme.com/v2/webhook" }),
        }),
      );
    });
  });

  it("shows inline error on webhook update failure", async () => {
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: [cloudAgent] }),
        });
      }
      if (init?.method === "PUT") {
        return Promise.resolve({ ok: false, status: 400, text: () => Promise.resolve("Invalid webhook URL") });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Webhook:")).toBeInTheDocument();
    });

    const webhookEditBtn = within(screen.getByText("Webhook:").closest("p")!)
      .getByText("Edit");
    fireEvent.click(webhookEditBtn);
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => {
      expect(screen.getByText("Invalid webhook URL")).toBeInTheDocument();
    });
  });
});

// ── Delete ───────────────────────────────────────────────

describe("delete agent", () => {
  it("handles a successful empty-body delete response and refreshes", async () => {
    let callCount = 0;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/dashboard/agents" && !init?.method) {
        callCount++;
        // Return agent on first call, empty on second (after delete)
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({ agents: callCount <= 1 ? [localAgent] : [] }),
        });
      }
      if (url === `/api/dashboard/agents/${encodeURIComponent(localAgent.email)}` && init?.method === "DELETE") {
        return Promise.resolve({
          ok: true,
          status: 200,
          text: () => Promise.resolve(""),
        });
      }
      return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve("not found") });
    });

    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });

    // Delete moved behind an overflow ⋯ menu — open it first, then click
    // the menuitem.
    fireEvent.click(screen.getByRole("button", { name: /more actions/i }));
    fireEvent.click(screen.getByRole("menuitem", { name: /delete agent/i }));

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith(
        `/api/dashboard/agents/${encodeURIComponent(localAgent.email)}`,
        expect.objectContaining({ method: "DELETE" }),
      );
    });

    // After delete, page refreshes and shows empty state
    await waitFor(() => {
      expect(screen.getByText("No agents yet")).toBeInTheDocument();
    });

    expect(screen.queryByText("Unexpected end of JSON input")).not.toBeInTheDocument();
  });
});

// ── Unverified agent ─────────────────────────────────────

describe("unverified agent", () => {
  it("shows Unverified badge and no connect action", async () => {
    mockAgentList([unverifiedAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("info@pending.com")).toBeInTheDocument();
    });

    expect(screen.getByText("Unverified")).toBeInTheDocument();
    expect(screen.queryByText("Connect")).not.toBeInTheDocument();
  });
});
