import { render, screen, waitFor, fireEvent } from "../../../test-utils/swr";
import userEvent from "@testing-library/user-event";
import DomainsPage from "./page";

// Mock next/link
jest.mock("next/link", () => {
  return function MockLink({ href, children, ...props }: { href: string; children: React.ReactNode; [key: string]: unknown }) {
    return <a href={href} {...props}>{children}</a>;
  };
});

// Mock fetch
const mockFetch = jest.fn();
global.fetch = mockFetch;

function mockDomainsAndAgents(
  domains: unknown[] = [],
  agents: unknown[] = [],
) {
  mockFetch.mockImplementation((url: string) => {
    if (url === "/v1/domains") {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ items: domains }),
      });
    }
    if (url === "/v1/agents") {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ items: agents }),
      });
    }
    return Promise.resolve({ ok: false, text: () => Promise.resolve("not found") });
  });
}

// Unverified domain, sending feature off: only the two inbound records
// (ownership + inbound_mx), both pending. dns_records is the unified,
// purpose-tagged array (no more mx/txt/dkim object).
const sampleDomain = {
  domain: "mail.example.com",
  verified: false,
  verification_token: "e2a-verify=abc123",
  dns_records: [
    {
      type: "TXT",
      name: "mail.example.com",
      value: "e2a-verify=abc123",
      priority: null,
      purpose: "ownership",
      status: "pending",
    },
    {
      type: "MX",
      name: "mail.example.com",
      value: "mx.e2a.dev",
      priority: 10,
      purpose: "inbound_mx",
      status: "pending",
    },
  ],
  created_at: "2026-01-01T00:00:00Z",
  verified_at: null,
};

// Verified domain, sending feature still off: inbound records now verified.
const verifiedDomain = {
  ...sampleDomain,
  domain: "verified.example.com",
  verified: true,
  verified_at: "2026-01-15T00:00:00Z",
  dns_records: [
    { ...sampleDomain.dns_records[0], status: "verified" },
    { ...sampleDomain.dns_records[1], status: "verified" },
  ],
};

// A verified domain with the sending feature ON: the unified array now also
// carries the DKIM record and the 2 deterministic custom MAIL FROM records
// (purpose mail_from_mx / mail_from_spf). The MX value is the bare host — its
// priority lives in the priority field, never embedded in the value.
const sendingDomain = {
  ...verifiedDomain,
  domain: "sending.example.com",
  sending_status: "pending",
  dns_records: [
    { ...verifiedDomain.dns_records[0] },
    { ...verifiedDomain.dns_records[1] },
    {
      type: "TXT",
      name: "e2a202606._domainkey.sending.example.com",
      value: "v=DKIM1; k=rsa; p=PUBKEY",
      priority: null,
      purpose: "dkim",
      status: "pending",
    },
    {
      type: "MX",
      name: "bounce.sending.example.com",
      value: "feedback-smtp.us-east-1.amazonses.com",
      priority: 10,
      purpose: "mail_from_mx",
      status: "pending",
    },
    {
      type: "TXT",
      name: "bounce.sending.example.com",
      value: "v=spf1 include:amazonses.com ~all",
      priority: null,
      purpose: "mail_from_spf",
      status: "pending",
    },
  ],
};

// Flips every sending record's per-record status (dkim + mail_from_*) to mirror
// a new domain-level sending_status — the backend derives these together.
function withSendingStatus(status: string) {
  return {
    ...sendingDomain,
    sending_status: status,
    dns_records: sendingDomain.dns_records.map((r) =>
      r.purpose === "dkim" ||
      r.purpose === "mail_from_mx" ||
      r.purpose === "mail_from_spf"
        ? { ...r, status }
        : r,
    ),
  };
}

const sampleAgent = {
  id: "ag_123",
  domain: "verified.example.com",
  email: "support@verified.example.com",
  name: "support",
  webhook_url: "https://example.com/webhook",
  agent_mode: "cloud",
  domain_verified: true,
  public: false,
  created_at: "2026-01-20T00:00:00Z",
};

beforeEach(() => {
  mockFetch.mockReset();
});

describe("Domains page — empty state", () => {
  it("shows empty state when no domains exist", async () => {
    mockDomainsAndAgents([], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("You don't have any domains yet.")).toBeInTheDocument();
    });
    expect(screen.getByText("Add domain")).toBeInTheDocument();
    expect(screen.getByText("Use shared e2a domain instead")).toBeInTheDocument();
  });

  it("links to get-started for shared domain path", async () => {
    mockDomainsAndAgents([], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Use shared e2a domain instead")).toBeInTheDocument();
    });
    const link = screen.getByText("Use shared e2a domain instead");
    expect(link).toHaveAttribute("href", "/get-started?mode=shared");
  });

  it("shows add domain form when CTA clicked in empty state", async () => {
    mockDomainsAndAgents([], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Add domain")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText("Add domain"));

    await waitFor(() => {
      expect(screen.getByText("Add a new domain")).toBeInTheDocument();
    });
    expect(screen.getByText("Register domain")).toBeInTheDocument();
  });
});

describe("Domains page — with domains", () => {
  it("renders domain cards", async () => {
    mockDomainsAndAgents([sampleDomain, verifiedDomain], [sampleAgent]);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("mail.example.com")).toBeInTheDocument();
    });
    expect(screen.getByText("verified.example.com")).toBeInTheDocument();
  });

  it("shows verification status badges", async () => {
    mockDomainsAndAgents([sampleDomain, verifiedDomain], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("mail.example.com")).toBeInTheDocument();
    });
    expect(screen.getByText("Unverified")).toBeInTheDocument();
    // "Verified" appears as a chip on the card *and* as a label in the
    // stats strip — both are real, count both.
    expect(screen.getAllByText("Verified").length).toBeGreaterThan(0);
  });

  it("shows agent count derived from agents response", async () => {
    mockDomainsAndAgents([verifiedDomain], [sampleAgent]);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("1 inbox")).toBeInTheDocument();
    });
  });

  it("shows 'No agents' for domains without agents", async () => {
    mockDomainsAndAgents([sampleDomain], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("No inboxes")).toBeInTheDocument();
    });
  });

  it("shows Verify domain button for unverified domains", async () => {
    mockDomainsAndAgents([sampleDomain], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Verify domain")).toBeInTheDocument();
    });
  });

  it("shows Create agent link for verified domains", async () => {
    mockDomainsAndAgents([verifiedDomain], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Create inbox")).toBeInTheDocument();
    });
    expect(screen.getByText("Create inbox")).toHaveAttribute("href", "/get-started?domain=verified.example.com");
  });

  it("toggles DNS records visibility", async () => {
    mockDomainsAndAgents([sampleDomain], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("View DNS records")).toBeInTheDocument();
    });

    // DNS records should not be visible initially
    expect(screen.queryByText("Route email to e2a")).not.toBeInTheDocument();

    await userEvent.click(screen.getByText("View DNS records"));

    expect(screen.getByText("Route email to e2a")).toBeInTheDocument();
    // Per-record DNS row label expanded for SPF context
    expect(
      screen.getByText(/Prove domain ownership/),
    ).toBeInTheDocument();
  });
});

describe("Domains page — unified sending records (by purpose)", () => {
  it("renders the mail_from records by purpose + a 'Verifying…' rollup for a pending identity", async () => {
    mockDomainsAndAgents([sendingDomain], []);
    render(<DomainsPage />);
    await waitFor(() => {
      expect(screen.getByText("sending.example.com")).toBeInTheDocument();
    });

    // Hidden until the DNS section is expanded.
    expect(screen.queryByText("Outbound sending")).not.toBeInTheDocument();
    await userEvent.click(screen.getByText("View DNS records"));

    expect(screen.getByText("Outbound sending")).toBeInTheDocument();
    // Sending records are verified by SES as a unit → the rollup chip carries
    // status (no redundant per-row chip).
    expect(screen.getByText("Verifying…")).toBeInTheDocument();

    // mail_from_mx — host only; the priority is split into its own field, so
    // the backend's combined "10 feedback-smtp…" string is never rendered.
    expect(
      screen.getByText("feedback-smtp.us-east-1.amazonses.com"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("10 feedback-smtp.us-east-1.amazonses.com"),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("Return path for bounces (MAIL FROM)"),
    ).toBeInTheDocument();

    // mail_from_spf — rendered by its purpose label + value.
    expect(
      screen.getByText("v=spf1 include:amazonses.com ~all"),
    ).toBeInTheDocument();
    expect(screen.getByText("Authorize sending (SPF)")).toBeInTheDocument();

    // dkim (an inbound-group record) renders in the main list with its own
    // per-record chip — pending here, tracking sending_status.
    expect(
      screen.getByText("Authenticate outbound mail (DKIM)"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Pending").length).toBeGreaterThan(0);
  });

  it("shows the 'Sending enabled' rollup when the sending identity is verified", async () => {
    mockDomainsAndAgents([withSendingStatus("verified")], []);
    render(<DomainsPage />);
    await waitFor(() => {
      expect(screen.getByText("sending.example.com")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("View DNS records"));
    expect(screen.getByText("Sending enabled")).toBeInTheDocument();
  });

  it("surfaces sending_error when verification failed", async () => {
    mockDomainsAndAgents(
      [
        {
          ...withSendingStatus("failed"),
          sending_error: "MAIL FROM MX not found",
        },
      ],
      [],
    );
    render(<DomainsPage />);
    await waitFor(() => {
      expect(screen.getByText("sending.example.com")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("View DNS records"));
    // Rollup chip + dkim per-record chip both read "Failed".
    expect(screen.getAllByText("Failed").length).toBeGreaterThan(0);
    expect(screen.getByText("MAIL FROM MX not found")).toBeInTheDocument();
  });

  it("degrades to the neutral 'Verifying…' rollup for an unknown sending_status", async () => {
    // Open set — a future status the UI doesn't know must not read "Not set up"
    // next to a populated record list.
    mockDomainsAndAgents([{ ...sendingDomain, sending_status: "provisioning" }], []);
    render(<DomainsPage />);
    await waitFor(() => {
      expect(screen.getByText("sending.example.com")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("View DNS records"));
    expect(screen.getByText("Outbound sending")).toBeInTheDocument();
    expect(screen.getByText("Verifying…")).toBeInTheDocument();
  });

  it("renders no sending section when the feature is off (no mail_from records)", async () => {
    mockDomainsAndAgents([verifiedDomain], []);
    render(<DomainsPage />);
    await waitFor(() => {
      expect(screen.getByText("verified.example.com")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByText("View DNS records"));
    expect(screen.queryByText("Outbound sending")).not.toBeInTheDocument();
    // The inbound records still render, by purpose.
    expect(screen.getByText("Route email to e2a")).toBeInTheDocument();
    expect(screen.getByText(/Prove domain ownership/)).toBeInTheDocument();
  });
});

describe("Domains page — add domain form", () => {
  it("toggles add domain form", async () => {
    mockDomainsAndAgents([sampleDomain], []);
    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Add domain")).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText("Add domain"));
    expect(screen.getByPlaceholderText("mail.yourcompany.com")).toBeInTheDocument();

    await userEvent.click(screen.getByText("Cancel"));
    expect(screen.queryByPlaceholderText("mail.yourcompany.com")).not.toBeInTheDocument();
  });
});

describe("Domains page — error handling", () => {
  it("shows error when domains fetch fails", async () => {
    mockFetch.mockImplementation((url: string) => {
      if (url === "/v1/domains") {
        return Promise.resolve({ ok: false, text: () => Promise.resolve("Server error") });
      }
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({ items: [] }),
      });
    });

    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Server error")).toBeInTheDocument();
    });
  });
});

describe("Domains page — verify domain", () => {
  it("calls verify endpoint and refreshes on success", async () => {
    // First load: unverified domain
    let callCount = 0;
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === `/v1/domains/${encodeURIComponent("mail.example.com")}/verify` && init?.method === "POST") {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ domain: "mail.example.com", verified: true }),
        });
      }
      if (url === "/v1/domains") {
        callCount++;
        // After verify, return verified domain
        const domains = callCount > 1
          ? [{ ...sampleDomain, verified: true, verified_at: "2026-01-15T00:00:00Z" }]
          : [sampleDomain];
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ items: domains }),
        });
      }
      if (url === "/v1/agents") {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ items: [] }),
        });
      }
      return Promise.resolve({ ok: false, text: () => Promise.resolve("not found") });
    });

    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Verify domain")).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText("Verify domain"));

    await waitFor(() => {
      // After verify succeeds, the chip on the card becomes "Verified".
      expect(screen.getByText("Verified")).toBeInTheDocument();
    });
  });

  it("shows error message on verify failure", async () => {
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url.includes("/verify") && init?.method === "POST") {
        return Promise.resolve({
          ok: false,
          status: 400,
          text: () => Promise.resolve("DNS records not found"),
        });
      }
      if (url === "/v1/domains") {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ items: [sampleDomain] }),
        });
      }
      if (url === "/v1/agents") {
        return Promise.resolve({
          ok: true,
          json: () => Promise.resolve({ items: [] }),
        });
      }
      return Promise.resolve({ ok: false, text: () => Promise.resolve("not found") });
    });

    render(<DomainsPage />);

    await waitFor(() => {
      expect(screen.getByText("Verify domain")).toBeInTheDocument();
    });

    await userEvent.click(screen.getByText("Verify domain"));

    await waitFor(() => {
      expect(screen.getByText("DNS records not found")).toBeInTheDocument();
    });
  });
});

// "Make primary" was removed — is_primary had no behavior behind it.
