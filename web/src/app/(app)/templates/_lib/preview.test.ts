import {
  escapeHTML,
  exampleData,
  forceDarkPreview,
  substituteVars,
} from "./preview";

describe("substituteVars", () => {
  it("substitutes flat {{var}} slots", () => {
    expect(
      substituteVars("Welcome to {{company_name}}", { company_name: "Acme" }),
    ).toBe("Welcome to Acme");
  });

  it("renders missing variables as empty strings (matching the engine)", () => {
    expect(substituteVars("Hi {{who}}!", {})).toBe("Hi !");
  });

  it("HTML-escapes {{var}} but not {{{var}}} when escape is on", () => {
    const out = substituteVars(
      "<td>{{name}}</td>{{{items_html}}}",
      { name: "A & B <script>", items_html: "<tr><td>1x Pro</td></tr>" },
      { escape: true },
    );
    expect(out).toBe(
      "<td>A &amp; B &lt;script&gt;</td><tr><td>1x Pro</td></tr>",
    );
  });

  it("does not escape in plain parts (no escape option)", () => {
    expect(substituteVars("{{x}}", { x: "a<b" })).toBe("a<b");
  });

  it("handles {{{raw}}} before {{escaped}} so braces never leak", () => {
    const out = substituteVars("{{{r}}} and {{e}}", { r: "<hr>", e: "ok" });
    expect(out).toBe("<hr> and ok");
    expect(out).not.toContain("{");
  });
});

describe("escapeHTML", () => {
  it("escapes the five significant characters", () => {
    expect(escapeHTML(`&<>"'`)).toBe("&amp;&lt;&gt;&quot;&#39;");
  });
});

describe("forceDarkPreview", () => {
  it("rewrites the prefers-color-scheme media condition to `all`", () => {
    const html =
      "<html><head><style>@media (prefers-color-scheme: dark) { .x{color:#fff !important} }</style></head><body></body></html>";
    const out = forceDarkPreview(html);
    expect(out).toContain("@media all");
    expect(out).not.toContain("prefers-color-scheme");
  });

  it("injects color-scheme: dark into <head> when present", () => {
    const out = forceDarkPreview("<html><head></head><body>x</body></html>");
    expect(out).toContain("<style>:root{color-scheme:dark}</style></head>");
  });

  it("prepends the color-scheme style for fragment documents", () => {
    const out = forceDarkPreview("<p>hello</p>");
    expect(out.startsWith("<style>:root{color-scheme:dark}</style>")).toBe(
      true,
    );
  });
});

describe("exampleData", () => {
  it("maps variable names to their catalog examples", () => {
    expect(
      exampleData([
        { name: "a", example: "1" },
        { name: "b", example: "2" },
      ]),
    ).toEqual({ a: "1", b: "2" });
  });
});
