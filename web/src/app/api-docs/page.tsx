import type { Metadata } from "next";
import { SITE_URL } from "../../lib/site";

const TITLE = "API Reference — e2a email gateway for AI agents";
const DESC =
  "REST API reference for e2a: register agents, verify domains, send and reply to email, manage webhooks, consume WebSocket notifications. Authenticated with API keys.";

export const metadata: Metadata = {
  title: { absolute: TITLE },
  description: DESC,
  alternates: { canonical: "/api-docs" },
  openGraph: {
    title: TITLE,
    description: DESC,
    url: `${SITE_URL}/api-docs`,
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESC,
  },
};

// The Scalar iframe renders with its own theme (light) because the static
// HTML in public/scalar.html doesn't yet read from Loft tokens. We wrap it
// in a Loft-style container so the surrounding chrome matches the rest of
// the app; the iframe contents stay light to keep Scalar's typography and
// inline code styling readable. Future: pass Loft tokens into scalar.html.
export default function APIDocsPage() {
  return (
    <div
      style={{
        background: "var(--bg)",
        minHeight: "100vh",
        padding: "12px",
        // Light-mode overrides for Scalar's own CSS vars — keeps the API
        // explorer readable until we wire Loft tokens into scalar.html.
        "--background": "#fafafa",
        "--foreground": "#111111",
        "--accent": "#B84A20",
        "--accent-light": "#E26534",
        "--muted": "#6b7280",
        "--border": "#e5e7eb",
        "--surface": "#ffffff",
        colorScheme: "light",
      } as React.CSSProperties}
    >
      <div
        className="overflow-hidden"
        style={{
          border: "1px solid var(--ink-border)",
          borderRadius: "var(--r-lg)",
          background: "var(--ink)",
          height: "calc(100vh - 24px)",
        }}
      >
        <iframe
          src="/scalar.html"
          className="w-full border-0 block"
          style={{
            height: "100%",
            background: "#ffffff",
          }}
          title="API Docs"
        />
      </div>
    </div>
  );
}
