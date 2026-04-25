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
    ],
  }),
};

export default withMDX(nextConfig);
