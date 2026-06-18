import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import WebhooksPage from "./page";

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

describe("Webhooks page", () => {
  // WebhookView from GET /v1/webhooks. GET never returns signing_secret —
  // it's only present on create / rotate responses.
  const webhook = {
    id: "wh_default0001",
    url: "https://app.example.com/inbox",
    description: "",
    events: ["email.received"],
    enabled: true,
    created_at: "2026-04-15T10:00:00Z",
  };

  it("lists existing webhooks (url, events, status) without exposing a secret", async () => {
    global.fetch = makeFetchMock({
      "/v1/webhooks": () => jsonResp({ webhooks: [webhook] }),
    }) as unknown as typeof fetch;

    render(<WebhooksPage />);
    await waitFor(() => {
      expect(screen.getByText(webhook.url)).toBeInTheDocument();
    });
    expect(screen.getByText("email.received")).toBeInTheDocument();
    expect(screen.getByText("enabled")).toBeInTheDocument();
    // No secret column / value in the list view.
    expect(document.body.innerHTML).not.toContain("signing_secret");
  });

  it("creates a webhook, reveals the signing secret once, and hides it on dismiss", async () => {
    const created = {
      id: "wh_new0002",
      url: "https://app.example.com/hook2",
      description: "",
      events: ["email.received"],
      enabled: true,
      created_at: "2026-04-27T16:10:00Z",
      signing_secret: "whsec_" + "deadbeef".repeat(8),
    };
    let listCallCount = 0;
    global.fetch = makeFetchMock({
      "GET /v1/webhooks": () => {
        listCallCount++;
        if (listCallCount === 1) return jsonResp({ webhooks: [webhook] });
        return jsonResp({
          webhooks: [webhook, { ...created, signing_secret: undefined }],
        });
      },
      "POST /v1/webhooks": () => jsonResp(created, 201),
    }) as unknown as typeof fetch;

    render(<WebhooksPage />);
    await waitFor(() =>
      expect(screen.getByText(webhook.url)).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: /add webhook/i }));
    fireEvent.change(screen.getByPlaceholderText(/your-app\.com/i), {
      target: { value: created.url },
    });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      expect(
        screen.getByText(/copy this signing secret now/i),
      ).toBeInTheDocument();
    });
    const plaintextInput = screen.getByLabelText(
      /plaintext signing secret/i,
    ) as HTMLInputElement;
    expect(plaintextInput.value).toBe(created.signing_secret);

    fireEvent.click(screen.getByRole("button", { name: /^dismiss$/i }));
    expect(
      screen.queryByText(/copy this signing secret now/i),
    ).not.toBeInTheDocument();
    expect(document.body.innerHTML).not.toContain(created.signing_secret);
  });

  it("copies the revealed secret to the clipboard and flips the Copy label", async () => {
    const created = {
      id: "wh_new0003",
      url: "https://app.example.com/hook3",
      events: ["email.received"],
      enabled: true,
      created_at: "2026-04-27T16:10:00Z",
      signing_secret: "whsec_copyme",
    };
    global.fetch = makeFetchMock({
      "GET /v1/webhooks": () => jsonResp({ webhooks: [webhook] }),
      "POST /v1/webhooks": () => jsonResp(created, 201),
    }) as unknown as typeof fetch;

    render(<WebhooksPage />);
    await waitFor(() =>
      expect(screen.getByText(webhook.url)).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /add webhook/i }));
    fireEvent.change(screen.getByPlaceholderText(/your-app\.com/i), {
      target: { value: created.url },
    });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() =>
      expect(
        screen.getByLabelText(/plaintext signing secret/i),
      ).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /^copy$/i }));
    expect(writeText).toHaveBeenCalledWith(created.signing_secret);
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: /^copied$/i }),
      ).toBeInTheDocument();
    });
  });

  it("shows an inline error when create fails", async () => {
    global.fetch = makeFetchMock({
      "GET /v1/webhooks": () => jsonResp({ webhooks: [webhook] }),
      "POST /v1/webhooks": () => errResp("invalid url", 400),
    }) as unknown as typeof fetch;

    render(<WebhooksPage />);
    await waitFor(() =>
      expect(screen.getByText(webhook.url)).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: /add webhook/i }));
    fireEvent.change(screen.getByPlaceholderText(/your-app\.com/i), {
      target: { value: "https://bad" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^create$/i }));

    await waitFor(() => {
      expect(screen.getByText(/invalid url/i)).toBeInTheDocument();
    });
  });

  it("rotates the secret and reveals the new one", async () => {
    global.fetch = makeFetchMock({
      "GET /v1/webhooks": () => jsonResp({ webhooks: [webhook] }),
      [`POST /v1/webhooks/${encodeURIComponent(webhook.id)}/rotate-secret`]: () =>
        jsonResp({
          signing_secret: "whsec_rotated",
          previous_secret_expires_at: "2026-05-01T00:00:00Z",
        }),
    }) as unknown as typeof fetch;

    render(<WebhooksPage />);
    await waitFor(() =>
      expect(screen.getByText(webhook.url)).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: /rotate secret/i }));

    await waitFor(() => {
      expect(
        screen.getByText(/copy the new signing secret now/i),
      ).toBeInTheDocument();
    });
    const plaintextInput = screen.getByLabelText(
      /plaintext signing secret/i,
    ) as HTMLInputElement;
    expect(plaintextInput.value).toBe("whsec_rotated");
  });

  it("DELETEs the right id when confirming a row delete", async () => {
    const second = {
      ...webhook,
      id: "wh_other",
      url: "https://app.example.com/other",
    };
    const deletedIDs: string[] = [];
    global.fetch = makeFetchMock({
      "GET /v1/webhooks": () => jsonResp({ webhooks: [webhook, second] }),
      [`DELETE /v1/webhooks/${encodeURIComponent("wh_other")}`]: () => {
        deletedIDs.push("wh_other");
        return {
          ok: true,
          status: 204,
          text: async () => "",
          json: async () => ({}),
        };
      },
    }) as unknown as typeof fetch;

    render(<WebhooksPage />);
    await waitFor(() =>
      expect(screen.getByText(second.url)).toBeInTheDocument(),
    );

    const delButtons = screen.getAllByRole("button", { name: /^delete$/i });
    expect(delButtons).toHaveLength(2);
    fireEvent.click(delButtons[1]);
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    await waitFor(() => {
      expect(deletedIDs).toEqual(["wh_other"]);
    });
  });
});
