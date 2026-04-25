import type { Metadata } from "next";
import Link from "next/link";

const TITLE = "Blog — e2a, email for AI agents";
const DESC =
  "Notes on building authenticated email for AI agents — tutorials, protocol deep-dives, and product updates from the e2a team.";

export const metadata: Metadata = {
  title: { absolute: TITLE },
  description: DESC,
  alternates: { canonical: "/blog" },
  openGraph: {
    title: TITLE,
    description: DESC,
    url: "https://e2a.dev/blog",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESC,
  },
};

export default function BlogLayout({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        background: "#FDFAF6",
        color: "#1C1A17",
        minHeight: "100vh",
        fontFamily: "var(--font-sans, system-ui)",
      }}
    >
      <nav
        style={{
          borderBottom: "0.5px solid #E8E0D4",
          background: "#FDFAF6",
          position: "sticky",
          top: 0,
          zIndex: 50,
        }}
      >
        <div
          style={{
            maxWidth: 760,
            margin: "0 auto",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            padding: "14px 32px",
          }}
        >
          <Link
            href="/"
            style={{
              fontFamily: "'IBM Plex Mono', monospace",
              fontWeight: 600,
              fontSize: 15,
              color: "#1C1A17",
              textDecoration: "none",
            }}
          >
            e2a
          </Link>
          <div style={{ display: "flex", alignItems: "center", gap: 18 }}>
            <Link href="/blog" style={{ fontSize: 13, color: "#7A6F63", textDecoration: "none" }}>
              Blog
            </Link>
            <Link href="/api-docs" style={{ fontSize: 13, color: "#7A6F63", textDecoration: "none" }}>
              Docs
            </Link>
            <Link
              href="/get-started"
              style={{
                fontSize: 13,
                fontWeight: 500,
                background: "#1C1A17",
                color: "#FDFAF6",
                padding: "7px 14px",
                borderRadius: 7,
                textDecoration: "none",
              }}
            >
              Start building →
            </Link>
          </div>
        </div>
      </nav>
      <main style={{ maxWidth: 720, margin: "0 auto", padding: "56px 32px 80px" }}>{children}</main>
      <footer
        style={{
          padding: "20px 32px",
          borderTop: "0.5px solid #E8E0D4",
          display: "flex",
          justifyContent: "space-between",
          maxWidth: 720,
          margin: "0 auto",
        }}
      >
        <span style={{ fontFamily: "'IBM Plex Mono', monospace", fontWeight: 600, fontSize: 13 }}>
          e2a
        </span>
        <span style={{ fontSize: 12, color: "#A89A8A" }}>MIT License</span>
      </footer>
    </div>
  );
}
