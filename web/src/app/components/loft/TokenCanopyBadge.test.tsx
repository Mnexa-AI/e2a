// The parent-brand endorsement lockup. Small surface, but it appears on the
// public marketing footer, so the link target and the "open in a new tab
// safely" attributes are worth pinning.

import { render, screen } from "@testing-library/react";
import { TokenCanopyBadge } from "./TokenCanopyBadge";

describe("TokenCanopyBadge", () => {
  it("links to Token Canopy and opens it safely in a new tab", () => {
    render(<TokenCanopyBadge />);
    const link = screen.getByTestId("token-canopy-badge");
    expect(link).toHaveTextContent("by Token Canopy");
    expect(link).toHaveAttribute("href", "https://tokencanopy.com");
    expect(link).toHaveAttribute("target", "_blank");
    // Without noopener the opened page gets a handle on window.opener.
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
  });

  // Nesting an <a> inside an <a> is invalid HTML and browsers recover from it
  // unpredictably, so callers already inside a link opt out with href={null}.
  it("renders as plain text, not an anchor, when href is null", () => {
    render(<TokenCanopyBadge href={null} />);
    const badge = screen.getByTestId("token-canopy-badge");
    expect(badge.tagName).toBe("SPAN");
    expect(badge).toHaveTextContent("by Token Canopy");
  });

  it("accepts a custom destination", () => {
    render(<TokenCanopyBadge href="https://example.com/about" />);
    expect(screen.getByTestId("token-canopy-badge")).toHaveAttribute(
      "href",
      "https://example.com/about",
    );
  });
});
