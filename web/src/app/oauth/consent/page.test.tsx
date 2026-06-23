/**
 * Consent page covers four meaningful states:
 *   1. Missing required OAuth params  → friendly error
 *   2. Not signed in                   → sign-in CTA with return_to
 *   3. Client lookup 404               → "unknown client" message
 *   4. Happy path                      → render form, slug-edit + submit
 *
 * The form submission itself is a plain HTML POST (so the browser
 * follows the 303 to the client's off-origin redirect_uri). We assert
 * the form's `method`, `action`, and hidden-input shape instead of
 * driving a real POST.
 */

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ConsentPage from "./page";

// ── Mocks ────────────────────────────────────────────────

// useSearchParams is mocked per test via __setSearchParams.
let searchParamsValue = new URLSearchParams();
jest.mock("next/navigation", () => ({
  useSearchParams: () => searchParamsValue,
}));
function __setSearchParams(qs: string) {
  searchParamsValue = new URLSearchParams(qs);
}

// useAuth is mocked per test via __setAuth.
let authValue: { user: { id: string; email: string; name?: string; created_at?: string } | null; loading: boolean } = {
  user: { id: "u_1", email: "user@example.com", name: "User", created_at: "" },
  loading: false,
};
jest.mock("../../components/AuthProvider", () => ({
  useAuth: () => authValue,
}));
function __setAuth(v: typeof authValue) {
  authValue = v;
}

// SignInLink is a thin wrapper around an <a>; render a stub so the
// test doesn't pull in the in-app-browser detection branch.
jest.mock("../../components/SignInLink", () => ({
  SignInLink: ({ children, className }: { children: React.ReactNode; className?: string }) => (
    <a href="/api/auth/login" className={className}>
      {children}
    </a>
  ),
}));

// fetch is mocked per test for client lookup + agents list.
const mockFetch = jest.fn();
global.fetch = mockFetch as unknown as typeof fetch;

// Valid params bundle reused across tests. Anything that needs to
// behave like a real /authorize bounce should derive from this.
const VALID_QS =
  "response_type=code" +
  "&client_id=mcp_abc123" +
  "&redirect_uri=http%3A%2F%2Flocalhost%3A8765%2Fcb" +
  "&code_challenge=test_challenge_value" +
  "&code_challenge_method=S256" +
  "&state=opaque-state-xyz" +
  "&scope=agent";

function mockClientAndAgents(opts: {
  clientStatus?: number;
  clientBody?: object;
  agents?: object[];
}) {
  const clientStatus = opts.clientStatus ?? 200;
  const clientBody = opts.clientBody ?? {
    client_id: "mcp_abc123",
    client_name: "Test MCP Client",
    redirect_uris: ["http://localhost:8765/cb"],
    scopes: ["agent"],
    client_id_issued_at: 1700000000,
  };
  const agents = opts.agents ?? [];
  mockFetch.mockImplementation((url: string) => {
    if (url.startsWith("/oauth2/clients/")) {
      return Promise.resolve({
        ok: clientStatus === 200,
        status: clientStatus,
        json: () => Promise.resolve(clientBody),
      });
    }
    if (url === "/v1/agents") {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ items: agents }),
      });
    }
    return Promise.resolve({ ok: false, status: 404 });
  });
}

beforeEach(() => {
  mockFetch.mockReset();
  // Default: any fetch that a test doesn't explicitly set up returns
  // an empty 404. Stops effects from blowing up with "undefined.then"
  // in branches where the fetched data is irrelevant to the assertion
  // (e.g. the "missing params" branch still fires the agents effect
  // before the render short-circuits).
  mockFetch.mockImplementation(() =>
    Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({}) }),
  );
  __setAuth({
    user: { id: "u_1", email: "user@example.com", name: "User", created_at: "" },
    loading: false,
  });
});

// ── Tests ────────────────────────────────────────────────

describe("ConsentPage", () => {
  test("renders 'invalid authorization request' when required params are missing", () => {
    __setSearchParams("client_id=mcp_abc123");
    render(<ConsentPage />);
    expect(screen.getByText(/Invalid authorization request/i)).toBeInTheDocument();
    // All five required params should be listed except client_id (provided).
    expect(screen.getByText(/response_type/)).toBeInTheDocument();
    expect(screen.getByText(/redirect_uri/)).toBeInTheDocument();
    expect(screen.getByText(/code_challenge\b/)).toBeInTheDocument();
    expect(screen.getByText(/code_challenge_method/)).toBeInTheDocument();
    // Don't fetch anything in this branch — the prompt is purely informational.
    expect(mockFetch).not.toHaveBeenCalled();
  });

  test("shows sign-in CTA carrying return_to when no session", () => {
    __setSearchParams(VALID_QS);
    __setAuth({ user: null, loading: false });

    render(<ConsentPage />);
    expect(screen.getByText(/Sign in to continue/i)).toBeInTheDocument();
    const linkWithReturnTo = screen.getByRole("link", {
      name: /Sign in and return to this authorization/i,
    }) as HTMLAnchorElement;
    expect(linkWithReturnTo.getAttribute("href")).toMatch(
      /^\/api\/auth\/login\?return_to=/,
    );
    // return_to must preserve every original OAuth param.
    const u = new URL(linkWithReturnTo.href, "http://localhost");
    const ret = u.searchParams.get("return_to") ?? "";
    expect(ret).toContain("client_id=mcp_abc123");
    expect(ret).toContain("code_challenge=test_challenge_value");
    expect(ret).toContain("response_type=code");
  });

  test("renders 'unknown client' when the lookup 404s", async () => {
    __setSearchParams(VALID_QS);
    mockClientAndAgents({ clientStatus: 404 });

    render(<ConsentPage />);
    await waitFor(() => {
      expect(screen.getByText(/Unknown client/i)).toBeInTheDocument();
    });
    expect(
      screen.getByText(/This client is not registered with e2a/i),
    ).toBeInTheDocument();
  });

  test("happy path: form contains hidden inputs for every OAuth param, create-new is preselected when user has no agents, and submit is allowed", async () => {
    __setSearchParams(VALID_QS);
    mockClientAndAgents({ agents: [] });

    const { container } = render(<ConsentPage />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Authorize Test MCP Client/i }),
      ).toBeInTheDocument();
    });

    const form = container.querySelector("form") as HTMLFormElement;
    expect(form).toBeTruthy();
    expect(form.method.toLowerCase()).toBe("post");
    expect(form.getAttribute("action")).toBe("/oauth2/consent");

    // Every OAuth param ends up as a hidden input.
    const hiddenByName = new Map<string, string>();
    form.querySelectorAll('input[type="hidden"]').forEach((el) => {
      const i = el as HTMLInputElement;
      hiddenByName.set(i.name, i.value);
    });
    expect(hiddenByName.get("response_type")).toBe("code");
    expect(hiddenByName.get("client_id")).toBe("mcp_abc123");
    expect(hiddenByName.get("redirect_uri")).toBe("http://localhost:8765/cb");
    expect(hiddenByName.get("code_challenge")).toBe("test_challenge_value");
    expect(hiddenByName.get("code_challenge_method")).toBe("S256");
    expect(hiddenByName.get("state")).toBe("opaque-state-xyz");
    expect(hiddenByName.get("scope")).toBe("agent");

    // With no existing agents, create_new is the default.
    const createRadio = screen.getByRole("radio", { name: /Create a new inbox/i }) as HTMLInputElement;
    expect(createRadio.checked).toBe(true);

    // Slug input is visible and accepts the default; Allow not disabled.
    const slugInput = screen.getByRole("textbox", { name: /New inbox slug/i }) as HTMLInputElement;
    expect(slugInput.value).toMatch(/^test-mcp-client-[0-9a-f]{6}$/);
    const allow = screen.getByRole("button", { name: /Allow/i }) as HTMLButtonElement;
    expect(allow.disabled).toBe(false);
  });

  test("Allow is disabled when the slug fails the client-side regex", async () => {
    __setSearchParams(VALID_QS);
    mockClientAndAgents({ agents: [] });

    render(<ConsentPage />);
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Authorize Test MCP Client/i }),
      ).toBeInTheDocument();
    });

    const slugInput = screen.getByRole("textbox", { name: /New inbox slug/i }) as HTMLInputElement;
    const allow = screen.getByRole("button", { name: /Allow/i }) as HTMLButtonElement;

    // Empty slug → invalid.
    await userEvent.clear(slugInput);
    expect(allow.disabled).toBe(true);
    expect(slugInput.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText(/2–40 lowercase letters/i)).toBeInTheDocument();

    // Leading hyphen → invalid (regex requires alphanumeric start).
    await userEvent.type(slugInput, "-foo");
    expect(allow.disabled).toBe(true);

    // Single-char slug → invalid (backend requires 2-40).
    // Previously the client regex's tail was optional and accepted
    // 1-char slugs, causing the form to submit and the backend to
    // 400 with no inline UI feedback. Regression guard.
    await userEvent.clear(slugInput);
    await userEvent.type(slugInput, "a");
    expect(allow.disabled).toBe(true);

    // Valid 2-char slug → allow re-enabled.
    await userEvent.clear(slugInput);
    await userEvent.type(slugInput, "ab");
    expect(allow.disabled).toBe(false);

    // Valid longer slug → allow stays enabled.
    await userEvent.clear(slugInput);
    await userEvent.type(slugInput, "good-slug-1");
    expect(allow.disabled).toBe(false);
  });

  test("renders existing agents as radios and uses the first as the default", async () => {
    __setSearchParams(VALID_QS);
    mockClientAndAgents({
      agents: [
        { id: "a1", domain: "verified.com", email: "alice@verified.com" },
        { id: "a2", domain: "verified.com", email: "bob@verified.com" },
      ],
    });

    render(<ConsentPage />);
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Authorize Test MCP Client/i }),
      ).toBeInTheDocument();
    });

    const aliceRadio = screen.getByRole("radio", { name: /alice@verified\.com/ }) as HTMLInputElement;
    const bobRadio = screen.getByRole("radio", { name: /bob@verified\.com/ }) as HTMLInputElement;
    const createRadio = screen.getByRole("radio", { name: /Create a new inbox/i }) as HTMLInputElement;
    expect(aliceRadio.checked).toBe(true);
    expect(bobRadio.checked).toBe(false);
    expect(createRadio.checked).toBe(false);

    // Switching to create_new shows the slug field. Switching back hides it.
    await userEvent.click(createRadio);
    expect(screen.getByRole("textbox", { name: /New inbox slug/i })).toBeInTheDocument();
    await userEvent.click(aliceRadio);
    expect(screen.queryByRole("textbox", { name: /New inbox slug/i })).not.toBeInTheDocument();
  });

  test("Deny and Allow are the two submit buttons with matching name=action values", async () => {
    __setSearchParams(VALID_QS);
    mockClientAndAgents({ agents: [] });

    render(<ConsentPage />);
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Authorize Test MCP Client/i }),
      ).toBeInTheDocument();
    });

    const deny = screen.getByRole("button", { name: /Deny/i }) as HTMLButtonElement;
    const allow = screen.getByRole("button", { name: /Allow/i }) as HTMLButtonElement;
    expect(deny.type).toBe("submit");
    expect(deny.name).toBe("action");
    expect(deny.value).toBe("deny");
    expect(allow.type).toBe("submit");
    expect(allow.name).toBe("action");
    expect(allow.value).toBe("allow");
  });

  test("forwards unknown params through hidden inputs (forward-compat with RFC 8707 resource etc)", async () => {
    __setSearchParams(VALID_QS + "&resource=https%3A%2F%2Fapi.e2a.dev");
    mockClientAndAgents({ agents: [] });

    const { container } = render(<ConsentPage />);
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Authorize Test MCP Client/i }),
      ).toBeInTheDocument();
    });
    const form = container.querySelector("form") as HTMLFormElement;
    const resource = form.querySelector('input[name="resource"]') as HTMLInputElement;
    expect(resource).toBeTruthy();
    expect(resource.value).toBe("https://api.e2a.dev");
  });
});
