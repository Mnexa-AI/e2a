import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import WebhookSecretsPage from "./page";

// jsdom doesn't provide navigator.clipboard. The signing-secret Copy
// button calls writeText, so we install a jest mock once at module
// level so individual tests can assert on it.
const writeText = jest.fn(async () => {});
Object.assign(navigator, { clipboard: { writeText } });
beforeEach(() => writeText.mockClear());

// Mock next/link → plain anchors (no router in jsdom).
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

// --- Fetch mock helpers (lifted verbatim from the old settings test
//     file — the same helpers covered the signing-secrets section
//     before it moved here). ---

type FetchInit = { method?: string; body?: BodyInit };
type MockResponse = {
  ok: boolean;
  status: number;
  text: () => Promise<string>;
  json: () => Promise<unknown>;
};

function makeFetchMock(
  routes: Record<
    string,
    (init?: FetchInit) => MockResponse | Promise<MockResponse>
  >,
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
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => body,
  };
}

function errResp(text: string, status: number): MockResponse {
  return { ok: false, status, text: async () => text, json: async () => ({}) };
}

describe("Webhook secrets page", () => {
  // The list endpoint returns the full plaintext `secret` so the dashboard
  // can offer a Show/Hide toggle. Fixtures include both prefix and full
  // secret for that reason.
  const defaultSecret = {
    id: "wsec_default0001",
    name: "default",
    secret: "abcd1234efgh".repeat(6).slice(0, 64),
    secret_prefix: "abcd1234efgh",
    created_at: "2026-04-15T10:00:00Z",
  };

  it("lists existing secrets with the prefix hidden by default", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() => {
      expect(screen.getByText("default")).toBeInTheDocument();
    });
    expect(screen.getByText("abcd1234efgh…")).toBeInTheDocument();
    expect(screen.queryByText(defaultSecret.secret)).not.toBeInTheDocument();
  });

  it("toggles the full secret on Show / Hide", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() =>
      expect(screen.getByText("default")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: /^show$/i }));
    expect(screen.getByText(defaultSecret.secret)).toBeInTheDocument();
    expect(screen.queryByText("abcd1234efgh…")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^hide$/i }));
    expect(screen.queryByText(defaultSecret.secret)).not.toBeInTheDocument();
    expect(screen.getByText("abcd1234efgh…")).toBeInTheDocument();
  });

  it("copies the full plaintext to the clipboard and flips the Copy label", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() =>
      expect(screen.getByText("default")).toBeInTheDocument(),
    );

    // Copy is only rendered once the row is revealed — that's the
    // point of the feature, you can't copy what you haven't disclosed.
    expect(
      screen.queryByRole("button", { name: /^copy$/i }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^show$/i }));

    fireEvent.click(screen.getByRole("button", { name: /^copy$/i }));
    expect(writeText).toHaveBeenCalledWith(defaultSecret.secret);
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /^copied$/i }),
      ).toBeInTheDocument();
    });
  });

  it("disables Delete when only one secret exists", async () => {
    global.fetch = makeFetchMock({
      "/api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret] }),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() =>
      expect(screen.getByText("default")).toBeInTheDocument(),
    );
    const delBtn = screen.getByRole("button", { name: /^delete$/i });
    expect(delBtn).toBeDisabled();
  });

  it("creates a new secret, shows the plaintext in the banner, and hides the banner on dismiss", async () => {
    const created = {
      id: "wsec_new0002",
      name: "rolling",
      secret: "deadbeef".repeat(8),
      secret_prefix: "deadbeefdead",
      created_at: "2026-04-27T16:10:00Z",
    };
    let listCallCount = 0;
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () => {
        listCallCount++;
        if (listCallCount === 1) return jsonResp({ secrets: [defaultSecret] });
        return jsonResp({ secrets: [defaultSecret, created] });
      },
      "POST /api/v1/users/me/signing-secrets": () => jsonResp(created, 201),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() =>
      expect(screen.getByText("default")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: /create new secret/i }));
    fireEvent.change(screen.getByPlaceholderText(/rolling-2026/i), {
      target: { value: "rolling" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      expect(
        screen.getByText(/new signing secret created/i),
      ).toBeInTheDocument();
    });
    const plaintextInput = screen.getByLabelText(
      /plaintext signing secret/i,
    ) as HTMLInputElement;
    expect(plaintextInput.value).toBe(created.secret);

    fireEvent.click(screen.getByRole("button", { name: /^dismiss$/i }));
    expect(
      screen.queryByText(/new signing secret created/i),
    ).not.toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain(created.secret);
  });

  it("shows an inline error when the cap is reached on create", async () => {
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret] }),
      "POST /api/v1/users/me/signing-secrets": () =>
        errResp(
          "at most 5 signing secrets per user; delete one before creating another",
          400,
        ),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() =>
      expect(screen.getByText("default")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: /create new secret/i }));
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      expect(
        screen.getByText(/at most 5 signing secrets/i),
      ).toBeInTheDocument();
    });
  });

  it("DELETEs the right id when confirming a row delete", async () => {
    const second = {
      ...defaultSecret,
      id: "wsec_other",
      name: "other",
      secret: "1111".repeat(16),
      secret_prefix: "1111",
    };
    const deletedIDs: string[] = [];
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret, second] }),
      [`DELETE /api/v1/users/me/signing-secrets/${encodeURIComponent("wsec_other")}`]:
        () => {
          deletedIDs.push("wsec_other");
          return {
            ok: true,
            status: 204,
            text: async () => "",
            json: async () => ({}),
          };
        },
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() =>
      expect(screen.getByText("other")).toBeInTheDocument(),
    );

    const delButtons = screen.getAllByRole("button", { name: /^delete$/i });
    expect(delButtons).toHaveLength(2);
    fireEvent.click(delButtons[1]);
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    await waitFor(() => {
      expect(deletedIDs).toEqual(["wsec_other"]);
    });
  });

  it("surfaces a 'cannot delete the last' error inline", async () => {
    const second = {
      ...defaultSecret,
      id: "wsec_other",
      name: "other",
      secret: "1111".repeat(16),
      secret_prefix: "1111",
    };
    global.fetch = makeFetchMock({
      "GET /api/v1/users/me/signing-secrets": () =>
        jsonResp({ secrets: [defaultSecret, second] }),
      [`DELETE /api/v1/users/me/signing-secrets/${encodeURIComponent("wsec_other")}`]:
        () =>
          errResp(
            "cannot delete the last signing secret; create a new one first",
            400,
          ),
    }) as unknown as typeof fetch;

    render(<WebhookSecretsPage />);
    await waitFor(() => expect(screen.getByText("other")).toBeInTheDocument());

    const delButtons = screen.getAllByRole("button", { name: /^delete$/i });
    fireEvent.click(delButtons[1]);
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    await waitFor(() => {
      expect(screen.getByText(/cannot delete the last/i)).toBeInTheDocument();
    });
  });
});
