import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import SettingsPage from "./page";

// Mock next/link to plain anchors so we don't need a router in jsdom.
jest.mock("next/link", () => {
  return function MockLink({ href, children, ...rest }: { href: string; children: React.ReactNode; [k: string]: unknown }) {
    return <a href={href} {...rest}>{children}</a>;
  };
});

const signOut = jest.fn();
let mockAuth: {
  user: { id: string; email: string; name: string; created_at: string } | null;
  loading: boolean;
  signOut: jest.Mock;
};

jest.mock("../../components/AuthProvider", () => ({
  useAuth: () => mockAuth,
}));

beforeEach(() => {
  mockAuth = {
    user: {
      id: "usr_abc123",
      email: "alice@example.com",
      name: "Alice",
      created_at: "2026-04-01T10:00:00Z",
    },
    loading: false,
    signOut,
  };
});

describe("Settings — Profile section", () => {
  it("shows the user's name, email, ID, and member-since date", () => {
    render(<SettingsPage />);
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("alice@example.com")).toBeInTheDocument();
    expect(screen.getByText("usr_abc123")).toBeInTheDocument();
    // The exact format depends on locale but the year should always render.
    expect(screen.getByText(/2026/)).toBeInTheDocument();
  });

  it("renders nothing when user is null (defensive)", () => {
    mockAuth.user = null;
    const { container } = render(<SettingsPage />);
    expect(container.firstChild).toBeNull();
  });
});

describe("Settings — Export section", () => {
  it("links the Download export button at the API endpoint", () => {
    render(<SettingsPage />);
    const link = screen.getByRole("link", { name: /download export/i });
    expect(link).toHaveAttribute("href", "/api/v1/users/me/export");
  });
});

describe("Settings — Danger zone (delete account)", () => {
  it("hides the confirm input until the user opens the flow", () => {
    render(<SettingsPage />);
    expect(screen.queryByPlaceholderText("DELETE")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /delete account/i }));
    expect(screen.getByPlaceholderText("DELETE")).toBeInTheDocument();
  });

  it("disables the final delete button until confirmation matches", () => {
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: /delete account/i }));
    const finalBtn = screen.getByRole("button", { name: /delete my account/i });
    expect(finalBtn).toBeDisabled();

    const input = screen.getByPlaceholderText("DELETE") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "delete" } }); // wrong case
    expect(finalBtn).toBeDisabled();

    fireEvent.change(input, { target: { value: "DELETE" } });
    expect(finalBtn).toBeEnabled();
  });

  it("issues DELETE /api/v1/users/me?confirm=DELETE with cookie credentials on confirmation", async () => {
    // Hold the response in a deferred Promise so the assertion runs
    // before the success handler tries to redirect (which would mutate
    // window.location and is hard to mock cleanly in jsdom).
    type FetchLike = { ok: boolean; status: number; text: () => Promise<string>; json: () => Promise<unknown> };
    let resolveFetch: (resp: FetchLike) => void = () => {};
    const fetchPromise = new Promise<FetchLike>((r) => { resolveFetch = r; });
    const fetchMock = jest.fn(() => fetchPromise);
    global.fetch = fetchMock as unknown as typeof fetch;

    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: /delete account/i }));
    fireEvent.change(screen.getByPlaceholderText("DELETE"), { target: { value: "DELETE" } });
    fireEvent.click(screen.getByRole("button", { name: /delete my account/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/users/me?confirm=DELETE",
        expect.objectContaining({ method: "DELETE", credentials: "include" }),
      );
    });
    // Resolve so the test doesn't leak a pending promise — but don't
    // await the redirect, since that path mutates window.location.
    resolveFetch({ ok: true, status: 200, text: async () => "{}", json: async () => ({ user_deleted: true }) });
  });

  it("shows an error message when the server rejects the delete", async () => {
    global.fetch = jest.fn(async () => ({ ok: false, status: 400, text: async () => "nope" })) as unknown as typeof fetch;
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: /delete account/i }));
    fireEvent.change(screen.getByPlaceholderText("DELETE"), { target: { value: "DELETE" } });
    fireEvent.click(screen.getByRole("button", { name: /delete my account/i }));

    // The error paragraph renders as "Failed: nope" in a single <p>.
    // Match by the full string rather than splitting between Failed/nope
    // since text-matching by sub-substring of a single text node only
    // matches once.
    await waitFor(() => {
      expect(screen.getByText(/Failed:\s*nope/)).toBeInTheDocument();
    });
  });
});
