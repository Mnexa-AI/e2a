import type { Metadata } from "next";
import { SITE_URL } from "../../lib/site";

const TITLE = "Python SDK — email for AI agents";
const DESC =
  "Install the e2a Python SDK. AsyncE2AClient, WebSocket listener, InboundEmail parser, reply helpers, attachments. pip install e2a.";

export const metadata: Metadata = {
  title: { absolute: TITLE },
  description: DESC,
  alternates: { canonical: "/python-sdk" },
  openGraph: {
    title: TITLE,
    description: DESC,
    url: `${SITE_URL}/python-sdk`,
    type: "article",
  },
  twitter: {
    card: "summary_large_image",
    title: TITLE,
    description: DESC,
  },
};

export default function PythonSDKLayout({ children }: { children: React.ReactNode }) {
  return <>{children}</>;
}
