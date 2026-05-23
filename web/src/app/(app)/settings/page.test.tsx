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
  // The Settings → Usage section fetches /api/dashboard/stats on mount.
  // Tests that don't care about Usage just stub fetch with a stats
  // response so the section can settle into its 0-state instead of
  // throwing an unhandled rejection.
  global.fetch = jest.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      today: { inbound: 0, outbound: 0, inbound_delta_pct: 0, outbound_delta_pct: 0 },
      pending: { count: 0, oldest_seconds: 0 },
      delivery_success_pct: 0,
      sample_window_days: 30,
      inbound_window: 0,
      outbound_window: 0,
    }),
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
  // The list endpoint now returns the full plaintext `secret` so the
  // dashboard can offer a Show/Hide toggle. Fixtures include both
  // prefix and full secret for that reason.
  const defaultSecret = {
    id: "wsec_default0001",
    name: "default",
    secret: "abcd1234efgh".repeat(6).slice(0, 64),
    secret_prefix: "abcd1234efgh",
    created_at: "2026-04-15T10:00:00Z",
  };

  it("lists existing secrets with the prefix hidden by default", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => {
      expect(screen.getByText("default")).toBeInTheDocument();
    });
    // Prefix is shown with a trailing ellipsis until the user clicks Show.
    expect(screen.getByText("abcd1234efgh…")).toBeInTheDocument();
    expect(screen.queryByText(defaultSecret.secret)).not.toBeInTheDocument();
  });

  it("toggles the full secret on Show / Hide", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("default")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /^show$/i }));
    expect(screen.getByText(defaultSecret.secret)).toBeInTheDocument();
    expect(screen.queryByText("abcd1234efgh…")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^hide$/i }));
    expect(screen.queryByText(defaultSecret.secret)).not.toBeInTheDocument();
    expect(screen.getByText("abcd1234efgh…")).toBeInTheDocument();
  });

  it("copies the full plaintext to the clipboard and flips the Copy label", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () => jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("default")).toBeInTheDocument());

    // Copy is only rendered once the row is revealed — that's the
    // point of the feature, you can't copy what you haven't disclosed.
    expect(screen.queryByRole("button", { name: /^copy$/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^show$/i }));

    fireEvent.click(screen.getByRole("button", { name: /^copy$/i }));
    expect(writeText).toHaveBeenCalledWith(defaultSecret.secret);
    // The handler is async (await writeText) so the "Copied" flip is
    // applied on the next microtask — waitFor lets jsdom flush it.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /^copied$/i })).toBeInTheDocument();
    });
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

  it("creates a new secret, shows the plaintext in the banner, and hides the banner on dismiss", async () => {
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
        // Second list reflects the row added by POST. The row carries
        // `secret` because it's the reveal source for Show — we just
        // start collapsed so the plaintext isn't in the rendered DOM.
        return jsonResp({ secrets: [defaultSecret, created] });
      },
      "POST /api/v1/users/me/signing-secrets": () => jsonResp(created, 201),
    }) as unknown as typeof fetch;

    render(<SettingsPage />);
    await waitFor(() => expect(screen.getByText("default")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /create new secret/i }));
    fireEvent.change(screen.getByPlaceholderText(/rolling-2026/i), { target: { value: "rolling" } });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    // The plaintext appears in a readonly input inside the new-secret banner.
    await waitFor(() => {
      expect(screen.getByText(/new signing secret created/i)).toBeInTheDocument();
    });
    const plaintextInput = screen.getByLabelText(/plaintext signing secret/i) as HTMLInputElement;
    expect(plaintextInput.value).toBe(created.secret);

    // After dismiss the banner is gone. The plaintext still lives in
    // the table row state (so Show works) but is not in the rendered
    // DOM because the row starts collapsed.
    fireEvent.click(screen.getByRole("button", { name: /^dismiss$/i }));
    expect(screen.queryByText(/new signing secret created/i)).not.toBeInTheDocument();
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
    const second = { ...defaultSecret, id: "wsec_other", name: "other", secret: "1111".repeat(16), secret_prefix: "1111" };
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
    const second = { ...defaultSecret, id: "wsec_other", name: "other", secret: "1111".repeat(16), secret_prefix: "1111" };
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
