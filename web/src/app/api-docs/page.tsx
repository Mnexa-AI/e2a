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

export default function APIDocsPage() {
  return (
    <div
      style={{
        "--background": "#fafafa",
        "--foreground": "#111111",
        "--accent": "#2563eb",
        "--accent-light": "#3b82f6",
        "--muted": "#6b7280",
        "--border": "#e5e7eb",
        "--surface": "#ffffff",
        colorScheme: "light",
      } as React.CSSProperties}
    >
      <iframe
        src="/scalar.html"
        className="w-full border-0"
        style={{ height: "100vh" }}
        title="API Docs"
      />
    </div>
  );
}
