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

// --- Signing secrets ---

type FetchInit = { method?: string; body?: BodyInit };
type MockResponse = { ok: boolean; status: number; text: () => Promise<string>; json: () => Promise<unknown> };

function makeFetchMock(
  routes: Record<string, (init?: FetchInit) => MockResponse | Promise<MockResponse>>,
) {
  return jest.fn(async (url: string, init?: FetchInit) => {
    const method = init?.method ?? "GET";
    const key = `${method} ${url}`;
    const handler = routes[key] ?? routes[url];
    if (!handler) {
      throw new Error(`unmocked ${key}`);
    }
    return handler(init);
  });
}

function jsonResp(body: unknown, status = 200): MockResponse {
  const text = JSON.stringify(body);
  return { ok: status >= 200 && status < 300, status, text: async () => text, json: async () => body };
}

function errResp(text: string, status: number): MockResponse {
  return { ok: false, status, text: async () => text, json: async () => ({}) };
}

describe("Settings — Signing secrets section", () => {
  const defaultSecret = {
    id: "wsec_default0001",
    name: "default",
    secret_prefix: "abcd1234efgh",
    created_at: "2026-04-15T10:00:00Z",
  };

  it("lists existing secrets without exposing plaintext", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => {
      expect(screen.getByText("default")).toBeInTheDocument();
    });
    // Prefix is shown with a trailing ellipsis.
    expect(screen.getByText("abcd1234efgh…")).toBeInTheDocument();
    // Critical: nothing in the rendered DOM should contain a 32+ char
    // hex blob (the full plaintext form). The mock never returns one.
    expect(document.body.innerHTML).not.toMatch(/[a-f0-9]{32,}/i);
  });

  it("disables Delete when only one secret exists", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("default")).toBeInTheDocument());
    const delBtn = screen.getByRole("button", { name: /^delete$/i });
    expect(delBtn).toBeDisabled();
  });

  it("creates a new secret and shows the plaintext exactly once, then hides it on dismiss", async () => {
    const created = {
      id: "wsec_new0002",
      name: "rolling",
      secret: "deadbeef".repeat(8), // 64-char "plaintext"
      secret_prefix: "deadbeefdead",
      created_at: "2026-04-27T16:10:00Z",
    };
    let listCallCount = 0;
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () => {
        listCallCount++;
        if (listCallCount === 1) return jsonResp({ secrets: [defaultSecret] });
        return jsonResp({ secrets: [defaultSecret, { ...created, secret: undefined }] });
      },
      "POST /api/v1/users/me/signing-secrets": () => jsonResp(created, 201),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("default")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /create new secret/i }));
    fireEvent.change(screen.getByPlaceholderText(/rolling-2026/i), { target: { value: "rolling" } });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    // The plaintext is now in a readonly input with the warning banner.
    await waitFor(() => {
      expect(screen.getByText(/Save this secret now/i)).toBeInTheDocument();
    });
    const plaintextInput = screen.getByLabelText(/plaintext signing secret/i) as HTMLInputElement;
    expect(plaintextInput.value).toBe(created.secret);

    // After dismiss, the plaintext disappears from the rendered DOM.
    fireEvent.click(screen.getByRole("button", { name: /^dismiss$/i }));
    expect(screen.queryByText(/save this secret now/i)).not.toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain(created.secret);
  });

  it("shows an inline error when the cap is reached on create", async () => {
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret] }),
      "POST /api/v1/users/me/signing-secrets": () =>
        errResp("at most 5 signing secrets per user; delete one before creating another", 400),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("default")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /create new secret/i }));
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      expect(screen.getByText(/at most 5 signing secrets/i)).toBeInTheDocument();
    });
  });

  it("DELETEs the right id when confirming a row delete", async () => {
    const second = { ...defaultSecret, id: "wsec_other", name: "other", secret_prefix: "1111" };
    const deletedIDs: string[] = [];
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret, second] }),
      [`DELETE /api/v1/users/me/signing-secrets/${encodeURIComponent("wsec_other")}`]: () => {
        deletedIDs.push("wsec_other");
        return { ok: true, status: 204, text: async () => "", json: async () => ({}) };
      },
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("other")).toBeInTheDocument());

    // Two rows now → both Delete buttons should be enabled.
    const delButtons = screen.getAllByRole("button", { name: /^delete$/i });
    expect(delButtons).toHaveLength(2);
    fireEvent.click(delButtons[1]);
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    await waitFor(() => {
      expect(deletedIDs).toEqual(["wsec_other"]);
    });
  });

  it("surfaces a 'cannot delete the last' error inline", async () => {
    const second = { ...defaultSecret, id: "wsec_other", name: "other", secret_prefix: "1111" };
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret, second] }),
      [`DELETE /api/v1/users/me/signing-secrets/${encodeURIComponent("wsec_other")}`]: () =>
        errResp("cannot delete the last signing secret; create a new one first", 400),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("other")).toBeInTheDocument());

    const delButtons = screen.getAllByRole("button", { name: /^delete$/i });
    fireEvent.click(delButtons[1]);
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    await waitFor(() => {
      expect(screen.getByText(/cannot delete the last/i)).toBeInTheDocument();
    });
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
