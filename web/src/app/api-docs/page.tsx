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

// The API reference is rendered by Redoc inside an iframe at
// /scalar.html — that page configures Redoc with the Loft palette
// directly, so the iframe contents already match the surrounding app
// chrome. This wrapper just provides the page background + ink-bordered
// frame.
export default function APIDocsPage() {
  return (
    <div
      style={{
        background: "var(--bg)",
        minHeight: "100vh",
        padding: "12px",
      }}
    >
      <div
        className="overflow-hidden"
        style={{
          border: "1px solid var(--border)",
          borderRadius: "var(--r-lg)",
          background: "var(--bg-panel)",
          height: "calc(100vh - 24px)",
        }}
      >
        <iframe
          src="/scalar.html"
          className="w-full border-0 block"
          style={{
            height: "100%",
            background: "var(--bg)",
          }}
          title="API Docs"
        />
      </div>
    </div>
  );
}
