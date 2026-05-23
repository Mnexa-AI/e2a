import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import APIKeysPage from "./page";

// Mock next/link.
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
beforeEach(() => {
  mockFetch.mockReset();
  global.fetch = mockFetch;
});

// stageList mounts the page with a known initial list-keys response.
// Returns the recorded fetch call list so individual tests can assert
// on POST body shapes after a Create.
function stageList(initial: unknown[] = []) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  mockFetch.mockImplementation((url: string, init?: RequestInit) => {
    calls.push({ url, init });
    if (url === "/api/keys" && (!init || !init.method || init.method === "GET")) {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(initial),
      });
    }
    if (url === "/api/keys" && init?.method === "POST") {
      const body = JSON.parse(init.body as string);
      return Promise.resolve({
        ok: true,
        json: () =>
          Promise.resolve({
            id: "apk_new",
            user_id: "usr",
            name: body.name,
            key_prefix: "e2a_abcd",
            key: "e2a_abcd_PLAINTEXT",
            created_at: new Date().toISOString(),
            expires_at: body.expires_at ?? null,
          }),
      });
    }
    return Promise.resolve({
      ok: false,
      text: () => Promise.resolve("not found"),
    });
  });
  return calls;
}

// Helper: extract the body of the most recent POST /api/keys call.
function lastCreateBody(calls: Array<{ url: string; init?: RequestInit }>) {
  const create = [...calls]
    .reverse()
    .find((c) => c.url === "/api/keys" && c.init?.method === "POST");
  return create ? JSON.parse(create.init!.body as string) : null;
}

describe("API keys page — Expires-in select", () => {
  it("omits expires_at on POST when Never is selected (default)", async () => {
    const calls = stageList([]);
    render(<APIKeysPage />);
    await screen.findByText(/No API keys yet/i);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/Key name/i), "ci-token");
    await user.click(screen.getByRole("button", { name: /^create key$/i }));

    await waitFor(() => {
      const body = lastCreateBody(calls);
      expect(body).not.toBeNull();
      expect(body.name).toBe("ci-token");
      // Never selected → field omitted (NOT sent as null/empty string).
      // Backend treats absent field as "never expires".
      expect(body).not.toHaveProperty("expires_at");
    });
  });

  it("sends expires_at as an ISO timestamp 30 days out when 'In 30 days' is selected", async () => {
    const calls = stageList([]);
    render(<APIKeysPage />);
    await screen.findByText(/No API keys yet/i);

    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/Key name/i), "rolling");
    await user.selectOptions(screen.getByLabelText(/Expires/i), "30");
    await user.click(screen.getByRole("button", { name: /^create key$/i }));

    await waitFor(() => {
      const body = lastCreateBody(calls);
      expect(body).not.toBeNull();
      expect(body.expires_at).toBeDefined();
      const expAt = new Date(body.expires_at).getTime();
      const expectedMs = Date.now() + 30 * 24 * 60 * 60 * 1000;
      // Allow ±10s of slack for test execution time.
      expect(Math.abs(expAt - expectedMs)).toBeLessThan(10_000);
    });
  });

  it("sends 90 days when 'In 90 days' is selected", async () => {
    const calls = stageList([]);
    render(<APIKeysPage />);
    await screen.findByText(/No API keys yet/i);

    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText(/Expires/i), "90");
    await user.click(screen.getByRole("button", { name: /^create key$/i }));

    await waitFor(() => {
      const body = lastCreateBody(calls);
      expect(body).not.toBeNull();
      const expAt = new Date(body.expires_at).getTime();
      const expectedMs = Date.now() + 90 * 24 * 60 * 60 * 1000;
      expect(Math.abs(expAt - expectedMs)).toBeLessThan(10_000);
    });
  });

  it("resets the Expires select back to Never after a successful create", async () => {
    stageList([]);
    render(<APIKeysPage />);
    await screen.findByText(/No API keys yet/i);

    const user = userEvent.setup();
    const select = screen.getByLabelText(/Expires/i) as HTMLSelectElement;
    await user.selectOptions(select, "365");
    expect(select.value).toBe("365");
    await user.click(screen.getByRole("button", { name: /^create key$/i }));

    await waitFor(() => {
      expect(select.value).toBe("never");
    });
  });
});

describe("API keys table — Expires column", () => {
  const baseKey = {
    id: "apk_1",
    user_id: "usr_x",
    name: "ci-token",
    key_prefix: "e2a_abcd",
    created_at: "2026-04-01T10:00:00Z",
  };

  it("renders 'Never' for keys with no expires_at", async () => {
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/keys" && (!init || !init.method)) {
        return Promise.resolve({
          ok: true,
          json: async () => [{ ...baseKey, expires_at: null }],
        });
      }
      return Promise.resolve({
        ok: false,
        text: async () => "not found",
      });
    });
    render(<APIKeysPage />);
    await screen.findByText("ci-token");
    // Two cells render "Never": Last used (never used) + Expires (no expiry)
    expect(screen.getAllByText("Never").length).toBeGreaterThanOrEqual(2);
  });

  it("renders 'in Nd' for keys expiring within a month", async () => {
    // 12 days + 1 hour buffer so the floor()-based formatter doesn't
    // round down to "in 11d" because of microseconds of test latency
    // between the timestamp seeding and the cell rendering.
    const future = new Date(
      Date.now() + 12 * 24 * 60 * 60 * 1000 + 60 * 60 * 1000,
    ).toISOString();
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/keys" && (!init || !init.method)) {
        return Promise.resolve({
          ok: true,
          json: async () => [{ ...baseKey, expires_at: future }],
        });
      }
      return Promise.resolve({ ok: false, text: async () => "" });
    });
    render(<APIKeysPage />);
    await screen.findByText("ci-token");
    expect(screen.getByText("in 12d")).toBeInTheDocument();
  });

  it("renders 'expired' for past expires_at", async () => {
    const past = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
    mockFetch.mockImplementation((url: string, init?: RequestInit) => {
      if (url === "/api/keys" && (!init || !init.method)) {
        return Promise.resolve({
          ok: true,
          json: async () => [{ ...baseKey, expires_at: past }],
        });
      }
      return Promise.resolve({ ok: false, text: async () => "" });
    });
    render(<APIKeysPage />);
    await screen.findByText("ci-token");
    expect(screen.getByText("expired")).toBeInTheDocument();
  });
});
