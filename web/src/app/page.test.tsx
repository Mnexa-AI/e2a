import { render, screen, fireEvent } from "@testing-library/react";
import Home from "./page";

// Mock next/link to render plain anchor tags
jest.mock("next/link", () => {
  return function MockLink({ href, children, ...props }: { href: string; children: React.ReactNode; [key: string]: unknown }) {
    return <a href={href} {...props}>{children}</a>;
  };
});

// Mock AuthProvider
const mockSignOut = jest.fn();
let mockAuthValue = { user: null as { id: string; email: string; name: string; created_at: string } | null, loading: false, signOut: mockSignOut };

jest.mock("./components/AuthProvider", () => ({
  useAuth: () => mockAuthValue,
}));

beforeEach(() => {
  mockAuthValue = { user: null, loading: false, signOut: mockSignOut };
  mockSignOut.mockReset();
});

describe("Landing page", () => {
  it("renders hero content", () => {
    render(<Home />);
    expect(screen.getByText(/Give your agent an email address/)).toBeInTheDocument();
    expect(screen.getByText(/Anyone can send an email, so your agent should have one/)).toBeInTheDocument();
  });

  it("renders navigation links", () => {
    render(<Home />);
    expect(screen.getAllByText("e2a").length).toBeGreaterThan(0);
    expect(screen.getByText(/^Docs/)).toBeInTheDocument();
  });

  it("shows docs dropdown items on hover", () => {
    render(<Home />);
    const docsButton = screen.getByText(/^Docs/).closest("div")!;
    fireEvent.mouseEnter(docsButton);
    expect(screen.getByText("API Reference")).toBeInTheDocument();
    expect(screen.getAllByText("Python SDK").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("TypeScript SDK").length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("CLI").length).toBeGreaterThanOrEqual(2);
  });

  it("hides docs dropdown on mouse leave", () => {
    render(<Home />);
    const docsButton = screen.getByText(/^Docs/).closest("div")!;
    fireEvent.mouseEnter(docsButton);
    expect(screen.getByText("API Reference")).toBeInTheDocument();
    fireEvent.mouseLeave(docsButton);
    expect(screen.queryByText("API Reference")).not.toBeInTheDocument();
  });

  it("shows the how it works section", () => {
    render(<Home />);
    expect(screen.getByText("Up and running in three steps")).toBeInTheDocument();
    expect(screen.getByText("Register your agent")).toBeInTheDocument();
    expect(screen.getByText("Connect your agent")).toBeInTheDocument();
    expect(screen.getByText("Receive, reply, stay in thread")).toBeInTheDocument();
  });

  it("shows the use cases section", () => {
    render(<Home />);
    expect(screen.getByText("What you can build")).toBeInTheDocument();
    expect(screen.getByText("Support and intake")).toBeInTheDocument();
    expect(screen.getByText("Procurement")).toBeInTheDocument();
  });

  it("shows CTA section with links", () => {
    render(<Home />);
    const getStartedLinks = screen.getAllByText(/Get started free/);
    expect(getStartedLinks.length).toBeGreaterThan(0);
  });
});

describe("Navigation auth state", () => {
  it("shows Sign in link when not authenticated", () => {
    render(<Home />);
    expect(screen.getByText("Sign in")).toBeInTheDocument();
    expect(screen.queryByText("Dashboard")).not.toBeInTheDocument();
    expect(screen.queryByText("API Keys")).not.toBeInTheDocument();
  });

  it("shows loading indicator while checking auth", () => {
    mockAuthValue = { user: null, loading: true, signOut: mockSignOut };
    render(<Home />);
    expect(screen.getByText("...")).toBeInTheDocument();
    expect(screen.queryByText("Sign in")).not.toBeInTheDocument();
  });

  it("shows 'Go to Dashboard' link when authenticated", () => {
    mockAuthValue = {
      user: { id: "u1", email: "dev@example.com", name: "Dev", created_at: "2026-01-01T00:00:00Z" },
      loading: false,
      signOut: mockSignOut,
    };
    render(<Home />);
    expect(screen.getByText("Go to Dashboard")).toBeInTheDocument();
    expect(screen.queryByText("Sign in")).not.toBeInTheDocument();
    expect(screen.queryByText("API Keys")).not.toBeInTheDocument();
    expect(screen.queryByText("Sign out")).not.toBeInTheDocument();
  });

  it("links the GitHub repo from the header", () => {
    render(<Home />);
    const githubLink = screen.getByRole("link", { name: /view source on github/i });
    expect(githubLink).toHaveAttribute("href", "https://github.com/Mnexa-AI/e2a");
  });
});

describe("Footer", () => {
  it("renders footer links", () => {
    render(<Home />);
    expect(screen.getByText("API Docs")).toBeInTheDocument();
    expect(screen.getByText("Claude Skill")).toBeInTheDocument();
    expect(screen.getByText("Feedback")).toBeInTheDocument();
    expect(screen.getByText("Apache 2.0")).toBeInTheDocument();
  });

  it("links the GitHub repo from the footer", () => {
    render(<Home />);
    const githubLink = screen
      .getAllByRole("link")
      .find((l) => l.textContent === "GitHub" && l.getAttribute("href") === "https://github.com/Mnexa-AI/e2a");
    expect(githubLink).toBeInTheDocument();
  });

  it("renders SDK and CLI footer links", () => {
    render(<Home />);
    const footerLinks = screen.getAllByRole("link");
    const pypiLink = footerLinks.find(l => l.textContent === "Python SDK" && l.getAttribute("href")?.includes("pypi.org"));
    const tsLink = footerLinks.find(l => l.textContent === "TypeScript SDK" && l.getAttribute("href")?.includes("npmjs.com"));
    const cliLink = footerLinks.find(l => l.textContent === "CLI" && l.getAttribute("href")?.includes("npmjs.com"));
    expect(pypiLink).toBeInTheDocument();
    expect(tsLink).toBeInTheDocument();
    expect(cliLink).toBeInTheDocument();
  });
});
