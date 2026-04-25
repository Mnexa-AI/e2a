import type { Metadata } from "next";

const TITLE = "Docs — e2a email API for AI agents";
const DESC =
  "Developer docs for e2a: authenticated email API for AI agents. SDKs, CLI, webhook payload reference, SPF/DKIM auth headers, conversation threading, agent-to-agent routing.";

export const metadata: Metadata = {
  title: { absolute: TITLE },
  description: DESC,
  alternates: { canonical: "/docs" },
  openGraph: {
    title: TITLE,
    description: DESC,
    url: "https://e2a.dev/docs",
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESC,
  },
};

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
