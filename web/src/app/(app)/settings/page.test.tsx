import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import SettingsPage from "./page";

// jsdom doesn't provide navigator.clipboard. The signing-secret Copy
// button calls writeText, so we install a jest mock once at module
// level so individual tests can assert on it.
const writeText = jest.fn(async () => {});
Object.assign(navigator, { clipboard: { writeText } });
beforeEach(() => writeText.mockClear());

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
  // Generic fetch stub. The Settings page no longer fetches on mount
  // (the Usage section was removed); the delete-account test overrides
  // this with its own deferred mock.
  global.fetch = jest.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => ({}),
    text: async () => "",
  })) as unknown as typeof fetch;
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
    expect(link).toHaveAttribute("href", "/v1/account/export");
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

  it("issues DELETE /v1/account?confirm=DELETE with cookie credentials on confirmation", async () => {
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
        "/v1/account?confirm=DELETE",
        expect.objectContaining({ method: "DELETE", credentials: "include" }),
      );
    });
    // Resolve so the test doesn't leak a pending promise — but don't
    // await the redirect, since that path mutates window.location.
    resolveFetch({ ok: true, status: 200, text: async () => "{}", json: async () => ({ user_deleted: true }) });
  });

  it("surfaces the server's error message inline when the delete is rejected", async () => {
    global.fetch = jest.fn(async () => ({ ok: false, status: 400, text: async () => "nope" })) as unknown as typeof fetch;
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: /delete account/i }));
    fireEvent.change(screen.getByPlaceholderText("DELETE"), { target: { value: "DELETE" } });
    fireEvent.click(screen.getByRole("button", { name: /delete my account/i }));

    // The raw server message is surfaced verbatim (no hardcoded "Failed:"
    // prefix), so a plain-text body shows as-is.
    await waitFor(() => {
      expect(screen.getByText("nope")).toBeInTheDocument();
    });
  });

  it("surfaces the sole-admin block message + actionable guidance", async () => {
    const body = JSON.stringify({
      error: {
        code: "sole_admin_workspace",
        message:
          "cannot delete account: you are the sole admin of a workspace with other members; promote another member to admin first",
      },
    });
    global.fetch = jest.fn(async () => ({
      ok: false,
      status: 409,
      text: async () => body,
    })) as unknown as typeof fetch;
    render(<SettingsPage />);
    fireEvent.click(screen.getByRole("button", { name: /delete account/i }));
    fireEvent.change(screen.getByPlaceholderText("DELETE"), { target: { value: "DELETE" } });
    fireEvent.click(screen.getByRole("button", { name: /delete my account/i }));

    await waitFor(() => {
      expect(screen.getByText(/sole admin of a workspace/i)).toBeInTheDocument();
    });
    // The guidance points the user to the workspace to promote/remove first.
    expect(screen.getByText(/promote another member to admin or remove/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /workspace/i })).toHaveAttribute("href", "/workspace");
  });
});
