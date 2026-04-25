// Site-level config that the marketing/dashboard surface needs at build time.
// All values resolve from public env vars so a fork or self-host can override
// without touching source. Defaults are localhost-friendly so `next dev` and
// the static build both work out of the box without any env setup.

export const SITE_URL =
  process.env.NEXT_PUBLIC_SITE_URL?.replace(/\/$/, "") || "http://localhost:3000";

export const SITE_NAME = process.env.NEXT_PUBLIC_SITE_NAME || "e2a";

// Shared agent domain for slug-based registration (e.g. "agents.example.com").
// Empty when the deployment doesn't offer a shared domain — in that mode the
// landing page copy reads as "your custom domain" rather than naming the
// shared host.
export const AGENTS_DOMAIN = process.env.NEXT_PUBLIC_AGENTS_DOMAIN || "";

// Address shown in the in-app feedback form. Empty hides the link.
export const FEEDBACK_EMAIL = process.env.NEXT_PUBLIC_FEEDBACK_EMAIL || "";

// Google Search Console verification token — only emitted into <head> when
// configured, so forks don't accidentally inherit the upstream property.
export const GOOGLE_SITE_VERIFICATION =
  process.env.NEXT_PUBLIC_GOOGLE_SITE_VERIFICATION || "";
