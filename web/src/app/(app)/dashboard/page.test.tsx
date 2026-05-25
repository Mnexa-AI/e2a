import { render, screen, waitFor, fireEvent } from "../../../test-utils/swr";
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
    // Mode switching used to live inline on the card via the
    // "Switch to cloud" toggle; it now lives on /dashboard/agents/settings.
    expect(screen.queryByText("Switch to cloud")).not.toBeInTheDocument();
  });

  it("does not render the webhook editor or HITL editor inline", async () => {
    // Editors moved to /dashboard/agents/settings. The card stays
    // focused on identity + stats + the two CTAs (Open inbox + Settings).
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });

    expect(screen.queryByText("Webhook:")).not.toBeInTheDocument();
    // HITLEditor's wording — match strings unique to the editor, not
    // the dashboard's "HITL on" filter chip (which is unrelated).
    expect(screen.queryByText(/Require human approval/i)).not.toBeInTheDocument();
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

  // The card's name, email chip, and "Open inbox →" link are the three
  // surfaces that route into the per-agent threaded inbox. They all
  // target the same URL — pinning that here so a future drive-by
  // doesn't re-introduce divergent destinations (the prior
  // ActivityPanel toggle was the case we just collapsed).
  it("name, email chip, and Open inbox CTA all link to the same inbox URL", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });

    const expectedHref = `/dashboard/agents/messages?email=${encodeURIComponent(localAgent.email)}`;
    const openInbox = screen.getByText(/Open inbox/);
    expect(openInbox.closest("a")).toHaveAttribute("href", expectedHref);

    // Email chip
    const emailLink = screen.getByText(localAgent.email).closest("a");
    expect(emailLink).toHaveAttribute("href", expectedHref);

    // Name link (the agent has a `name` field, so it renders as a link too)
    const nameLink = screen.getByText(localAgent.name).closest("a");
    expect(nameLink).toHaveAttribute("href", expectedHref);
  });

  it("does not render the legacy 'Show Activity' toggle", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });
    expect(screen.queryByText(/Show Activity/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Hide Activity/i)).not.toBeInTheDocument();
  });
});

// ── Cloud agent card ─────────────────────────────────────

describe("cloud agent card", () => {
  it("renders mode + custom-domain chips and Connect action", async () => {
    mockAgentList([cloudAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("support@mail.acme.com")).toBeInTheDocument();
    });

    expect(screen.getByText("Cloud")).toBeInTheDocument();
    expect(screen.getByText("Custom")).toBeInTheDocument();
    expect(screen.getByText("Connect")).toBeInTheDocument();
    // Inline editors moved to /dashboard/agents/settings.
    expect(screen.queryByText("Switch to local")).not.toBeInTheDocument();
    expect(screen.queryByText("Webhook:")).not.toBeInTheDocument();
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

// Mode switching, webhook editing, HITL editing, and delete moved
// off the dashboard agent card to /dashboard/agents/settings. Those
// behaviors are exercised in
// web/src/app/(app)/dashboard/agents/settings/page.test.tsx — this
// suite focuses on what the *dashboard* surface still owns:
// identity + chips + Test + Connect + the two CTAs.

// ── Settings CTA ─────────────────────────────────────────

describe("settings CTA", () => {
  it("agent card renders a Settings link to /dashboard/agents/settings?email=...", async () => {
    mockAgentList([localAgent]);
    render(<DashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("bot@agents.e2a.dev")).toBeInTheDocument();
    });

    const settingsLink = screen.getByText("Settings").closest("a");
    expect(settingsLink).toHaveAttribute(
      "href",
      `/dashboard/agents/settings?email=${encodeURIComponent(localAgent.email)}`,
    );
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
