import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import TemplatesPage from "./page";

// Mock next/link → plain anchors, and next/navigation for the
// UseStarterButton's router.push after a from_starter create.
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

const mockRouterPush = jest.fn();
jest.mock("next/navigation", () => ({
  useRouter: () => ({ push: mockRouterPush }),
}));
beforeEach(() => mockRouterPush.mockClear());

// --- Fetch mock helpers (same shape as the webhooks page tests). ---

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

// --- Fixtures ---

const template = {
  id: "tpl_abc123",
  name: "Order receipt",
  alias: "receipt",
  subject: "Your receipt for {{order_id}}",
  created_at: "2026-06-20T10:00:00Z",
  updated_at: "2026-06-28T09:30:00Z",
};

const starterCatalog = {
  items: [
    {
      alias: "welcome",
      name: "Welcome",
      description: "Warm, brief welcome for new users.",
      version: "1.0.0",
      subject: "Welcome to {{company_name}}",
      variables: [
        {
          name: "company_name",
          required: true,
          raw: false,
          description: "Sender company or product name.",
          example: "Acme",
        },
        {
          name: "items_html",
          required: false,
          raw: true,
          description: "Raw pre-rendered rows.",
          example: "<tr><td>1x Pro</td></tr>",
        },
      ],
    },
  ],
  next_cursor: null,
};

const starterDetail = {
  ...starterCatalog.items[0],
  text: "Welcome aboard, courtesy of {{company_name}}.",
  html:
    "<html><head><style>@media (prefers-color-scheme: dark){.x{color:#fff !important}}</style></head><body><p>Hello from {{company_name}}</p><table>{{{items_html}}}</table></body></html>",
};

const emptyPage = { items: [], next_cursor: null };

describe("Templates page", () => {
  it("lists the account's templates with name, alias, subject and updated date", async () => {
    global.fetch = makeFetchMock({
      "/v1/templates": () =>
        jsonResp({ items: [template], next_cursor: null }),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText("Order receipt")).toBeInTheDocument(),
    );
    expect(screen.getByText("receipt")).toBeInTheDocument();
    expect(
      screen.getByText("Your receipt for {{order_id}}"),
    ).toBeInTheDocument();
    // Name links through to the edit view.
    const link = screen.getByRole("link", { name: "Order receipt" });
    expect(link).toHaveAttribute("href", "/templates/edit?id=tpl_abc123");
  });

  it("shows the empty state and leads with the starter gallery when there are no templates", async () => {
    global.fetch = makeFetchMock({
      "/v1/templates": () => jsonResp(emptyPage),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText(/no templates yet/i)).toBeInTheDocument(),
    );
    expect(screen.getByText("Start from a starter")).toBeInTheDocument();
  });

  it("renders a starter card from the catalog: name, description, variables with required/raw badges", async () => {
    global.fetch = makeFetchMock({
      "/v1/templates": () => jsonResp(emptyPage),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText("Welcome")).toBeInTheDocument(),
    );
    expect(
      screen.getByText("Warm, brief welcome for new users."),
    ).toBeInTheDocument();
    expect(screen.getByText("company_name")).toBeInTheDocument();
    expect(screen.getByText("items_html")).toBeInTheDocument();
    expect(screen.getByText("required")).toBeInTheDocument();
    expect(screen.getByText("raw")).toBeInTheDocument();
  });

  it("DELETEs the right template id after confirm", async () => {
    const deletedIDs: string[] = [];
    global.fetch = makeFetchMock({
      "GET /v1/templates": () =>
        jsonResp({ items: [template], next_cursor: null }),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
      [`DELETE /v1/templates/${encodeURIComponent(template.id)}?confirm=DELETE`]: () => {
        deletedIDs.push(template.id);
        // Uniform delete contract: 200 + {deleted:true, id}.
        return {
          ok: true,
          status: 200,
          text: async () => JSON.stringify({ deleted: true, id: template.id }),
          json: async () => ({ deleted: true, id: template.id }),
        };
      },
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText("Order receipt")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));
    await waitFor(() => expect(deletedIDs).toEqual([template.id]));
  });

  it("Use this template POSTs from_starter and navigates to the new template's edit view", async () => {
    const posted: unknown[] = [];
    global.fetch = makeFetchMock({
      "GET /v1/templates": () => jsonResp(emptyPage),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
      "POST /v1/templates": (init) => {
        posted.push(JSON.parse(String(init?.body)));
        return jsonResp({ ...template, id: "tpl_new1" }, 201);
      },
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText("Welcome")).toBeInTheDocument(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: /use this template/i }),
    );
    await waitFor(() =>
      expect(mockRouterPush).toHaveBeenCalledWith(
        "/templates/edit?id=tpl_new1",
      ),
    );
    expect(posted).toEqual([{ from_starter: "welcome" }]);
  });

  it("handles 409 alias_taken by prompting for a different alias and retrying", async () => {
    const posted: Array<Record<string, unknown>> = [];
    global.fetch = makeFetchMock({
      "GET /v1/templates": () => jsonResp(emptyPage),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
      "POST /v1/templates": (init) => {
        const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
        posted.push(body);
        if (!body.alias) {
          return jsonResp(
            { error: { code: "alias_taken", message: "alias taken" } },
            409,
          );
        }
        return jsonResp({ ...template, id: "tpl_new2" }, 201);
      },
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText("Welcome")).toBeInTheDocument(),
    );
    fireEvent.click(
      screen.getByRole("button", { name: /use this template/i }),
    );

    // Conflict → inline alias prompt.
    const aliasInput = await screen.findByLabelText(/new template alias/i);
    fireEvent.change(aliasInput, { target: { value: "welcome-agent" } });
    fireEvent.click(
      screen.getByRole("button", { name: /create as this alias/i }),
    );

    await waitFor(() =>
      expect(mockRouterPush).toHaveBeenCalledWith(
        "/templates/edit?id=tpl_new2",
      ),
    );
    expect(posted).toEqual([
      { from_starter: "welcome" },
      { from_starter: "welcome", alias: "welcome-agent" },
    ]);
  });

  it("previews a starter: verbatim master + example substitution in a sandboxed iframe, with text tab and dark toggle", async () => {
    global.fetch = makeFetchMock({
      "GET /v1/templates": () => jsonResp(emptyPage),
      "/v1/starter-templates": () => jsonResp(starterCatalog),
      "/v1/starter-templates/welcome": () => jsonResp(starterDetail),
    }) as unknown as typeof fetch;

    render(<TemplatesPage />);
    await waitFor(() =>
      expect(screen.getByText("Welcome")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: /^preview$/i }));

    // Substituted subject appears once the detail loads.
    await waitFor(() =>
      expect(screen.getByText("Welcome to Acme")).toBeInTheDocument(),
    );

    // HTML part: sandboxed iframe whose srcdoc is the master with
    // examples substituted — raw slot verbatim, {{}} slot escaped.
    const iframe = document.querySelector("iframe");
    expect(iframe).not.toBeNull();
    expect(iframe?.getAttribute("sandbox")).toBe("");
    const srcdoc = iframe?.getAttribute("srcdoc") ?? "";
    expect(srcdoc).toContain("Hello from Acme");
    expect(srcdoc).toContain("<tr><td>1x Pro</td></tr>");
    expect(srcdoc).not.toContain("{{");

    // Dark toggle rewrites the prefers-color-scheme condition in the
    // display copy so the master's dark overrides actually apply.
    fireEvent.click(screen.getByRole("button", { name: /^dark$/i }));
    const darkDoc =
      document.querySelector("iframe")?.getAttribute("srcdoc") ?? "";
    expect(darkDoc).toContain("@media all");
    expect(darkDoc).not.toContain("prefers-color-scheme");
    expect(darkDoc).toContain("color-scheme:dark");

    // Plain-text tab shows the substituted text part in a <pre>.
    fireEvent.click(screen.getByRole("button", { name: /plain-text part/i }));
    expect(
      screen.getByText(/welcome aboard, courtesy of acme\./i),
    ).toBeInTheDocument();
  });
});
