import type { NextConfig } from "next";
import createMDX from "@next/mdx";

const isDev = process.env.NODE_ENV !== "production";

const withMDX = createMDX({});

const nextConfig: NextConfig = {
  output: isDev ? undefined : "export",
  pageExtensions: ["ts", "tsx", "md", "mdx"],
  ...(isDev && {
    rewrites: async () => [
      { source: "/api/:path*", destination: "http://localhost:8080/api/:path*" },
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
