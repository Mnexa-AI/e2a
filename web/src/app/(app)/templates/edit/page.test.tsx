import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import TemplateEditPage from "./page";

// Mock next/link + next/navigation. The edit page reads ?id= via
// useSearchParams (under Suspense) — same mocking pattern as the
// per-agent inbox pages.
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

let mockSearch = new URLSearchParams("id=tpl_abc123");
jest.mock("next/navigation", () => ({
  useSearchParams: () => mockSearch,
  useRouter: () => ({ push: jest.fn(), replace: jest.fn() }),
}));
beforeEach(() => {
  mockSearch = new URLSearchParams("id=tpl_abc123");
});

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

const template = {
  id: "tpl_abc123",
  name: "Order receipt",
  alias: "receipt",
  subject: "Your receipt for {{order_id}}",
  body: "Order {{order_id}} total: {{total}}",
  html_body: "<p>Order {{order_id}}: {{total}}</p>",
  created_at: "2026-06-20T10:00:00Z",
  updated_at: "2026-06-28T09:30:00Z",
};

describe("Template edit page", () => {
  it("loads the template into the form fields", async () => {
    global.fetch = makeFetchMock({
      "/v1/templates/tpl_abc123": () => jsonResp(template),
    }) as unknown as typeof fetch;

    render(<TemplateEditPage />);
    await waitFor(() =>
      expect(screen.getByDisplayValue("Order receipt")).toBeInTheDocument(),
    );
    expect(screen.getByDisplayValue("receipt")).toBeInTheDocument();
    expect(
      screen.getByDisplayValue("Your receipt for {{order_id}}"),
    ).toBeInTheDocument();
    expect(
      screen.getByDisplayValue("Order {{order_id}} total: {{total}}"),
    ).toBeInTheDocument();
    expect(
      screen.getByDisplayValue("<p>Order {{order_id}}: {{total}}</p>"),
    ).toBeInTheDocument();
  });

  it("shows an error when ?id= is missing", async () => {
    mockSearch = new URLSearchParams("");
    global.fetch = makeFetchMock({}) as unknown as typeof fetch;

    render(<TemplateEditPage />);
    await waitFor(() =>
      expect(screen.getByText(/missing \?id=/i)).toBeInTheDocument(),
    );
  });

  it("PATCHes the edited fields on save", async () => {
    const patched: unknown[] = [];
    global.fetch = makeFetchMock({
      "GET /v1/templates/tpl_abc123": () => jsonResp(template),
      "PATCH /v1/templates/tpl_abc123": (init) => {
        patched.push(JSON.parse(String(init?.body)));
        return jsonResp({ ...template, name: "Renamed receipt" });
      },
    }) as unknown as typeof fetch;

    render(<TemplateEditPage />);
    await waitFor(() =>
      expect(screen.getByDisplayValue("Order receipt")).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByDisplayValue("Order receipt"), {
      target: { value: "Renamed receipt" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(patched).toHaveLength(1));
    expect(patched[0]).toEqual({
      name: "Renamed receipt",
      alias: "receipt",
      subject: "Your receipt for {{order_id}}",
      body: "Order {{order_id}} total: {{total}}",
      html_body: "<p>Order {{order_id}}: {{total}}</p>",
    });
    await waitFor(() =>
      expect(screen.getByText(/^saved$/i)).toBeInTheDocument(),
    );
  });

  it("surfaces alias_taken conflicts from save inline", async () => {
    global.fetch = makeFetchMock({
      "GET /v1/templates/tpl_abc123": () => jsonResp(template),
      "PATCH /v1/templates/tpl_abc123": () =>
        jsonResp({ error: { code: "alias_taken", message: "taken" } }, 409),
    }) as unknown as typeof fetch;

    render(<TemplateEditPage />);
    await waitFor(() =>
      expect(screen.getByDisplayValue("Order receipt")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() =>
      expect(screen.getByText(/already taken/i)).toBeInTheDocument(),
    );
  });

  it("previews via /v1/templates/validate: renders parts and seeds test-data inputs from suggested_data", async () => {
    const validateBodies: Array<Record<string, unknown>> = [];
    global.fetch = makeFetchMock({
      "GET /v1/templates/tpl_abc123": () => jsonResp(template),
      "POST /v1/templates/validate": (init) => {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        validateBodies.push(body);
        const data = (body.test_data ?? {}) as Record<string, string>;
        return jsonResp({
          valid: true,
          errors: [],
          rendered: {
            subject: `Your receipt for ${data.order_id ?? ""}`,
            body: `Order ${data.order_id ?? ""} total: ${data.total ?? ""}`,
            html_body: `<p>Order ${data.order_id ?? ""}: ${data.total ?? ""}</p>`,
          },
          suggested_data: { order_id: "order_id_value", total: "total_value" },
        });
      },
    }) as unknown as typeof fetch;

    render(<TemplateEditPage />);
    await waitFor(() =>
      expect(screen.getByDisplayValue("Order receipt")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /^preview$/i }));

    // Rendered subject + seeded test-data inputs appear.
    await waitFor(() =>
      expect(
        screen.getByLabelText(/test value for order_id/i),
      ).toBeInTheDocument(),
    );
    expect(validateBodies[0]).toMatchObject({
      subject: template.subject,
      body: template.body,
      html_body: template.html_body,
      test_data: {},
    });

    // Edit a test value and refresh — user-entered data is sent back.
    fireEvent.change(screen.getByLabelText(/test value for order_id/i), {
      target: { value: "ORD-77" },
    });
    fireEvent.click(screen.getByRole("button", { name: /refresh preview/i }));
    await waitFor(() => expect(validateBodies).toHaveLength(2));
    expect(
      (validateBodies[1].test_data as Record<string, string>).order_id,
    ).toBe("ORD-77");
    await waitFor(() =>
      expect(screen.getByText("Your receipt for ORD-77")).toBeInTheDocument(),
    );
    // Rendered HTML part lands in a sandboxed iframe.
    const iframe = document.querySelector("iframe");
    expect(iframe?.getAttribute("sandbox")).toBe("");
    expect(iframe?.getAttribute("srcdoc")).toContain("Order ORD-77:");
  });

  it("lists per-part parse errors when the template is invalid", async () => {
    global.fetch = makeFetchMock({
      "GET /v1/templates/tpl_abc123": () => jsonResp(template),
      "POST /v1/templates/validate": () =>
        jsonResp({
          valid: false,
          errors: [{ part: "body", message: "unclosed variable" }],
        }),
    }) as unknown as typeof fetch;

    render(<TemplateEditPage />);
    await waitFor(() =>
      expect(screen.getByDisplayValue("Order receipt")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /^preview$/i }));
    await waitFor(() =>
      expect(screen.getByText(/does not parse/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/unclosed variable/i)).toBeInTheDocument();
  });
});
