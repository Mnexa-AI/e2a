import type { Metadata } from "next";

const TITLE = "Python SDK docs — send email from your AI agent";
const DESC =
  "Python SDK guide for e2a. Install the e2a package, send and receive authenticated email from your AI agent, use WebSocket for real-time delivery, thread conversations across agents and humans.";

export const metadata: Metadata = {
  title: { absolute: TITLE },
  description: DESC,
  alternates: { canonical: "/docs/python" },
  openGraph: {
    title: TITLE,
    description: DESC,
    url: "https://e2a.dev/docs/python",
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESC,
  },
};

export default function DocsPythonLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
