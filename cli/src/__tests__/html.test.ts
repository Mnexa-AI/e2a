import { describe, it, expect } from "vitest";
import { htmlToText } from "../html.js";

describe("htmlToText", () => {
  it("strips tags and preserves block structure as line breaks", () => {
    const html = "<div><p>First para</p><p>Second para</p></div>";
    const text = htmlToText(html);
    expect(text).toContain("First para");
    expect(text).toContain("Second para");
    expect(text.indexOf("First para")).toBeLessThan(text.indexOf("Second para"));
  });

  it("drops script and style contents entirely", () => {
    const html = "<style>.x{color:red}</style><script>alert('hi')</script><p>Visible</p>";
    const text = htmlToText(html);
    expect(text).toBe("Visible");
    expect(text).not.toContain("color");
    expect(text).not.toContain("alert");
  });

  it("decodes named and numeric entities", () => {
    expect(htmlToText("<p>a &amp; b &lt;c&gt; &#8212; d &#x2014; e</p>")).toBe(
      "a & b <c> — d — e",
    );
  });

  it("converts <br> to newlines", () => {
    const text = htmlToText("line one<br>line two<br/>line three");
    expect(text).toBe("line one\nline two\nline three");
  });

  it("collapses runs of whitespace", () => {
    const text = htmlToText("<p>a    lot\t\tof   space</p>");
    expect(text).toBe("a lot of space");
  });

  it("handles a realistic email snippet", () => {
    const html = `<div style="max-width:480px"><h2>Status</h2>
      <p>Merged <b>#357</b> &mdash; tests green.</p>
      <ul><li>next: docs</li></ul></div>`;
    const text = htmlToText(html);
    expect(text).toContain("Status");
    expect(text).toContain("Merged #357");
    expect(text).toContain("next: docs");
    expect(text).not.toContain("<");
  });
});
