import type { Metadata } from "next";
import { Geist, Geist_Mono, Playfair_Display } from "next/font/google";
import "./globals.css";
import { AuthProvider } from "./components/AuthProvider";
import { ThemeProvider } from "./components/ThemeProvider";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

const playfair = Playfair_Display({
  variable: "--font-playfair",
  subsets: ["latin"],
  style: ["normal", "italic"],
});

const SITE = "https://e2a.dev";
const SITE_NAME = "e2a";
const ROOT_TITLE = "e2a — Authenticated Email for AI Agents";
const ROOT_DESC =
  "Authenticated email gateway for AI agents: verified sender identity, SPF/DKIM, agent-to-agent routing, conversation threading. Send and receive real email from your agent with a signed webhook or WebSocket.";

export const metadata: Metadata = {
  metadataBase: new URL(SITE),
  title: {
    default: ROOT_TITLE,
    template: "%s — e2a",
  },
  description: ROOT_DESC,
  applicationName: SITE_NAME,
  keywords: [
    "email api for ai agents",
    "agent email gateway",
    "authenticated email webhook",
    "SPF DKIM AI agents",
    "agent-to-agent email",
    "conversation id email threading",
    "smtp relay for AI agents",
    "send email from python agent",
    "ai assistant email",
    "openclaw",
    "email for openclaw",
  ],
  authors: [{ name: "e2a" }],
  alternates: {
    canonical: "/",
  },
  openGraph: {
    title: ROOT_TITLE,
    description: ROOT_DESC,
    url: SITE,
    siteName: SITE_NAME,
    type: "website",
    locale: "en_US",
  },
  twitter: {
    card: "summary_large_image",
    title: ROOT_TITLE,
    description: ROOT_DESC,
  },
  verification: {
    google: "dnrzXd6lqYXs5pAX6M4eKaEX0vCY_gvYzRyllQF4gDM",
  },
  robots: {
    index: true,
    follow: true,
    googleBot: {
      index: true,
      follow: true,
      "max-snippet": -1,
      "max-image-preview": "large",
      "max-video-preview": -1,
    },
  },
  icons: {
    icon: "/favicon.ico",
  },
};

const jsonLd = {
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  name: SITE_NAME,
  description: ROOT_DESC,
  url: SITE,
  applicationCategory: "DeveloperApplication",
  operatingSystem: "Any",
  offers: {
    "@type": "Offer",
    price: "0",
    priceCurrency: "USD",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`${geistSans.variable} ${geistMono.variable} ${playfair.variable} antialiased`}
      suppressHydrationWarning
    >
      <body className="min-h-screen flex flex-col">
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }}
        />
        <AuthProvider>
          <ThemeProvider>
            {children}
          </ThemeProvider>
        </AuthProvider>
      </body>
    </html>
  );
}
