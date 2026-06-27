import type { NextConfig } from "next";
import { fileURLToPath } from "node:url";
import createMDX from "@next/mdx";

const isDev = process.env.NODE_ENV !== "production";

// The monorepo root (parent of web/). Pinned as Turbopack's root so it resolves
// the sibling @e2a/ui workspace (file:../design-system) — which lives OUTSIDE
// web/. Without this, Turbopack infers web/ as the root and rejects @e2a/ui as
// "outside the project". Portable: resolves to the repo root locally and /app
// in the web Docker image. Also silences the "inferred workspace root" warning.
const repoRoot = fileURLToPath(new URL("..", import.meta.url));

const withMDX = createMDX({});

const nextConfig: NextConfig = {
  output: isDev ? undefined : "export",
  turbopack: { root: repoRoot },
  pageExtensions: ["ts", "tsx", "md", "mdx"],
  ...(isDev && {
    rewrites: async () => [
      { source: "/api/:path*", destination: "http://localhost:8080/api/:path*" },
      // The typed /v1 surface + /v1 WebSocket + HITL magic-link pages
      // (/v1/approve, /v1/reject) are backend-owned and not under /api/* —
      // proxy them to the Go server in dev, mirroring the Caddyfile in prod,
      // or the dashboard's /v1 fetches and the HITL links 404.
      { source: "/v1/:path*", destination: "http://localhost:8080/v1/:path*" },
      // OAuth surface moved to /oauth2/* (Slice 5b) — proxy it (and the JWKS +
      // AS discovery docs the backend owns) to the Go server in dev, mirroring
      // the Caddyfile in prod. Without these the consent UI + token flow 404.
      { source: "/oauth2/:path*", destination: "http://localhost:8080/oauth2/:path*" },
      { source: "/agent/identity", destination: "http://localhost:8080/agent/identity" },
      { source: "/.well-known/jwks.json", destination: "http://localhost:8080/.well-known/jwks.json" },
      { source: "/.well-known/oauth-authorization-server", destination: "http://localhost:8080/.well-known/oauth-authorization-server" },
    ],
  }),
};

export default withMDX(nextConfig);
