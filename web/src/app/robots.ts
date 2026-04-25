import type { MetadataRoute } from "next";

// Required for static export (output: "export")
export const dynamic = "force-static";

const SITE = "https://e2a.dev";

export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: "*",
        allow: "/",
        disallow: ["/dashboard", "/api/", "/api-keys", "/domains", "/feedback"],
      },
    ],
    sitemap: `${SITE}/sitemap.xml`,
    host: SITE,
  };
}
