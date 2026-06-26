// EmailHtmlBody renders UNTRUSTED inbound email HTML. These tests pin the two
// security controls — DOMPurify sanitization and the iframe CSP / remote-image
// blocking — plus the "Load images" opt-in, since a silent regression in any of
// them reintroduces XSS or tracking-pixel leakage.

import { render, screen, fireEvent } from "@testing-library/react";
import { EmailHtmlBody } from "./EmailHtmlBody";

function srcdocOf(): string {
  const frame = screen.getByTitle("Email body") as HTMLIFrameElement;
  return frame.getAttribute("srcdoc") ?? "";
}

describe("EmailHtmlBody", () => {
  it("strips scripts, event handlers, and javascript: URLs", () => {
    render(
      <EmailHtmlBody
        html={
          '<div>ok <b>bold</b>' +
          '<script>window.pwned=1</script>' +
          '<img src="x" onerror="alert(1)">' +
          '<a href="javascript:alert(1)">x</a>' +
          "<iframe src=\"http://evil\"></iframe></div>"
        }
      />,
    );
    const doc = srcdocOf();
    expect(doc).toContain("<b>bold</b>"); // benign markup survives
    expect(doc).not.toContain("<script");
    expect(doc).not.toContain("onerror");
    expect(doc).not.toContain("javascript:");
    expect(doc).not.toContain("<iframe");
  });

  it("renders the iframe sandboxed with no script execution", () => {
    render(<EmailHtmlBody html="<p>hi</p>" />);
    const frame = screen.getByTitle("Email body") as HTMLIFrameElement;
    expect(frame.getAttribute("sandbox")).toBe("allow-same-origin allow-popups");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-scripts");
  });

  it("injects a restrictive CSP that blocks remote fetches when images are off", () => {
    render(<EmailHtmlBody html="<p>hi</p>" />);
    const doc = srcdocOf();
    expect(doc).toContain("Content-Security-Policy");
    expect(doc).toContain("default-src 'none'");
    // images blocked => only data: images, never http/https
    expect(doc).toMatch(/img-src data:/);
    expect(doc).not.toMatch(/img-src[^;"]*https?:/);
  });

  it("blocks a remote tracking image by default and shows the banner", () => {
    render(<EmailHtmlBody html={'<img src="http://track.example.com/p.gif">'} />);
    const doc = srcdocOf();
    expect(doc).not.toContain("track.example.com");
    expect(screen.getByText(/Remote images blocked/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Load images/i })).toBeInTheDocument();
  });

  it("blocks non-<img> remote vectors (td background, video poster) too", () => {
    render(
      <EmailHtmlBody
        html={
          '<table><tr><td background="http://evil/bg.png">x</td></tr></table>' +
          '<video poster="http://evil/poster.png"></video>'
        }
      />,
    );
    const doc = srcdocOf();
    expect(doc).not.toContain("evil/bg.png");
    expect(doc).not.toContain("evil/poster.png");
    // and the banner fires (blockedRemote was set), no false reassurance
    expect(screen.getByText(/Remote images blocked/i)).toBeInTheDocument();
  });

  it("loads images after the user opts in", () => {
    render(<EmailHtmlBody html={'<img src="http://track.example.com/p.gif">'} />);
    fireEvent.click(screen.getByRole("button", { name: /Load images/i }));
    const doc = srcdocOf();
    expect(doc).toContain("track.example.com"); // src restored
    expect(doc).toMatch(/img-src[^;"]*https?:/); // CSP now permits remote images
    expect(screen.queryByText(/Remote images blocked/i)).not.toBeInTheDocument();
  });

  it("shows no banner when there is nothing remote to block", () => {
    render(<EmailHtmlBody html="<p>just <b>text</b></p>" />);
    expect(screen.queryByText(/Remote images blocked/i)).not.toBeInTheDocument();
  });
});
